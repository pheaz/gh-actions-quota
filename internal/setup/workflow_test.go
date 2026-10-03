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
		"name: gh-actions-quota\n",
		"  gh-actions-quota:\n    name: gh-actions-quota\n",
		"id: gh-actions-quota\n",
		"jobs.gh-actions-quota.outputs.allowed",
		"jobs.gh-actions-quota.outputs.usage_available",
		"jobs.gh-actions-quota.outputs.usage_percent",
		"steps.gh-actions-quota.outputs.allowed",
		"steps.gh-actions-quota.outputs['usage-available']",
		"steps.gh-actions-quota.outputs['usage-percent']",
		"workflow_call:",
		"ACTIONS_QUOTA_TOKEN:\n        required: false",
		"philippwallrafen/gh-actions-quota@" + actionMajor,
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

func TestEnsureReusableWorkflowRefusesPreviousTemplate(t *testing.T) {
	previous, err := os.ReadFile("testdata/legacy-gh-actions-quota.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{string(previous), strings.Replace(string(previous), "default: 50", "default: 75", 1)} {
		root := t.TempDir()
		path := filepath.Join(root, filepath.FromSlash(workflowPath))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		for attempt := 0; attempt < 2; attempt++ {
			changed, err := ensureReusableWorkflow(root)
			if err == nil || changed || !strings.Contains(err.Error(), "differs from the generated template") {
				t.Fatalf("previous helper was accepted: changed=%v err=%v", changed, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatal("previous helper was modified")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != before.Mode().Perm() || !info.ModTime().Equal(before.ModTime()) {
				t.Fatal("previous helper was rewritten")
			}
		}
	}
}
