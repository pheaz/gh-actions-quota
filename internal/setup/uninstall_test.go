package setup

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUninstallRemovesRepositorySetupAndKeepsAuthentication(t *testing.T) {
	gh := &fakeGH{secretPresent: true}
	s, output, _ := setupFixture(t, gh, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.token = fakeToken

	dir := filepath.Join(s.root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "name: CI\n\non:\n  push:\n\njobs:\n  build:\n    runs-on: ubuntu-latest\n"
	withCaller, err := setQuotaCaller(original, true)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseWorkflow(withCaller)
	if err != nil {
		t.Fatal(err)
	}
	choices := doc.choices(".github/workflows/ci.yml")
	if len(choices) != 1 {
		t.Fatalf("unexpected job choices: %+v", choices)
	}
	choices[0].selected = true
	gated, err := setJobGates(withCaller, choices)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(gated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, filepath.FromSlash(workflowPath)), []byte(reusableWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(filepath.Join(dir, "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated), "gh-actions-quota") || strings.Contains(string(updated), "Quota threshold") {
		t.Fatalf("quota integration remained after uninstall:\n%s", updated)
	}
	if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(workflowPath))); !os.IsNotExist(err) {
		t.Fatalf("helper workflow was not removed: %v", err)
	}
	if store.token != fakeToken || store.loads != 0 || store.saves != 0 || store.deletes != 0 {
		t.Fatalf("uninstall changed account authentication: %+v", store)
	}
	if !reflect.DeepEqual(gh.calls, []ghCall{
		{args: []string{"repo", "view", "--json", "nameWithOwner,url"}},
		{args: []string{"secret", "list", "--repo", "owner/repo", "--json", "name"}},
		{args: []string{"secret", "delete", "ACTIONS_QUOTA_TOKEN", "--repo", "owner/repo"}},
	}) {
		t.Fatalf("unexpected uninstall gh calls: %v", gh.calls)
	}
	for _, want := range []string{
		"Repository: owner/repo",
		"Workflow integrations: removed (1 file)",
		"Helper workflow:       removed",
		"Secret:                removed",
		"Authentication: kept",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing uninstall output %q: %s", want, output)
		}
	}
}

func TestUninstallRefusesCustomQuotaExpressionBeforeChanges(t *testing.T) {
	gh := &fakeGH{secretPresent: true}
	s, _, _ := setupFixture(t, gh, validAccount, 0)
	dir := filepath.Join(s.root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := `name: CI
jobs:
  gh-actions-quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets: inherit
  build:
    needs: gh-actions-quota
    if: ${{ needs.gh-actions-quota.outputs.allowed == 'true' }}
    runs-on: ubuntu-latest
`
	path := filepath.Join(dir, "ci.yml")
	if err := os.WriteFile(path, []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, filepath.FromSlash(workflowPath)), []byte(reusableWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	before := repositorySnapshot(t, s.root)
	err := s.uninstall(context.Background())
	if err == nil || !strings.Contains(err.Error(), "custom quota expression") {
		t.Fatalf("custom expression should require manual editing: %v", err)
	}
	if !reflect.DeepEqual(before, repositorySnapshot(t, s.root)) {
		t.Fatal("failed uninstall modified repository files")
	}
	for _, call := range gh.calls {
		if len(call.args) > 1 && call.args[0] == "secret" && call.args[1] == "delete" {
			t.Fatal("failed uninstall deleted repository secret")
		}
	}
}

func TestUninstallIsIdempotentWhenSetupMissing(t *testing.T) {
	s, output, _ := setupFixture(t, &fakeGH{}, validAccount, 0)
	if err := s.uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Workflow integrations: missing",
		"Helper workflow:       missing",
		"Secret:                missing",
		"Authentication: kept",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing idempotent uninstall output %q: %s", want, output)
		}
	}
}
