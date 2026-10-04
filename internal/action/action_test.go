package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/philippwallrafen/gh-actions-quota/internal/quota"
)

const fakeToken = "test-only-secret-token"

type options struct {
	env       map[string]string
	used      float64
	plan      string
	ownerType string
	status    int
	malformed bool
	login     string
}

type actionResult struct {
	values   map[string]string
	summary  string
	log      string
	requests []string
}

func runFixture(t *testing.T, opt options) actionResult {
	t.Helper()
	directory := t.TempDir()
	outputPath, summaryPath := filepath.Join(directory, "output"), filepath.Join(directory, "summary")
	env := map[string]string{
		"GITHUB_REPOSITORY_OWNER": "owner", "GITHUB_ACTOR": "other-actor", "INPUT_TOKEN": fakeToken,
		"INPUT_THRESHOLD": "50", "GITHUB_OUTPUT": outputPath, "GITHUB_STEP_SUMMARY": summaryPath,
	}
	for key, value := range opt.env {
		env[key] = value
	}
	if opt.plan == "" {
		opt.plan = "pro"
	}
	if opt.ownerType == "" {
		opt.ownerType = "User"
	}
	if opt.login == "" {
		opt.login = "owner"
	}
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+fakeToken {
			t.Error("wrong token fallback or precedence")
		}
		if opt.status != 0 {
			w.WriteHeader(opt.status)
			io.WriteString(w, fakeToken)
			return
		}
		switch r.URL.Path {
		case "/users/owner":
			json.NewEncoder(w).Encode(map[string]string{"type": opt.ownerType})
		case "/user":
			json.NewEncoder(w).Encode(map[string]any{"login": opt.login, "type": "User", "plan": map[string]string{"name": opt.plan}})
		case "/users/owner/settings/billing/usage":
			var quantity any = opt.used
			if opt.malformed {
				quantity = fakeToken
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{map[string]any{
				"product": "actions", "unitType": "Minutes", "sku": "Actions Linux", "quantity": quantity,
				"repositoryName": "owner/private", "discountAmount": 99999,
			}}})
		case "/repos/owner/private":
			io.WriteString(w, `{"private":true}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(server.Close)
	client := quota.NewClient()
	client.APIBase = server.URL
	var log bytes.Buffer
	a := &adapter{env: func(key string) string { return env[key] }, output: &log, client: client}
	if err := a.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		name, value, found := strings.Cut(line, "=")
		if !found {
			t.Fatalf("invalid output %q", line)
		}
		values[name] = value
	}
	if len(values) != 9 {
		t.Fatalf("wrong output contract: %v", values)
	}
	for _, text := range []string{string(output), string(summary), log.String()} {
		if strings.Contains(text, fakeToken) {
			t.Fatal("secret appeared in action output, summary or log")
		}
	}
	return actionResult{values: values, summary: string(summary), log: log.String(), requests: requests}
}

func TestPrivateActionThresholdAndOutputs(t *testing.T) {
	for _, test := range []struct {
		used      float64
		allowed   string
		remaining string
		percent   string
	}{
		{1499, "true", "1501", "49.966667"},
		{1500, "false", "1500", "50"},
		{1501, "false", "1499", "50.033333"},
		{4000, "false", "0", "133.333333"},
	} {
		t.Run(test.remaining, func(t *testing.T) {
			result := runFixture(t, options{used: test.used})
			want := map[string]string{
				"allowed": test.allowed, "usage-available": "true", "used-minutes": formatNumber(test.used),
				"quota-minutes": "3000", "remaining-minutes": test.remaining, "usage-percent": test.percent,
				"billing-owner": "owner", "billing-owner-type": "user", "unmetered": "false",
			}
			if !reflect.DeepEqual(result.values, want) {
				t.Fatalf("outputs=%v want=%v", result.values, want)
			}
			if !strings.Contains(result.summary, "Threshold: **50%**") || !strings.Contains(result.summary, "Allowed: **"+test.allowed+"**") || !strings.Contains(result.summary, formatNumber(test.used)+" / 3000 minutes") {
				t.Fatalf("wrong summary: %s", result.summary)
			}
			if result.log != "" {
				t.Fatalf("unexpected logging: %s", result.log)
			}
		})
	}
}

func TestQuotaOverrideAndTokenFallback(t *testing.T) {
	for _, name := range []string{"INPUT_QUOTA-MINUTES", "INPUT_QUOTA_MINUTES"} {
		t.Run(name, func(t *testing.T) {
			result := runFixture(t, options{used: 1200, plan: "unknown", env: map[string]string{
				name: "4000", "INPUT_TOKEN": "", "ACTIONS_QUOTA_TOKEN": fakeToken, "INPUT_THRESHOLD": "30",
			}})
			if result.values["quota-minutes"] != "4000" || result.values["usage-percent"] != "30" || result.values["allowed"] != "false" {
				t.Fatalf("override/fallback failure: %v", result.values)
			}
			for _, path := range result.requests {
				if path == "/user" {
					t.Fatal("override must skip plan detection")
				}
			}
		})
	}
	result := runFixture(t, options{used: 1200, env: map[string]string{"INPUT_QUOTA-MINUTES": "4000", "INPUT_QUOTA_MINUTES": "2000", "ACTIONS_QUOTA_TOKEN": "unused-fallback"}})
	if result.values["quota-minutes"] != "4000" || result.values["allowed"] != "true" {
		t.Fatalf("hyphenated input must take precedence: %v", result.values)
	}
}

func TestPublicRepositoryShortCircuit(t *testing.T) {
	event := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(event, []byte(`{"repository":{"private":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{
		{"GITHUB_REPOSITORY_VISIBILITY": "public"},
		{"GITHUB_EVENT_PATH": event},
	} {
		env["INPUT_TOKEN"] = ""
		env["INPUT_QUOTA-MINUTES"] = "invalid-ignored-for-public"
		result := runFixture(t, options{env: env})
		want := map[string]string{
			"allowed": "true", "usage-available": "true", "used-minutes": "0", "quota-minutes": "unmetered",
			"remaining-minutes": "unmetered", "usage-percent": "0", "billing-owner": "owner",
			"billing-owner-type": "unmetered", "unmetered": "true",
		}
		if !reflect.DeepEqual(result.values, want) || len(result.requests) != 0 || !strings.Contains(result.summary, "unmetered") {
			t.Fatalf("wrong public behavior: %+v", result)
		}
	}
}

func TestFailuresAreClosedAndSanitized(t *testing.T) {
	for _, opt := range []options{
		{env: map[string]string{"INPUT_TOKEN": ""}},
		{status: 401}, {status: 403}, {status: 500}, {malformed: true},
		{ownerType: "Organization"}, {plan: "unknown"}, {login: "someone-else"},
		{env: map[string]string{"INPUT_THRESHOLD": fakeToken}},
		{env: map[string]string{"INPUT_THRESHOLD": "0"}},
		{env: map[string]string{"INPUT_THRESHOLD": "101"}},
		{env: map[string]string{"INPUT_QUOTA-MINUTES": fakeToken}},
		{env: map[string]string{"INPUT_QUOTA-MINUTES": "0"}},
		{env: map[string]string{"GITHUB_REPOSITORY_OWNER": ""}},
		{env: map[string]string{"INPUT_TOKEN": "", "GITHUB_EVENT_PATH": filepath.Join(t.TempDir(), "missing")}},
		{env: map[string]string{"INPUT_THRESHOLD": "invalid", "GITHUB_REPOSITORY_VISIBILITY": "public"}},
	} {
		result := runFixture(t, opt)
		if result.values["allowed"] != "false" || result.values["usage-available"] != "false" || result.values["used-minutes"] != "unavailable" || result.values["unmetered"] != "false" {
			t.Fatalf("failure did not close gate: %v", result.values)
		}
		if !strings.Contains(result.log, "::warning title=Actions quota::"+unavailableMessage) || !strings.Contains(result.summary, "Usage unavailable.") {
			t.Fatalf("missing warning or failure summary: %+v", result)
		}
		if opt.ownerType == "Organization" && len(result.requests) != 1 {
			t.Fatal("organizations must fail before plan or billing requests")
		}

	}
}

func TestMissingTokenDoesNotCallAPI(t *testing.T) {
	result := runFixture(t, options{env: map[string]string{"INPUT_TOKEN": "", "GITHUB_REPOSITORY_VISIBILITY": "private"}})
	if len(result.requests) != 0 {
		t.Fatal("missing token must fail before API calls")
	}
}

func TestOutputFallbackAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(path, []byte("existing=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	env := map[string]string{"GITHUB_REPOSITORY_VISIBILITY": "public", "GITHUB_REPOSITORY_OWNER": "owner\r\nallowed=false", "GITHUB_OUTPUT": path}
	a := &adapter{env: func(key string) string { return env[key] }, output: &output}
	if err := a.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "existing=value\nallowed=true\n") || !strings.Contains(string(data), "billing-owner=owner  allowed=false\n") {
		t.Fatalf("wrong append or newline escaping: %s", data)
	}
	delete(env, "GITHUB_OUTPUT")
	if err := a.run(context.Background()); err != nil || !strings.Contains(output.String(), "allowed=true\n") {
		t.Fatalf("stdout fallback failed: %v %s", err, output.String())
	}
}

func TestOutputAndSummaryWriteErrorsAreSanitized(t *testing.T) {
	for _, key := range []string{"GITHUB_OUTPUT", "GITHUB_STEP_SUMMARY"} {
		env := map[string]string{"GITHUB_REPOSITORY_VISIBILITY": "public", key: filepath.Join(t.TempDir(), fakeToken, "missing")}
		a := &adapter{env: func(key string) string { return env[key] }, output: io.Discard}
		if err := a.run(context.Background()); err == nil || strings.Contains(err.Error(), fakeToken) {
			t.Fatalf("missing or unsafe file error: %v", err)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportErrorIsSanitized(t *testing.T) {
	client := quota.NewClient()
	client.HTTP.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New(fakeToken) })
	env := map[string]string{"GITHUB_REPOSITORY_OWNER": "owner", "INPUT_TOKEN": fakeToken}
	var output bytes.Buffer
	a := &adapter{env: func(key string) string { return env[key] }, output: &output, client: client}
	if err := a.run(context.Background()); err != nil || !strings.Contains(output.String(), "allowed=false") || strings.Contains(output.String(), fakeToken) {
		t.Fatalf("unsafe transport failure: %v %s", err, output.String())
	}
}
