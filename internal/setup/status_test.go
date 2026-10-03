package setup

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func repositorySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		files[path] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestStatusPublicSkipsDeviceFlowAndBilling(t *testing.T) {
	gh := &fakeGH{public: true, ownerType: "Organization"}
	s, output, requests := setupFixture(t, gh, validAccount, 0)
	s.browser = func(context.Context, string) error { t.Fatal("public status opened browser"); return nil }
	s.clipboard = func(context.Context, string) error { t.Fatal("status wrote clipboard"); return nil }
	before := repositorySnapshot(t, s.root)
	if err := s.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *requests != 0 {
		t.Fatalf("public status made %d API requests", *requests)
	}
	if !reflect.DeepEqual(gh.calls, []ghCall{{args: []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}}}) {
		t.Fatalf("unexpected public gh calls: %v", gh.calls)
	}
	if output.String() != "Repository: owner/repo\nVisibility: public\nActions quota: unmetered\n" {
		t.Fatalf("wrong public output: %s", output)
	}
	if !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
		t.Fatal("public status modified repository files")
	}
}

func TestStatusPrivateUsesDeviceFlowAndStaysReadOnly(t *testing.T) {
	for _, test := range []struct {
		name      string
		used      float64
		usedText  string
		remaining string
		percent   string
	}{
		{"partial", 742.33, "742.33", "1257.67", "37.12"},
		{"empty", 0, "0.00", "2000.00", "0.00"},
		{"over quota", 2500, "2500.00", "0.00", "125.00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gh := &fakeGH{}
			s, output, _ := setupFixture(t, gh, validAccount, 0)
			var endpoints []string
			s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				endpoints = append(endpoints, r.URL.Path)
				switch r.URL.Path {
				case "/login/device/code":
					io.WriteString(w, deviceJSON)
				case "/login/oauth/access_token":
					io.WriteString(w, `{"access_token":"`+fakeToken+`","token_type":"bearer"}`)
				case "/user":
					io.WriteString(w, `{"login":"owner","type":"User","plan":{"name":"free"}}`)
				case "/users/owner/settings/billing/usage":
					json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{
						billingItem(test.used*linuxMinutePriceUSD, nil),
						billingItem(18, map[string]any{"repositoryName": "owner/public"}),
					}})
				case "/repos/owner/private":
					io.WriteString(w, `{"private":true}`)
				case "/repos/owner/public":
					io.WriteString(w, `{"private":false}`)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
			})
			browserCalls := 0
			s.browser = func(_ context.Context, uri string) error {
				browserCalls++
				if uri != "https://github.com/login/device" {
					t.Error("wrong device URL")
				}
				return nil
			}
			clipboardCalls := 0
			s.clipboard = func(_ context.Context, code string) error {
				clipboardCalls++
				if code != "ABCD-EFGH" {
					t.Errorf("wrong clipboard code: %s", code)
				}
				return nil
			}
			if err := os.MkdirAll(filepath.Join(s.root, ".github", "workflows"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.root, ".github", "workflows", "ci.yml"), []byte("jobs:\n  build:\n    runs-on: ubuntu-latest\n"), 0644); err != nil {
				t.Fatal(err)
			}
			before := repositorySnapshot(t, s.root)
			if err := s.status(context.Background()); err != nil {
				t.Fatal(err)
			}
			if browserCalls != 1 || clipboardCalls != 1 || !reflect.DeepEqual(endpoints, []string{"/login/device/code", "/login/oauth/access_token", "/user", "/users/owner/settings/billing/usage", "/repos/owner/private", "/repos/owner/public"}) {
				t.Fatalf("wrong authentication/billing requests: %v", endpoints)
			}
			store := s.credentials.(*fakeCredentialStore)
			if store.saves != 1 || store.token != fakeToken {
				t.Fatal("status did not persist fresh authorization")
			}
			if !reflect.DeepEqual(gh.calls, []ghCall{
				{args: []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}},
				{args: []string{"api", "users/owner", "--hostname", "github.com", "--jq", ".type"}},
			}) {
				t.Fatalf("status called gh secret set or another unexpected command: %v", gh.calls)
			}
			for _, want := range []string{"Repository: owner/repo", "Visibility: private", "Code copied to clipboard: ABCD-EFGH", "Plan: free", "Actions quota:", "Used:       " + test.usedText + " / 2000 min", "Remaining: " + test.remaining + " min", "Usage:      " + test.percent + "%"} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("missing status output %q: %s", want, output)
				}
			}
			if test.name == "partial" && !strings.Contains(output.String(), "Actions quota:\n  Used:       742.33 / 2000 min\n  Remaining: 1257.67 min\n  Usage:      37.12%\n") {
				t.Fatalf("wrong quota formatting: %s", output)
			}
			if strings.Contains(output.String(), fakeToken) || !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
				t.Fatal("private status exposed credentials or modified repository files")
			}
		})
	}
}

func TestStatusFailuresRemainReadOnly(t *testing.T) {
	for _, test := range []struct {
		gh      *fakeGH
		account string
		billing int
	}{
		{&fakeGH{failAt: 1}, validAccount, 0},
		{&fakeGH{failAt: 2}, validAccount, 0},
		{&fakeGH{ownerType: "Organization"}, validAccount, 0},
		{&fakeGH{}, `{"login":"other","type":"User","plan":{"name":"free"}}`, 0},
		{&fakeGH{}, validAccount, 403},
	} {
		s, output, _ := setupFixture(t, test.gh, test.account, test.billing)
		before := repositorySnapshot(t, s.root)
		err := s.status(context.Background())
		if err == nil || strings.Contains(err.Error(), fakeToken) || strings.Contains(output.String(), fakeToken) {
			t.Fatalf("unsafe or missing status error: %v", err)
		}
		for _, call := range test.gh.calls {
			if call.args[0] == "secret" {
				t.Fatal("failed status wrote a secret")
			}
		}
		if !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
			t.Fatal("failed status modified repository files")
		}
	}
}

func TestStatusPrivateReusesStoredCredential(t *testing.T) {
	gh := &fakeGH{}
	s, output, requests := setupFixture(t, gh, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.token = fakeToken
	s.browser = func(context.Context, string) error { t.Fatal("cached status opened browser"); return nil }
	s.clipboard = func(context.Context, string) error { t.Fatal("cached status wrote clipboard"); return nil }
	if err := s.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *requests != 3 {
		t.Fatalf("cached status made %d API requests; want 3", *requests)
	}
	if store.loads != 1 || store.saves != 0 || store.deletes != 0 {
		t.Fatalf("unexpected credential operations: %+v", store)
	}
	if strings.Contains(output.String(), "/login/device") || strings.Contains(output.String(), "Code:") || strings.Contains(output.String(), "Code copied") {
		t.Fatal("cached status displayed device authorization")
	}
	if !strings.Contains(output.String(), "Plan: pro") {
		t.Fatal("cached status did not complete")
	}
}
