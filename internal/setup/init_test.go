package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanWorkflowChoicesDetectsExistingCaller(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	configured := `jobs:
  quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets:
      ACTIONS_QUOTA_TOKEN: ${{ secrets.ACTIONS_QUOTA_TOKEN }}
`
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
		"  quota:\n",
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

func TestSetQuotaCallerNormalizesExistingSecretMapping(t *testing.T) {
	content := `jobs:
  quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    with:
      threshold: 75
    secrets:
      ACTIONS_QUOTA_TOKEN: ${{ secrets.ACTIONS_QUOTA_TOKEN }}

  test:
    runs-on: ubuntu-latest
`
	updated, err := setQuotaCaller(content, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, "    secrets: inherit") {
		t.Fatal("existing caller was not normalized to secrets: inherit")
	}
	if strings.Contains(updated, "ACTIONS_QUOTA_TOKEN:") {
		t.Fatal("old explicit secret mapping remains")
	}
	if !strings.Contains(updated, "      threshold: 75") {
		t.Fatal("caller inputs were not preserved")
	}
}

func TestSetQuotaCallerRefusesConflictingQuotaJob(t *testing.T) {
	content := "jobs:\n  quota:\n    runs-on: ubuntu-latest\n"
	_, err := setQuotaCaller(content, true)
	if err == nil || !strings.Contains(err.Error(), "jobs.quota") {
		t.Fatal("conflicting quota job was not rejected")
	}
}

func TestSetQuotaCallerPreservesCRLF(t *testing.T) {
	content := "name: CI\r\n\r\njobs:\r\n  test:\r\n    runs-on: ubuntu-latest\r\n"
	updated, err := setQuotaCaller(content, true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasQuotaCaller(updated) || strings.Contains(strings.ReplaceAll(updated, "\r\n", ""), "\n") {
		t.Fatal("CRLF line endings were not preserved")
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
