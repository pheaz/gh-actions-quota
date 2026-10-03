package setup

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestStatusPublicUsesCurrentPersonalAccount(t *testing.T) {
	for _, repository := range []string{"some-org/example", "other-user/example"} {
		for _, cached := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/cached=%t", repository, cached), func(t *testing.T) {
				gh := &fakeGH{public: true, ownerType: "Organization", repository: repository}
				s, output, _ := setupFixture(t, gh, validAccount, 0)
				store := s.credentials.(*fakeCredentialStore)
				if cached {
					store.token = fakeToken
				}
				var endpoints []string
				s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
					endpoints = append(endpoints, r.URL.Path)
					if !strings.HasPrefix(r.URL.Path, "/login/") && r.Header.Get("Authorization") != "Bearer "+fakeToken {
						t.Error("wrong app authorization")
					}
					switch r.URL.Path {
					case "/login/device/code":
						io.WriteString(w, deviceJSON)
					case "/login/oauth/access_token":
						io.WriteString(w, `{"access_token":"`+fakeToken+`","token_type":"bearer"}`)
					case "/user":
						io.WriteString(w, `{"login":"SIGNED-IN","type":"User","plan":{"name":"free"}}`)
					case "/users/signed-in/settings/billing/usage":
						json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{
							billingItem(742.33*linuxMinutePriceUSD, map[string]any{"repositoryName": "signed-in/private"}),
							billingItem(18, map[string]any{"repositoryName": repository}),
						}})
					case "/repos/signed-in/private":
						io.WriteString(w, `{"private":true}`)
					case "/repos/" + repository:
						io.WriteString(w, `{"private":false}`)
					default:
						t.Errorf("unexpected endpoint: %s", r.URL.Path)
					}
				})
				browserCalls, clipboardCalls := 0, 0
				s.browser = func(_ context.Context, uri string) error {
					browserCalls++
					if uri != "https://github.com/login/device" {
						t.Error("wrong device URL")
					}
					return nil
				}
				s.clipboard = func(_ context.Context, code string) error {
					clipboardCalls++
					if code != "ABCD-EFGH" {
						t.Error("wrong clipboard code")
					}
					return nil
				}
				before := repositorySnapshot(t, s.root)
				if err := s.status(context.Background()); err != nil {
					t.Fatal(err)
				}
				wantEndpoints := []string{"/user", "/users/signed-in/settings/billing/usage", "/repos/signed-in/private", "/repos/" + repository}
				fresh := 0
				if !cached {
					fresh = 1
					wantEndpoints = append([]string{"/login/device/code", "/login/oauth/access_token"}, wantEndpoints...)
					if !strings.Contains(output.String(), "Code copied to clipboard: ABCD-EFGH") || !reflect.DeepEqual(store.savedAccounts, []string{"signed-in"}) {
						t.Fatal("fresh public authorization was not copied and saved for the current account")
					}
				}
				if browserCalls != fresh || clipboardCalls != fresh || !reflect.DeepEqual(endpoints, wantEndpoints) {
					t.Fatalf("wrong public authentication/billing requests: %v", endpoints)
				}
				if store.loads != 1 || store.saves != fresh || store.deletes != 0 || store.token != fakeToken || !reflect.DeepEqual(store.loadedAccounts, []string{"signed-in"}) {
					t.Fatalf("wrong public credential operations: %+v", store)
				}
				if !reflect.DeepEqual(gh.calls, []ghCall{
					{args: []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}},
					{args: []string{"api", "user", "--hostname", "github.com"}},
					{args: []string{"secret", "list", "--repo", repository, "--json", "name"}},
				}) {
					t.Fatalf("unexpected public gh calls: %v", gh.calls)
				}
				wantHeader := "Repository: " + repository + "\nVisibility: Public (unmetered)\n"
				wantQuota := "\nActions quota:\n  Account: signed-in\n  Used:    742.33 / 2000 min  ( 37.12% )\n  Plan:    Free\n"
				if !strings.HasPrefix(output.String(), wantHeader) || !strings.HasSuffix(output.String(), wantQuota) || (cached && output.String() != wantHeader+wantQuota) {
					t.Fatalf("wrong public output: %s", output)
				}
				if strings.Contains(output.String(), fakeToken) || !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
					t.Fatal("public status exposed credentials or modified repository files")
				}
			})
		}
	}
}

func TestStatusPrivateUsesDeviceFlowAndStaysReadOnly(t *testing.T) {
	for _, test := range []struct {
		name     string
		used     float64
		usedText string
		percent  string
	}{
		{"partial", 742.33, "742.33", "37.12"},
		{"empty", 0, "0.00", "0.00"},
		{"over quota", 2500, "2500.00", "125.00"},
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
			if store.saves != 1 || store.token != fakeToken || !reflect.DeepEqual(store.loadedAccounts, []string{"owner"}) || !reflect.DeepEqual(store.savedAccounts, []string{"owner"}) {
				t.Fatal("status did not persist fresh authorization")
			}
			if !reflect.DeepEqual(gh.calls, []ghCall{
				{args: []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}},
				{args: []string{"api", "users/owner", "--hostname", "github.com", "--jq", ".type"}},
				{args: []string{"secret", "list", "--repo", "owner/repo", "--json", "name"}},
			}) {
				t.Fatalf("status called gh secret set or another unexpected command: %v", gh.calls)
			}
			for _, want := range []string{"Repository: owner/repo\nVisibility: Private (metered)\n", "Code copied to clipboard: ABCD-EFGH"} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("missing status output %q: %s", want, output)
				}
			}
			wantQuota := "\nActions quota:\n  Account: owner\n  Used:    " + test.usedText + " / 2000 min  ( " + test.percent + "% )\n  Plan:    Free\n"
			if !strings.HasSuffix(output.String(), wantQuota) || strings.Contains(output.String(), "Remaining") || strings.Contains(output.String(), "Usage:") {
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
	if store.loads != 1 || store.saves != 0 || store.deletes != 0 || !reflect.DeepEqual(store.loadedAccounts, []string{"owner"}) {
		t.Fatalf("unexpected credential operations: %+v", store)
	}
	if strings.Contains(output.String(), "/login/device") || strings.Contains(output.String(), "Code:") || strings.Contains(output.String(), "Code copied") {
		t.Fatal("cached status displayed device authorization")
	}
	want := "Repository: owner/repo\nVisibility: Private (metered)\n\nSetup:\n  Workflow: missing\n  Secret:   missing\n\nActions quota:\n  Account: owner\n  Used:    2000.00 / 3000 min  ( 66.67% )\n  Plan:    Pro\n"
	if output.String() != want {
		t.Fatalf("wrong cached private output: %s", output)
	}
}

func TestStatusPublicRejectsInvalidCurrentAccountBeforeAuthorization(t *testing.T) {
	for _, test := range []struct {
		name    string
		account string
		failAt  int
	}{
		{"gh failure", "", 2},
		{"invalid JSON", "not JSON", 0},
		{"null", "null", 0},
		{"missing login", `{"type":"User"}`, 0},
		{"missing type", `{"login":"signed-in"}`, 0},
		{"organization", `{"login":"some-org","type":"Organization"}`, 0},
		{"bot", `{"login":"bot","type":"Bot"}`, 0},
		{"invalid login", `{"login":"other/account","type":"User"}`, 0},
		{"unsafe response", `{"login":"` + fakeToken + `","type":"User"}`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			gh := &fakeGH{public: true, currentUser: test.account, failAt: test.failAt}
			s, output, requests := setupFixture(t, gh, validAccount, 0)
			before := repositorySnapshot(t, s.root)
			err := s.status(context.Background())
			if err == nil || strings.Contains(err.Error(), fakeToken) || strings.Contains(output.String(), fakeToken) {
				t.Fatalf("unsafe or missing current-account error: %v", err)
			}
			store := s.credentials.(*fakeCredentialStore)
			if *requests != 0 || store.loads != 0 || store.saves != 0 || store.deletes != 0 {
				t.Fatal("invalid current account reached app authorization or credential storage")
			}
			if !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
				t.Fatal("failed public status modified repository files")
			}
		})
	}
}

func TestRepositoryVisibilityRequiredBeforeSetupOrStatus(t *testing.T) {
	for _, visibility := range []string{"", `,"isPrivate":null`, `,"isPrivate":"false"`} {
		for _, command := range []string{"setup", "status"} {
			t.Run(command+visibility, func(t *testing.T) {
				gh := &fakeGH{repoResponse: `{"nameWithOwner":"owner/repo","url":"https://github.com/owner/repo"` + visibility + `}`}
				s, _, requests := setupFixture(t, gh, validAccount, 0)
				before := repositorySnapshot(t, s.root)
				var err error
				if command == "setup" {
					err = s.run(context.Background())
				} else {
					err = s.status(context.Background())
				}
				if err == nil {
					t.Fatal("missing or invalid visibility was accepted")
				}
				store := s.credentials.(*fakeCredentialStore)
				if *requests != 0 || store.loads != 0 || store.saves != 0 || store.deletes != 0 || !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
					t.Fatal("invalid repository visibility caused side effects")
				}
			})
		}
	}
}

func TestStatusPlanFormatting(t *testing.T) {
	for _, test := range []struct {
		plan    string
		display string
		quota   string
		percent string
	}{
		{"free", "Free", "2000", "37.12"},
		{"pro", "Pro", "3000", "24.74"},
		{"team", "Team", "3000", "24.74"},
		{"enterprise", "Enterprise", "50000", "1.48"},
		{"enterprise cloud", "Enterprise Cloud", "50000", "1.48"},
	} {
		t.Run(test.plan, func(t *testing.T) {
			s, output, _ := setupFixture(t, &fakeGH{}, validAccount, 0)
			s.credentials.(*fakeCredentialStore).token = fakeToken
			s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					json.NewEncoder(w).Encode(map[string]any{"login": "owner", "type": "User", "plan": map[string]string{"name": test.plan}})
				case "/users/owner/settings/billing/usage":
					json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(742.33*linuxMinutePriceUSD, nil)}})
				case "/repos/owner/private":
					io.WriteString(w, `{"private":true}`)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
			})
			if err := s.status(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "Repository: owner/repo\nVisibility: Private (metered)\n\nSetup:\n  Workflow: missing\n  Secret:   missing\n\nActions quota:\n  Account: owner\n  Used:    742.33 / " + test.quota + " min  ( " + test.percent + "% )\n  Plan:    " + test.display + "\n"
			if output.String() != want {
				t.Fatalf("wrong status formatting: %s", output)
			}
		})
	}
}
