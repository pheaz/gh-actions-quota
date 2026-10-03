package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureReusableWorkflowCreatesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	created, err := ensureReusableWorkflow(root)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("workflow was not created")
	}

	path := filepath.Join(root, filepath.FromSlash(workflowPath))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != reusableWorkflow {
		t.Fatal("workflow content differs from template")
	}
	for _, fragment := range []string{
		"workflow_call:",
		"ACTIONS_QUOTA_TOKEN:",
		"philippwallrafen/gh-actions-quota@v1",
		"runs-on: ubuntu-slim",
		"default: 50",
		"usage_available:",
		"usage_percent:",
	} {
		if !strings.Contains(string(data), fragment) {
			t.Errorf("workflow missing %q", fragment)
		}
	}

	created, err = ensureReusableWorkflow(root)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("identical workflow should not be rewritten")
	}
}

func TestEnsureReusableWorkflowRefusesModifiedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(workflowPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const custom = "name: custom workflow\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := ensureReusableWorkflow(root)
	if err == nil || created || !strings.Contains(err.Error(), "differs from the generated template") {
		t.Fatal("modified workflow was not rejected")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != custom {
		t.Fatal("modified workflow was overwritten")
	}
}
