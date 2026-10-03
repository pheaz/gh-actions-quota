package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeWorkflowsPromptsAndUpdatesSelectedFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ci := "name: CI\n\njobs:\n  test:\n    runs-on: ubuntu-latest\n"
	release := "name: Release\n\njobs:\n  release:\n    runs-on: ubuntu-latest\n"
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(ci), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh-actions-quota.yml"), []byte(reusableWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := initializeWorkflows(root, strings.NewReader("y\nn\n"), &output); err != nil {
		t.Fatal(err)
	}

	ciData, _ := os.ReadFile(filepath.Join(dir, "ci.yml"))
	if !hasQuotaCaller(string(ciData)) {
		t.Fatal("selected workflow was not updated")
	}
	releaseData, _ := os.ReadFile(filepath.Join(dir, "release.yaml"))
	if hasQuotaCaller(string(releaseData)) {
		t.Fatal("unselected workflow was updated")
	}
	for _, text := range []string{
		"Add quota caller to .github/workflows/ci.yml?",
		"Updated: .github/workflows/ci.yml",
		"Add quota caller to .github/workflows/release.yaml?",
		"Skipped.",
	} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("missing output %q", text)
		}
	}
}

func TestInitializeWorkflowsSkipsAlreadyConfigured(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "jobs:\n" + quotaCallerBlock + "  test:\n    runs-on: ubuntu-latest\n"
	path := filepath.Join(dir, "ci.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := initializeWorkflows(root, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Already configured: .github/workflows/ci.yml") {
		t.Fatal("configured workflow was not recognized")
	}
}

func TestAddQuotaCallerRefusesConflictingQuotaJob(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ci.yml")
	content := "jobs:\n  quota:\n    runs-on: ubuntu-latest\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	err := addQuotaCaller(path)
	if err == nil || !strings.Contains(err.Error(), "jobs.quota") {
		t.Fatal("conflicting quota job was not rejected")
	}
	data, _ := os.ReadFile(path)
	if string(data) != content {
		t.Fatal("conflicting workflow was modified")
	}
}

func TestAddQuotaCallerPreservesCRLF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ci.yml")
	content := "name: CI\r\n\r\njobs:\r\n  test:\r\n    runs-on: ubuntu-latest\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addQuotaCaller(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !hasQuotaCaller(string(data)) || strings.Contains(strings.ReplaceAll(string(data), "\r\n", ""), "\n") {
		t.Fatal("CRLF line endings were not preserved")
	}
}
