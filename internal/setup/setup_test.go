package setup

import (
	"bytes"
	"context"
	"errors"
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
	calls     []ghCall
	ownerType string
	public    bool
	failAt    int
}

type fakeCredentialStore struct {
	token     string
	loadErr   error
	saveErr   error
	deleteErr error
	loads     int
	saves     int
	deletes   int
}

func (s *fakeCredentialStore) Load(context.Context, string) (string, error) {
	s.loads++
	if s.loadErr != nil {
		return "", s.loadErr
	}
	if s.token == "" {
		return "", errCredentialNotFound
	}
	return s.token, nil
}

func (s *fakeCredentialStore) Save(_ context.Context, _ string, token string) error {
	s.saves++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.token = token
	return nil
}

func (s *fakeCredentialStore) Delete(context.Context, string) error {
	s.deletes++
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
		privacy := "true"
		if g.public {
			privacy = "false"
		}
		return []byte(`{"nameWithOwner":"owner/repo","url":"https://github.com/owner/repo","isPrivate":` + privacy + `}`), nil
	case "api":
		if g.ownerType != "" {
			return []byte(g.ownerType), nil
		}
		return []byte("User\n"), nil
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
			io.WriteString(w, `{"usageItems":[{"product":"Actions","unitType":"minutes","sku":"actions_windows","repositoryName":"owner/repo","discountAmount":6.13},{"product":"Actions","unitType":"minutes","sku":"actions_macos","repositoryName":"owner/repo","discountAmount":5.87}]}`)
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

func TestPublicRepositorySkipsAuthorizationAndSecretWrite(t *testing.T) {
	gh := &fakeGH{public: true, ownerType: "Organization"}
	s, output, requests := setupFixture(t, gh, validAccount, 0)
	s.browser = func(context.Context, string) error { t.Fatal("public setup opened browser"); return nil }
	s.clipboard = func(context.Context, string) error { t.Fatal("public setup wrote clipboard"); return nil }
	if err := s.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *requests != 0 {
		t.Fatalf("public repository unexpectedly used device or billing API: %d requests", *requests)
	}
	if !reflect.DeepEqual(gh.calls, []ghCall{
		{args: []string{"--version"}},
		{args: []string{"repo", "view", "--json", "nameWithOwner,url,isPrivate"}},
	}) {
		t.Fatalf("unexpected public setup gh calls: %v", gh.calls)
	}
	for _, call := range gh.calls {
		if len(call.args) > 0 && (call.args[0] == "api" || call.args[0] == "secret") {
			t.Fatalf("public setup unexpectedly called gh %s", call.args[0])
		}
	}
	for _, text := range []string{"Public repository:", "ACTIONS_QUOTA_TOKEN is not required", workflowPath, "No existing workflow files found."} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("missing public setup output %q", text)
		}
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
