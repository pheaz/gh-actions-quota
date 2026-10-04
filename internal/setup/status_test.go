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

func TestStatusPublicUsesCurrentPersonalAccount(t *testing.T) {
	for _, repository := range []string{"some-org/example", "other-user/example"} {
		t.Run(repository, func(t *testing.T) {
			gh := &fakeGH{public: true, ownerType: "Organization", repository: repository}
			s, output, _ := setupFixture(t, gh, validAccount, 0)
			store := s.credentials.(*fakeCredentialStore)
			store.token = fakeToken

			var endpoints []string
			s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				endpoints = append(endpoints, r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer "+fakeToken {
					t.Error("wrong app authorization")
				}
				switch r.URL.Path {
				case "/user":
					io.WriteString(w, `{"login":"SIGNED-IN","type":"User","plan":{"name":"free"}}`)
				case "/users/signed-in/settings/billing/usage":
					json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{
						billingItem(742.33, map[string]any{"repositoryName": "signed-in/private"}),
						billingItem(3000, map[string]any{"repositoryName": repository}),
					}})
				case "/repos/signed-in/private":
					io.WriteString(w, `{"private":true}`)
				case "/repos/" + repository:
					io.WriteString(w, `{"private":false}`)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
			})
			s.browser = func(context.Context, string) error { t.Fatal("status opened browser"); return nil }
			s.clipboard = func(context.Context, string) error { t.Fatal("status used clipboard"); return nil }

			before := repositorySnapshot(t, s.root)
			if err := s.status(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantEndpoints := []string{"/user", "/users/signed-in/settings/billing/usage", "/repos/signed-in/private", "/repos/" + repository}
			if !reflect.DeepEqual(endpoints, wantEndpoints) {
				t.Fatalf("wrong public billing requests: %v", endpoints)
			}
			if store.loads != 1 || store.saves != 0 || store.deletes != 0 || !reflect.DeepEqual(store.loadedAccounts, []string{"signed-in"}) {
				t.Fatalf("wrong public credential operations: %+v", store)
			}
			if !reflect.DeepEqual(gh.calls, []ghCall{
				{args: []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}},
				{args: []string{"api", "user", "--hostname", "github.com"}},
				{args: []string{"secret", "list", "--repo", repository, "--json", "name"}},
			}) {
				t.Fatalf("unexpected public gh calls: %v", gh.calls)
			}
			want := "Repository: " + repository + "\nVisibility: Public (unmetered)\n\nActions quota:\n  Account: signed-in\n  Used:    742.33 / 2000 min  ( 37.12% )\n  Plan:    Free\n"
			if output.String() != want {
				t.Fatalf("wrong public output: %s", output)
			}
			if strings.Contains(output.String(), fakeToken) || !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
				t.Fatal("public status exposed credentials or modified repository files")
			}
		})
	}
}

func TestStatusPublicRequiresStoredAuthenticationWithoutDeviceFlow(t *testing.T) {
	gh := &fakeGH{public: true, repository: "some-org/example"}
	s, output, requests := setupFixture(t, gh, validAccount, 0)
	s.browser = func(context.Context, string) error { t.Fatal("status opened browser"); return nil }
	s.clipboard = func(context.Context, string) error { t.Fatal("status used clipboard"); return nil }

	err := s.status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not authenticated with gh-actions-quota for signed-in") ||
		!strings.Contains(err.Error(), "gh actions-quota auth login") {
		t.Fatalf("missing authentication guidance: %v", err)
	}
	store := s.credentials.(*fakeCredentialStore)
	if *requests != 0 || store.loads != 1 || store.saves != 0 || store.deletes != 0 {
		t.Fatalf("status started authorization or changed credentials: requests=%d store=%+v", *requests, store)
	}
	want := "Repository: some-org/example\nVisibility: Public (unmetered)\n"
	if output.String() != want {
		t.Fatalf("wrong unauthenticated public output: %s", output)
	}
}

func TestStatusPrivateRequiresStoredAuthenticationWithoutDeviceFlow(t *testing.T) {
	gh := &fakeGH{}
	s, output, requests := setupFixture(t, gh, validAccount, 0)
	s.browser = func(context.Context, string) error { t.Fatal("status opened browser"); return nil }
	s.clipboard = func(context.Context, string) error { t.Fatal("status used clipboard"); return nil }

	before := repositorySnapshot(t, s.root)
	err := s.status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not authenticated with gh-actions-quota for owner") {
		t.Fatalf("missing private authentication requirement: %v", err)
	}
	store := s.credentials.(*fakeCredentialStore)
	if *requests != 0 || store.loads != 1 || store.saves != 0 || store.deletes != 0 {
		t.Fatalf("private status started authorization or changed credentials: requests=%d store=%+v", *requests, store)
	}
	if !reflect.DeepEqual(gh.calls, []ghCall{
		{args: []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}},
		{args: []string{"api", "users/owner", "--hostname", "github.com", "--jq", ".type"}},
		{args: []string{"secret", "list", "--repo", "owner/repo", "--json", "name"}},
	}) {
		t.Fatalf("unexpected private gh calls: %v", gh.calls)
	}
	want := "Repository: owner/repo\nVisibility: Private (metered)\n\nSetup:\n  Workflow: missing\n  Secret:   missing\n"
	if output.String() != want {
		t.Fatalf("wrong unauthenticated private output: %s", output)
	}
	if strings.Contains(output.String(), fakeToken) || !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
		t.Fatal("private status exposed credentials or modified repository files")
	}
}

func TestStatusFailuresRemainReadOnly(t *testing.T) {
	for _, test := range []struct {
		gh      *fakeGH
		account string
		billing int
	}{
		{&fakeGH{failAt: 2}, validAccount, 0},
		{&fakeGH{failAt: 3}, validAccount, 0},
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
			if len(call.args) > 1 && call.args[0] == "secret" && call.args[1] == "set" {
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
					json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(742.33, nil)}})
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

func TestStatusWithoutRepositoryUsesCurrentPersonalAccount(t *testing.T) {
	gh := &fakeGH{failAt: 1}
	s, output, _ := setupFixture(t, gh, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.token = fakeToken
	s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			io.WriteString(w, `{"login":"signed-in","type":"User","plan":{"name":"free"}}`)
		case "/users/signed-in/settings/billing/usage":
			json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{
				billingItem(742.33, map[string]any{"repositoryName": "signed-in/private"}),
			}})
		case "/repos/signed-in/private":
			io.WriteString(w, `{"private":true}`)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	})
	if err := s.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "Repository: not found\n\nActions quota:\n  Account: signed-in\n  Used:    742.33 / 2000 min  ( 37.12% )\n  Plan:    Free\n"
	if output.String() != want {
		t.Fatalf("wrong status without repository: %s", output)
	}
	if strings.Contains(output.String(), "Setup:") || strings.Contains(output.String(), "Visibility:") {
		t.Fatal("status without repository printed repository-only metadata")
	}
	if !reflect.DeepEqual(gh.calls, []ghCall{
		{args: []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}},
		{args: []string{"api", "user", "--hostname", "github.com"}},
	}) {
		t.Fatalf("status without repository made unexpected gh calls: %v", gh.calls)
	}
}

func TestStatusPrivateSetupPresence(t *testing.T) {
	gh := &fakeGH{secretPresent: true}
	s, output, _ := setupFixture(t, gh, validAccount, 0)
	s.credentials.(*fakeCredentialStore).token = fakeToken
	path := filepath.Join(s.root, filepath.FromSlash(workflowPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Visibility: Private (metered)",
		"Setup:\n  Workflow: present\n  Secret:   present\n",
		"Actions quota:\n  Account: owner\n  Used:    2000.00 / 3000 min  ( 66.67% )\n  Plan:    Pro\n",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in private setup status: %s", want, output)
		}
	}
}

func TestStatusPublicShowsOnlyPresentSetupArtifacts(t *testing.T) {
	for _, test := range []struct {
		name            string
		workflowPresent bool
		secretPresent   bool
		wantSetup       string
	}{
		{"none", false, false, ""},
		{"workflow", true, false, "Setup:\n  Workflow: present\n"},
		{"secret", false, true, "Setup:\n  Secret:   present\n"},
		{"both", true, true, "Setup:\n  Workflow: present\n  Secret:   present\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gh := &fakeGH{public: true, repository: "some-org/example", secretPresent: test.secretPresent}
			s, output, _ := setupFixture(t, gh, validAccount, 0)
			s.credentials.(*fakeCredentialStore).token = fakeToken
			if test.workflowPresent {
				path := filepath.Join(s.root, filepath.FromSlash(workflowPath))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("# installed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					io.WriteString(w, `{"login":"signed-in","type":"User","plan":{"name":"free"}}`)
				case "/users/signed-in/settings/billing/usage":
					json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{
						billingItem(742.33, map[string]any{"repositoryName": "signed-in/private"}),
					}})
				case "/repos/signed-in/private":
					io.WriteString(w, `{"private":true}`)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
			})
			if err := s.status(context.Background()); err != nil {
				t.Fatal(err)
			}
			if test.wantSetup == "" {
				if strings.Contains(output.String(), "Setup:") || strings.Contains(output.String(), "missing") {
					t.Fatalf("public status showed absent setup artifacts: %s", output)
				}
			} else if !strings.Contains(output.String(), test.wantSetup) {
				t.Fatalf("wrong public setup status: %s", output)
			}
			if strings.Contains(output.String(), "Workflow: missing") || strings.Contains(output.String(), "Secret:   missing") {
				t.Fatalf("public status printed missing setup artifacts: %s", output)
			}
		})
	}
}
