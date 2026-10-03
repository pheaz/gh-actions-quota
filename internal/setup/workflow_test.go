package setup

import (
	"fmt"
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

func TestEnsureReusableWorkflowMigratesLegacyTemplate(t *testing.T) {
	legacy, err := os.ReadFile("testdata/legacy-gh-actions-quota.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(legacy) != legacyReusableWorkflow {
		t.Fatal("legacy template recognition differs from the previous generated file")
	}
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint(custom), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(workflowPath))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			content := string(legacy)
			if custom {
				content = strings.Replace(content, "default: 50", "default: 75", 1)
			}
			if err := os.WriteFile(path, []byte(content), 0640); err != nil {
				t.Fatal(err)
			}
			// Compare the actual mode: Windows does not implement Unix permission bits,
			// and a Unix umask can alter the requested creation mode.
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := ensureReusableWorkflow(root)
			if custom {
				if err == nil || changed {
					t.Fatal("customized legacy template was overwritten")
				}
			} else if err != nil || !changed {
				t.Fatalf("migration failed: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := reusableWorkflow
			if custom {
				want = content
			}
			if string(data) != want {
				t.Fatal("unexpected workflow content")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != before.Mode().Perm() {
				t.Fatal("file permissions changed")
			}
			if !custom {
				changed, err = ensureReusableWorkflow(root)
				if err != nil || changed {
					t.Fatal("migration is not idempotent")
				}
			}
		})
	}
}
