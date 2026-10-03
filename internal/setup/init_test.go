package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScanWorkflowChoicesDetectsExistingCaller(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	configured := "jobs:\n" + quotaCallerBlock
	plain := "jobs:\n  test:\n    runs-on: ubuntu-latest\n"
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(configured), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh-actions-quota.yml"), []byte(reusableWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	choices, err := scanWorkflowChoices(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 2 {
		t.Fatalf("expected two workflow choices, got %d", len(choices))
	}
	if choices[0].path != ".github/workflows/ci.yml" || !choices[0].selected {
		t.Fatal("configured workflow was not preselected")
	}
	if choices[1].path != ".github/workflows/release.yaml" || choices[1].selected {
		t.Fatal("plain workflow selection is wrong")
	}
}

func TestSetQuotaCallerAddsInheritAndRemovesAgain(t *testing.T) {
	original := "name: CI\n\njobs:\n  test:\n    runs-on: ubuntu-latest\n"
	added, err := setQuotaCaller(original, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"  gh-actions-quota:\n",
		"    uses: ./.github/workflows/gh-actions-quota.yml\n",
		"    secrets: inherit\n",
	} {
		if !strings.Contains(added, expected) {
			t.Fatalf("missing %q", expected)
		}
	}
	if strings.Contains(added, "ACTIONS_QUOTA_TOKEN:") {
		t.Fatal("caller should use secrets: inherit")
	}

	removed, err := setQuotaCaller(added, false)
	if err != nil {
		t.Fatal(err)
	}
	if hasQuotaCaller(removed) {
		t.Fatal("caller was not removed")
	}
	if !strings.Contains(removed, "  test:\n    runs-on: ubuntu-latest") {
		t.Fatal("unrelated job was changed")
	}
}

func TestSetQuotaCallerLeavesUnselectedFileWithoutJobsUntouched(t *testing.T) {
	content := "name: Disabled\n"
	updated, err := setQuotaCaller(content, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated != content {
		t.Fatal("unselected file without jobs was modified")
	}
}

func TestSetQuotaCallerRefusesConflictingQuotaJob(t *testing.T) {
	for _, job := range []string{
		"    runs-on: ubuntu-latest\n",
		"    uses: ./.github/workflows/gh-actions-quota.yml\n",
		"    uses: ./.github/workflows/gh-actions-quota.yml\n    secrets:\n      ACTIONS_QUOTA_TOKEN: ${{ secrets.ACTIONS_QUOTA_TOKEN }}\n",
		"    uses: ./.github/workflows/other.yml\n    secrets: inherit\n",
		"    uses: ./.github/workflows/gh-actions-quota.yml\n    secrets: inherit\n    with:\n      threshold: 75\n",
		"    uses: ./.github/workflows/gh-actions-quota.yml\n    secrets: inherit\n    needs: build\n",
		"    uses: ./.github/workflows/gh-actions-quota.yml\n    secrets: inherit\n    name: custom\n",
		"    <<: *defaults\n",
	} {
		content := "defaults: &defaults\n  uses: ./.github/workflows/gh-actions-quota.yml\n  secrets: inherit\njobs:\n  gh-actions-quota:\n" + job
		if hasQuotaCaller(content) {
			t.Fatal("custom quota job recognized as canonical")
		}
		for _, selected := range []bool{false, true} {
			if _, err := setQuotaCaller(content, selected); err == nil || !strings.Contains(err.Error(), "jobs.gh-actions-quota") {
				t.Fatalf("custom quota job accepted (selected=%v): %v", selected, err)
			}
		}
	}
}

func TestCanonicalQuotaCallerIsIdempotent(t *testing.T) {
	for _, content := range []string{
		"jobs:\n" + quotaCallerBlock + "  build:\n    runs-on: ubuntu-latest\n",
		"jobs: # jobs comment\n  'gh-actions-quota': # caller comment\n    secrets: 'inherit'\n    uses: './.github/workflows/gh-actions-quota.yml' # helper\n  build:\n    runs-on: ubuntu-latest\n",
		"jobs:\n    gh-actions-quota:\n        uses: ./.github/workflows/gh-actions-quota.yml\n        secrets: inherit\n    build:\n        runs-on: ubuntu-latest\n",
	} {
		for _, text := range []string{content, strings.ReplaceAll(content, "\n", "\r\n")} {
			if !hasQuotaCaller(text) {
				t.Fatal("canonical caller not recognized")
			}
			updated, err := setQuotaCaller(text, true)
			if err != nil || updated != text {
				t.Fatalf("canonical caller was rewritten: %v", err)
			}
			removed, err := setQuotaCaller(text, false)
			if err != nil || hasQuotaCaller(removed) || !strings.Contains(removed, "runs-on: ubuntu-latest") {
				t.Fatalf("canonical caller removal failed: %v", err)
			}
		}
	}
}

func TestQuotaJobIsNotRenamedOrPreselected(t *testing.T) {
	for _, secrets := range []string{"", "    secrets: inherit\n", "    secrets:\n      ACTIONS_QUOTA_TOKEN: ${{ secrets.ACTIONS_QUOTA_TOKEN }}\n"} {
		body := "  quota:\n    uses: ./.github/workflows/gh-actions-quota.yml\n" + secrets + "  build:\n    needs: quota\n    if: needs.quota.outputs.allowed == 'true'\n    runs-on: ubuntu-latest\n"
		original := "jobs:\n" + body
		if hasQuotaCaller(original) {
			t.Fatal("jobs.quota was recognized as the current caller")
		}
		unchanged, err := setQuotaCaller(original, false)
		if err != nil || unchanged != original {
			t.Fatalf("unselected jobs.quota changed: %v", err)
		}
		added, err := setQuotaCaller(original, true)
		if err != nil || !strings.Contains(added, body) || !hasQuotaCaller(added) {
			t.Fatalf("jobs.quota or its dependencies/condition changed: %v", err)
		}
		root := t.TempDir()
		path := filepath.Join(root, ".github", "workflows", "ci.yml")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}
		choices, err := scanWorkflowChoices(root)
		if err != nil || len(choices) != 1 || choices[0].selected {
			t.Fatalf("jobs.quota was preselected: %v", err)
		}
		changed, err := applyWorkflowChoices(root, choices)
		if err != nil || len(changed) != 0 {
			t.Fatalf("jobs.quota was rewritten: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != original {
			t.Fatal("jobs.quota file changed")
		}
	}
}

func TestCustomQuotaCallerFailsBeforeAnyWorkflowWrites(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	plain := "jobs:\n  build:\n    runs-on: ubuntu-latest\n"
	custom := "jobs:\n  gh-actions-quota:\n    uses: ./.github/workflows/gh-actions-quota.yml\n"
	for name, content := range map[string]string{"a.yml": plain, "b.yml": custom} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	before := repositorySnapshot(t, root)
	if _, err := scanWorkflowChoices(root); err == nil || !strings.Contains(err.Error(), "jobs.gh-actions-quota") {
		t.Fatalf("custom quota caller was offered for editing: %v", err)
	}
	for _, selected := range []bool{false, true} {
		_, err := applyWorkflowChoices(root, []workflowChoice{{path: ".github/workflows/a.yml", selected: true}, {path: ".github/workflows/b.yml", selected: selected}})
		if err == nil || !strings.Contains(err.Error(), "jobs.gh-actions-quota") {
			t.Fatalf("custom quota caller was accepted: %v", err)
		}
		if !reflect.DeepEqual(before, repositorySnapshot(t, root)) {
			t.Fatal("workflow files changed despite conflicting quota caller")
		}
	}
}

func TestApplyWorkflowChoicesAddsAndRemoves(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	configured := "jobs:\n" + quotaCallerBlock + "  test:\n    runs-on: ubuntu-latest\n"
	plain := "jobs:\n  release:\n    runs-on: ubuntu-latest\n"
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(configured), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.yml"), []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}

	choices := []workflowChoice{
		{path: ".github/workflows/ci.yml", selected: false},
		{path: ".github/workflows/release.yml", selected: true},
	}
	changed, err := applyWorkflowChoices(root, choices)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("expected two changed workflows, got %d", len(changed))
	}
	ci, _ := os.ReadFile(filepath.Join(dir, "ci.yml"))
	release, _ := os.ReadFile(filepath.Join(dir, "release.yml"))
	if hasQuotaCaller(string(ci)) {
		t.Fatal("deselected caller was not removed")
	}
	if !hasQuotaCaller(string(release)) || !strings.Contains(string(release), "secrets: inherit") {
		t.Fatal("selected workflow did not get canonical caller")
	}
}

func TestChecklistKeys(t *testing.T) {
	for input, expected := range map[string]string{
		" ":      "toggle",
		"\r":     "apply",
		"\x03":   "cancel",
		"\x1b[A": "up",
		"\x1b[B": "down",
	} {
		got, err := readChecklistKey(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if got != expected {
			t.Fatalf("%q: expected %q, got %q", input, expected, got)
		}
	}
}

func TestDrawWorkflowChoicesShowsStars(t *testing.T) {
	choices := []workflowChoice{
		{path: ".github/workflows/ci.yml", selected: true},
		{path: ".github/workflows/release.yml", selected: false},
	}
	var output bytes.Buffer
	drawWorkflowChoices(&output, choices, 0, true)
	rendered := output.String()
	if !strings.Contains(rendered, "> * ci.yml") {
		t.Fatal("selected current workflow is missing star")
	}
	if !strings.Contains(rendered, "    release.yml") {
		t.Fatal("unselected workflow rendering is wrong")
	}
	if !strings.Contains(rendered, "Space toggle") {
		t.Fatal("checklist instructions missing")
	}
}
