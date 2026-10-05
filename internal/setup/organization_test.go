package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/philippwallrafen/gh-actions-quota/internal/quota"
)

func organizationFixture(t *testing.T, plan string, billingStatus int) (*setup, *fakeGH, *[]string) {
	t.Helper()
	gh := &fakeGH{ownerType: "Organization"}
	s, _, _ := setupFixture(t, gh, validAccount, 0)
	endpoints := []string{}
	s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		endpoints = append(endpoints, r.URL.Path)
		switch r.URL.Path {
		case "/login/device/code":
			io.WriteString(w, deviceJSON)
		case "/login/oauth/access_token":
			io.WriteString(w, `{"access_token":"`+fakeToken+`","token_type":"bearer"}`)
		case "/orgs/owner":
			json.NewEncoder(w).Encode(map[string]any{"login": "owner", "type": "Organization", "plan": map[string]string{"name": plan}})
		case "/organizations/owner/settings/billing/usage":
			if billingStatus != 0 {
				w.WriteHeader(billingStatus)
				io.WriteString(w, fakeToken)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(750, nil)}})
		case "/repos/owner/private":
			io.WriteString(w, `{"private":true}`)
		default:
			t.Errorf("unexpected organization endpoint: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	return s, gh, &endpoints
}

func TestOrganizationSetupAndStatus(t *testing.T) {
	s, gh, endpoints := organizationFixture(t, "team", 0)
	for i := 0; i < 2; i++ {
		if err := s.run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	store := s.credentials.(*fakeCredentialStore)
	if store.saves != 1 || store.deletes != 0 || !reflect.DeepEqual(store.savedAccounts, []string{"owner"}) {
		t.Fatalf("org cache not reused: %+v", store)
	}
	devices := 0
	for _, path := range *endpoints {
		if path == "/login/device/code" {
			devices++
		}
	}
	if devices != 1 {
		t.Fatalf("device requests=%d", devices)
	}
	writes := 0
	for _, call := range gh.calls {
		if call.args[0] == "secret" && call.args[1] == "set" {
			writes++
			if call.stdin != fakeToken {
				t.Fatal("wrong secret stdin")
			}
		}
		if strings.Contains(strings.Join(call.args, " "), fakeToken) {
			t.Fatal("token in argv")
		}
	}
	if writes != 2 {
		t.Fatalf("secret writes=%d", writes)
	}
	if !strings.Contains(s.output.(*bytes.Buffer).String(), "Owner type: organization") {
		t.Fatal("setup owner type missing")
	}
	before := repositorySnapshot(t, s.root)
	s.output.(*bytes.Buffer).Reset()
	if err := s.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := s.output.(*bytes.Buffer).String()
	if !strings.Contains(output, "Account: owner\n  Owner type: organization\n  Used:    750.00 / 3000 min  ( 25.00% )\n  Plan:    Team") || strings.Contains(output, fakeToken) {
		t.Fatalf("wrong status: %s", output)
	}
	if !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) || store.saves != 1 || store.deletes != 0 {
		t.Fatal("status mutated setup or cache")
	}
}

func TestOrganizationAuthorizationAndPermissionFailures(t *testing.T) {
	for _, status := range []int{403, 404} {
		for _, command := range []string{"setup", "status", "auth login", "auth status"} {
			t.Run(command+http.StatusText(status), func(t *testing.T) {
				s, gh, endpoints := organizationFixture(t, "team", status)
				store := s.credentials.(*fakeCredentialStore)
				store.token = fakeToken
				s.browser = func(context.Context, string) error { t.Fatal("permission failure started device flow"); return nil }
				var err error
				switch command {
				case "setup":
					err = s.run(context.Background())
				case "status":
					err = s.status(context.Background())
				case "auth login":
					err = s.authLogin(context.Background())
				case "auth status":
					_, err = s.authStatus(context.Background())
				}
				var access *quota.OrganizationAccessError
				if !errors.As(err, &access) || !strings.Contains(err.Error(), "install/approve") || !strings.Contains(err.Error(), "billing privileges") || strings.Contains(err.Error(), fakeToken) {
					t.Fatalf("missing actionable access error: %v", err)
				}
				if store.saves != 0 || store.deletes != 0 || store.token != fakeToken {
					t.Fatal("org permission failure discarded user authorization")
				}
				for _, path := range *endpoints {
					if strings.HasPrefix(path, "/login/") {
						t.Fatal("permission failure authorized again")
					}
				}
				for _, call := range gh.calls {
					if call.args[0] == "secret" && call.args[1] == "set" {
						t.Fatal("failed org setup wrote secret")
					}
				}
			})
		}
	}
}

func TestOrganizationQuotaUnavailableAndOverride(t *testing.T) {
	for _, command := range []string{"setup", "status"} {
		s, gh, _ := organizationFixture(t, "enterprise", 0)
		store := s.credentials.(*fakeCredentialStore)
		store.token = fakeToken
		var err error
		if command == "setup" {
			err = s.run(context.Background())
		} else {
			err = s.status(context.Background())
		}
		if !errors.Is(err, quota.ErrQuotaUnavailable) || !strings.Contains(s.output.(*bytes.Buffer).String(), "750.00") || !strings.Contains(s.output.(*bytes.Buffer).String(), "unavailable") {
			t.Fatalf("missing usage/quota distinction: %v %s", err, s.output)
		}
		for _, call := range gh.calls {
			if call.args[0] == "secret" && call.args[1] == "set" {
				t.Fatal("unknown quota wrote secret")
			}
		}
		s.quotaOverride = "4000"
		if command == "setup" {
			err = s.run(context.Background())
		} else {
			err = s.status(context.Background())
		}
		if err != nil || !strings.Contains(s.output.(*bytes.Buffer).String(), "750.00 / 4000") {
			t.Fatalf("override failed: %v %s", err, s.output)
		}
		if command == "setup" {
			override, err := workflowQuotaOverride(s.root)
			if err != nil || override != "4000" {
				t.Fatalf("override not persisted in helper: %s %v", override, err)
			}
			s.quotaOverride = ""
			if err := s.status(context.Background()); err != nil {
				t.Fatal("status did not reuse workflow override", err)
			}
			if _, helper, err := prepareWorkflowUninstall(s.root); err != nil || !helper {
				t.Fatalf("override helper not uninstallable: %v", err)
			}
		}
	}
}

func TestOrganizationAuthLoginAndStatusWithoutKnownQuota(t *testing.T) {
	s, _, _ := organizationFixture(t, "unknown", 0)
	if err := s.authLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := s.credentials.(*fakeCredentialStore)
	if store.saves != 1 || store.token != fakeToken {
		t.Fatal("org authorization not stored securely")
	}
	if authenticated, err := s.authStatus(context.Background()); err != nil || !authenticated {
		t.Fatal(authenticated, err)
	}
	if err := s.authLogout(context.Background()); err != nil || store.token != "" {
		t.Fatal(err)
	}
}

func TestOrganizationOwnerLookupFailuresBeforeAuthorization(t *testing.T) {
	for _, gh := range []*fakeGH{{ownerType: "Bot"}, {failAt: 4}} {
		s, _, requests := setupFixture(t, gh, validAccount, 0)
		before := repositorySnapshot(t, s.root)
		if err := s.run(context.Background()); err == nil {
			t.Fatal("unknown ownership accepted")
		}
		if *requests != 0 || !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
			t.Fatal("failed owner lookup changed setup")
		}
	}
}

func TestWorkflowOverrideCompatibilityAndIdempotence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(workflowPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(previousReusableWorkflow), 0640); err != nil {
		t.Fatal(err)
	}
	if created, err := ensureReusableWorkflow(root); err != nil || created {
		t.Fatal(created, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != previousReusableWorkflow {
		t.Fatal("existing personal helper was rewritten")
	}
	if _, err := ensureReusableWorkflowWithQuota(root, "4000"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if _, err := ensureReusableWorkflowWithQuota(root, "4000"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) || after.Mode().Perm() != 0640 {
		t.Fatal("override helper not idempotent")
	}
	data, _ = os.ReadFile(path)
	custom := strings.Replace(string(data), "default: 50", "default: 75", 1)
	if err := os.WriteFile(path, []byte(custom), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureReusableWorkflowWithQuota(root, "5000"); err == nil {
		t.Fatal("custom helper overwritten")
	}
	afterData, _ := os.ReadFile(path)
	if string(afterData) != custom {
		t.Fatal("custom helper changed")
	}
}

func TestOrganizationUninstallIsIdempotent(t *testing.T) {
	s, gh, _ := organizationFixture(t, "team", 0)
	s.credentials.(*fakeCredentialStore).token = fakeToken
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	gh.secretPresent = true
	if err := s.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	gh.secretPresent = false
	before := repositorySnapshot(t, s.root)
	if err := s.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) || s.credentials.(*fakeCredentialStore).token != fakeToken {
		t.Fatal("org uninstall changed retained credentials or was not idempotent")
	}
}

func TestWorkflowOverrideAllPositiveNumberFormats(t *testing.T) {
	for _, value := range []string{"1e-5", "1e9", "4000.5"} {
		root := t.TempDir()
		if _, err := ensureReusableWorkflowWithQuota(root, value); err != nil {
			t.Fatal(err)
		}
		stored, err := workflowQuotaOverride(root)
		if err != nil || stored == "" {
			t.Fatalf("lost positive allowance %s: %s %v", value, stored, err)
		}
		if _, found, err := prepareWorkflowUninstall(root); err != nil || !found {
			t.Fatalf("generated override cannot uninstall: %v", err)
		}
	}
}
