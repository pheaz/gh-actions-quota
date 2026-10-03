package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyCaller(id, secrets string) string {
	return "  " + id + ": # caller comment\n    uses: ./.github/workflows/gh-actions-quota.yml\n" + secrets
}

func TestCallerMigrationRenamesGatesAndDependencies(t *testing.T) {
	for _, id := range []string{"quota", "actions-quota", "ci-control"} {
		for _, secrets := range []string{"", "    secrets: inherit\n"} {
			for _, gate := range []string{quotaExpression(id, "75.5"), "needs." + id + ".outputs.allowed == 'true'"} {
				for _, needs := range []string{"", "    needs: " + id + " # dependency note\n", "    needs: [lint, " + id + "] # dependency note\n", "    needs:\n      - lint # lint note\n      - " + id + " # dependency note\n"} {
					t.Run(id+secrets+gate+needs, func(t *testing.T) {
						text := "name: CI\non: push\njobs:\n" + legacyCaller(id, secrets) + "    with:\n      threshold: 65\n  build:\n" + needs + "    if: ${{ (needs.lint.result == 'success') && (" + gate + ") }} # condition note\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo quota\n  lint:\n    runs-on: ubuntu-latest\n  report:\n    needs: " + id + "\n    if: always()\n    runs-on: ubuntu-latest\n"
						text = strings.ReplaceAll(text, "\n", "\r\n")
						if !hasQuotaCaller(text) {
							t.Fatal("legacy caller not recognized")
						}
						migrated, err := setQuotaCaller(text, true)
						if err != nil {
							t.Fatal(err)
						}
						doc, err := parseWorkflow(migrated)
						if err != nil {
							t.Fatal(err)
						}
						if doc.caller != "gh-actions-quota" || len(doc.jobs.Content) != 8 {
							t.Fatal("caller duplicated or not renamed")
						}
						if strings.Contains(migrated, "needs."+id+".") {
							t.Fatal("old output reference retained")
						}
						threshold := "75.5"
						if strings.Contains(gate, "outputs.allowed") {
							threshold = "65"
						}
						if !strings.Contains(migrated, "(needs.lint.result == 'success') && ("+quotaExpression("gh-actions-quota", threshold)+")") {
							t.Fatal("gate threshold or unrelated condition changed")
						}
						for _, fragment := range []string{"# caller comment", "# condition note", "run: echo quota", "if: always()", "threshold: 65"} {
							if strings.Count(migrated, fragment) != 1 {
								t.Fatalf("lost or duplicated %q", fragment)
							}
						}
						for _, comment := range []string{"# dependency note", "# lint note"} {
							if strings.Contains(text, comment) && strings.Count(migrated, comment) != 1 {
								t.Fatalf("lost or duplicated %q", comment)
							}
						}
						if strings.Contains(strings.ReplaceAll(migrated, "\r\n", ""), "\n") {
							t.Fatal("line endings changed")
						}
						for _, jobID := range []string{"build", "report"} {
							_, job := mappingValue(doc.jobs, jobID)
							_, needs := mappingValue(job, "needs")
							ids, err := dependencyIDs(needs)
							if err != nil || !strings.Contains(strings.Join(ids, ","), "gh-actions-quota") {
								t.Fatal("new dependency missing")
							}
							for _, dep := range ids {
								if dep == id {
									t.Fatal("old dependency retained")
								}
							}
						}
						repeated, err := setQuotaCaller(migrated, true)
						if err != nil || repeated != migrated {
							t.Fatal("migration is not idempotent")
						}
						choices := choicesFor(t, migrated)
						if !choices[0].selected || choices[0].threshold != threshold {
							t.Fatal("migrated gate not recognized")
						}
						choices[0].selected = false
						removed, err := setJobGates(migrated, choices)
						if err != nil {
							t.Fatal(err)
						}
						if strings.Contains(removed, "usage_percent") || !strings.Contains(removed, "needs.lint.result == 'success'") {
							t.Fatal("gate removal damaged original condition")
						}
						doc, err = parseWorkflow(removed)
						if err != nil {
							t.Fatal(err)
						}
						_, build := mappingValue(doc.jobs, "build")
						_, deps := mappingValue(build, "needs")
						remaining, _ := dependencyIDs(deps)
						for _, dep := range remaining {
							if dep == "gh-actions-quota" {
								t.Fatal("gate dependency not removed")
							}
						}
					})
				}
			}
		}
	}
}

func TestCallerMigrationRejectsUnsafeOrConflictingConfigurations(t *testing.T) {
	for _, jobs := range []string{
		"  gh-actions-quota:\n    runs-on: ubuntu-latest\n",
		quotaCallerBlock,
		"  build:\n    if: needs.quota.outputs.allowed == 'true' || failure()\n    runs-on: ubuntu-latest\n",
		"  build:\n    needs: ${{ inputs.dependencies }}\n    runs-on: ubuntu-latest\n",
		"  build:\n    if: (needs.quota.result == 'success') && (" + quotaExpression("quota", "50") + ")\n    runs-on: ubuntu-latest\n",
		"  build:\n    outputs:\n      allowed: ${{ needs.quota.outputs.allowed }}\n    runs-on: ubuntu-latest\n",
	} {
		text := "jobs:\n" + legacyCaller("quota", "    secrets: inherit\n") + jobs
		if _, err := setQuotaCaller(text, true); err == nil {
			t.Fatalf("unsafe migration accepted:\n%s", text)
		}
	}
}

func TestWorkflowSelectionMigratesLegacyCallerBeforeJobSelection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	text := "jobs:\n" + legacyCaller("quota", "") + "  build:\n    needs: quota\n    if: " + quotaExpression("quota", "50") + "\n    runs-on: ubuntu-latest\n"
	if err := os.WriteFile(path, []byte(text), 0640); err != nil {
		t.Fatal(err)
	}
	workflows, err := scanWorkflowChoices(root)
	if err != nil || len(workflows) != 1 || !workflows[0].selected {
		t.Fatal("legacy workflow not preselected")
	}
	changed, err := applyWorkflowChoices(root, workflows)
	if err != nil || len(changed) != 1 {
		t.Fatalf("workflow migration failed: %v", err)
	}
	choices, err := scanJobChoices(root, workflows)
	if err != nil || len(choices) != 1 || !choices[0].selected || choices[0].caller != "gh-actions-quota" {
		t.Fatalf("migrated job selection failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("workflow permissions changed")
	}
	changed, err = applyWorkflowChoices(root, workflows)
	if err != nil || len(changed) != 0 {
		t.Fatal("workflow migration not idempotent")
	}
}

func TestUnrelatedQuotaJobDoesNotConflict(t *testing.T) {
	text := "jobs:\n  quota:\n    runs-on: ubuntu-latest\n  build:\n    needs: quota\n    if: needs.quota.result == 'success'\n    runs-on: ubuntu-latest\n"
	updated, err := setQuotaCaller(text, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, text[len("jobs:\n"):]) || !strings.Contains(updated, quotaCallerBlock) {
		t.Fatal("unrelated quota job changed")
	}
}

func TestCallerRecognitionHandlesQuotedUsesAndInlineComments(t *testing.T) {
	text := "jobs:\n  'quota': # old caller\n    uses: './.github/workflows/gh-actions-quota.yml' # helper\n  build:\n    runs-on: ubuntu-latest\n"
	if !hasQuotaCaller(text) {
		t.Fatal("quoted caller was not recognized")
	}
	migrated, err := setQuotaCaller(text, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(migrated, "  gh-actions-quota: # old caller") || strings.Count(migrated, "uses:") != 1 {
		t.Fatal("quoted caller duplicated or not migrated")
	}
	for _, comment := range []string{"# old caller", "# helper"} {
		if strings.Count(migrated, comment) != 1 {
			t.Fatal("caller comments changed")
		}
	}
}
