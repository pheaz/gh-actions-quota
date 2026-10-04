package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type ghCall struct {
	args  []string
	stdin string
}
type fakeGH struct {
	calls         []ghCall
	ownerType     string
	repository    string
	repoResponse  string
	currentUser   string
	secretPresent bool
	public        bool
	failAt        int
}

type fakeCredentialStore struct {
	token           string
	loadErr         error
	saveErr         error
	deleteErr       error
	loads           int
	saves           int
	deletes         int
	loadedAccounts  []string
	savedAccounts   []string
	deletedAccounts []string
}

func (s *fakeCredentialStore) Load(_ context.Context, account string) (string, error) {
	s.loads++
	s.loadedAccounts = append(s.loadedAccounts, account)
	if s.loadErr != nil {
		return "", s.loadErr
	}
	if s.token == "" {
		return "", errCredentialNotFound
	}
	return s.token, nil
}

func (s *fakeCredentialStore) Save(_ context.Context, account string, token string) error {
	s.saves++
	s.savedAccounts = append(s.savedAccounts, account)
	if s.saveErr != nil {
		return s.saveErr
	}
	s.token = token
	return nil
}

func (s *fakeCredentialStore) Delete(_ context.Context, account string) error {
	s.deletes++
	s.deletedAccounts = append(s.deletedAccounts, account)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.token = ""
	return nil
}

func (g *fakeGH) Run(_ context.Context, args []string, input io.Reader) ([]byte, error) {
	call := ghCall{args: append([]string(nil), args...)}
	if input != nil {
		data, _ := io.ReadAll(input)
		call.stdin = string(data)
	}
	g.calls = append(g.calls, call)
	if g.failAt == len(g.calls) {
		return nil, errors.New(fakeToken + " simulated command dump")
	}
	switch args[0] {
	case "repo":
		if g.repoResponse != "" {
			return []byte(g.repoResponse), nil
		}
		repository := g.repository
		if repository == "" {
			repository = "owner/repo"
		}
		privacy := "true"
		if g.public {
			privacy = "false"
		}
		return []byte(`{"nameWithOwner":"` + repository + `","url":"https://github.com/` + repository + `","isPrivate":` + privacy + `}`), nil
	case "api":
		if args[1] == "user" {
			if g.currentUser != "" {
				return []byte(g.currentUser), nil
			}
			return []byte(`{"login":"signed-in","type":"User"}`), nil
		}
		if g.ownerType != "" {
			return []byte(g.ownerType), nil
		}
		return []byte("User\n"), nil
	case "secret":
		if len(args) > 1 && args[1] == "list" {
			if g.secretPresent {
				return []byte(`[{"name":"ACTIONS_QUOTA_TOKEN"}]`), nil
			}
			return []byte(`[]`), nil
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func setupFixture(t *testing.T, gh *fakeGH, account string, billingStatus int) (*setup, *bytes.Buffer, *int) {
	t.Helper()
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/login/device/code":
			io.WriteString(w, deviceJSON)
		case "/login/oauth/access_token":
			io.WriteString(w, `{"access_token":"`+fakeToken+`","token_type":"bearer"}`)
		case "/user":
			if r.Header.Get("Authorization") != "Bearer "+fakeToken {
				t.Error("wrong app authorization")
			}
			io.WriteString(w, account)
		case "/users/owner/settings/billing/usage":
			if r.Header.Get("X-GitHub-Api-Version") != apiVersion || r.URL.Query().Get("product") != "Actions" || r.URL.Query().Get("month") == "" || r.URL.Query().Get("year") == "" {
				t.Error("wrong billing request")
			}
			if billingStatus != 0 {
				w.WriteHeader(billingStatus)
				io.WriteString(w, fakeToken)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(120, map[string]any{"repositoryName": "owner/repo", "sku": "Actions Windows"}), billingItem(174.193548387, map[string]any{"repositoryName": "owner/repo", "sku": "Actions macOS 3-core"})}})
		case "/repos/owner/repo":
			io.WriteString(w, `{"private":true}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	var output bytes.Buffer
	s := &setup{gh: gh, client: c, input: strings.NewReader(""), output: &output, browser: func(context.Context, string) error { return errors.New("headless") }, clipboard: func(context.Context, string) error { return nil }, credentials: &fakeCredentialStore{}, root: t.TempDir()}
	return s, &output, &requests
}

const validAccount = `{"login":"OWNER","type":"User","plan":{"name":"pro"}}`

func TestSetupSecretWriteUsesOnlyStdin(t *testing.T) {
	gh := &fakeGH{}
	s, output, _ := setupFixture(t, gh, validAccount, 0)
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(gh.calls) != 5 {
		t.Fatalf("expected five gh calls, got %d", len(gh.calls))
	}
	secret := gh.calls[4]
	if !reflect.DeepEqual(secret.args, []string{"secret", "set", "ACTIONS_QUOTA_TOKEN", "--repo", "owner/repo"}) || secret.stdin != fakeToken {
		t.Fatal("wrong secret write")
	}
	for _, call := range gh.calls {
		for _, arg := range call.args {
			if strings.Contains(arg, fakeToken) {
				t.Fatal("token in argv")
			}
		}
	}
	if strings.Contains(output.String(), fakeToken) {
		t.Fatal("token logged")
	}
	store := s.credentials.(*fakeCredentialStore)
	if store.saves != 1 || store.token != fakeToken {
		t.Fatal("fresh authorization was not persisted")
	}
	for _, text := range []string{"Code copied to clipboard: ABCD-EFGH", "https://github.com/login/device", "2000.00 / 3000", workflowPath, "Stored repository secret", "No existing workflow files found."} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("missing setup output %s", text)
		}
	}
}

func TestOrganizationRejectedBeforeDeviceFlow(t *testing.T) {
	gh := &fakeGH{ownerType: "Organization"}
	s, _, requests := setupFixture(t, gh, validAccount, 0)
	err := s.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "organization-owned") || *requests != 0 {
		t.Fatal("organization was not rejected before authorization")
	}
}

func TestPublicSetupIsNoOp(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint("existing workflows=", existing), func(t *testing.T) {
			gh := &fakeGH{public: true, ownerType: "Organization", repository: "some-org/example"}
			s, output, requests := setupFixture(t, gh, validAccount, 0)
			s.browser = func(context.Context, string) error { t.Fatal("public setup opened browser"); return nil }
			s.clipboard = func(context.Context, string) error { t.Fatal("public setup wrote clipboard"); return nil }
			store := s.credentials.(*fakeCredentialStore)
			store.token = fakeToken
			if existing {
				dir := filepath.Join(s.root, ".github", "workflows")
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"ci.yml", "build.yaml", "gh-actions-quota.yml"} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte("# existing custom workflow\njobs:\n  build:\n    runs-on: ubuntu-latest\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := repositorySnapshot(t, s.root)
			if err := s.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if *requests != 0 {
				t.Fatalf("public setup used device or billing API: %d requests", *requests)
			}
			if store.loads != 0 || store.saves != 0 || store.deletes != 0 || store.token != fakeToken {
				t.Fatal("public setup accessed credentials")
			}
			if !reflect.DeepEqual(gh.calls, []ghCall{
				{args: []string{"--version"}},
				{args: []string{"repo", "view", "--json", "nameWithOwner,url,isPrivate"}},
			}) {
				t.Fatalf("unexpected public setup gh calls: %v", gh.calls)
			}
			if output.String() != "Repository: some-org/example\nVisibility: Public (unmetered)\n\nSetup is not required for public repositories.\n" {
				t.Fatalf("wrong public setup output: %s", output)
			}
			if !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
				t.Fatal("public setup created or modified repository files")
			}
		})
	}
}

func TestOwnerMismatchAndMissingPlanDoNotWriteSecret(t *testing.T) {
	for _, account := range []string{`{"login":"other","type":"User","plan":{"name":"pro"}}`, `{"login":"owner","type":"User"}`,
		`{"login":"owner","type":"Organization","plan":{"name":"team"}}`, `{"login":"owner","type":"User","plan":{"name":"unknown"}}`} {
		t.Run(account, func(t *testing.T) {
			gh := &fakeGH{}
			s, output, _ := setupFixture(t, gh, account, 0)
			if err := s.run(context.Background()); err == nil {
				t.Fatal("invalid account accepted")
			}
			if len(gh.calls) != 4 {
				t.Fatal("secret written despite invalid account")
			}
			if strings.Contains(output.String(), fakeToken) {
				t.Fatal("token leaked")
			}
		})
	}
}

func TestSetupCommandFailuresAreSanitized(t *testing.T) {
	for _, stage := range []int{1, 2, 3, 4, 5} {
		gh := &fakeGH{failAt: stage}
		s, output, _ := setupFixture(t, gh, validAccount, 0)
		err := s.run(context.Background())
		if err == nil || strings.Contains(err.Error(), fakeToken) || strings.Contains(output.String(), fakeToken) {
			t.Fatalf("unsafe failure at stage %d", stage)
		}
	}
}

func TestBillingFailurePreventsSecretWrite(t *testing.T) {
	gh := &fakeGH{}
	s, _, _ := setupFixture(t, gh, validAccount, 403)
	if err := s.run(context.Background()); err == nil {
		t.Fatal("billing error ignored")
	}
	if len(gh.calls) != 4 {
		t.Fatal("secret written despite billing failure")
	}
}

func TestSetupAlwaysInitializesWorkflows(t *testing.T) {
	s, _, _ := setupFixture(t, &fakeGH{}, validAccount, 0)
	dir := filepath.Join(s.root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte("jobs:\n  build:\n    runs-on: ubuntu-latest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := s.run(context.Background())
	if err == nil || err.Error() != "workflow selection requires an interactive terminal" {
		t.Fatalf("expected workflow selection, got %v", err)
	}
}

func TestSetupReusesStoredCredential(t *testing.T) {
	gh := &fakeGH{}
	s, output, requests := setupFixture(t, gh, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.token = fakeToken
	s.browser = func(context.Context, string) error { t.Fatal("cached setup opened browser"); return nil }
	s.clipboard = func(context.Context, string) error { t.Fatal("cached setup wrote clipboard"); return nil }
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *requests != 3 {
		t.Fatalf("cached setup made %d API requests; want 3", *requests)
	}
	if store.loads != 1 || store.saves != 0 || store.deletes != 0 {
		t.Fatalf("unexpected credential operations: %+v", store)
	}
	if strings.Contains(output.String(), "/login/device") || strings.Contains(output.String(), "Code:") || strings.Contains(output.String(), "Code copied") {
		t.Fatal("cached setup displayed device authorization")
	}
}

func billingItem(quantity any, overrides map[string]any) map[string]any {
	item := map[string]any{"product": "actions", "unitType": "Minutes", "sku": "actions_linux", "repositoryName": "owner/private", "quantity": quantity,
		"pricePerUnit": 0.006, "grossAmount": 3, "discountAmount": 0, "netAmount": 3}
	for key, value := range overrides {
		item[key] = value
	}
	return item
}
