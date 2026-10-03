package setup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jobWorkflow(jobs string) string {
	return "name: CI\non: push\n# Keep this comment.\njobs:\n" + quotaCallerBlock + jobs
}
func choicesFor(t *testing.T, text string) []jobChoice {
	t.Helper()
	doc, err := parseWorkflow(text)
	if err != nil {
		t.Fatal(err)
	}
	return doc.choices(".github/workflows/ci.yml")
}

func TestJobGatesPreserveFieldsAndRemoveAgain(t *testing.T) {
	original := jobWorkflow("  build:\n    # Existing build dependency.\n    needs: lint # Keep dependency comment.\n    if: github.ref == 'refs/heads/main' # Keep condition comment.\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hello # Keep step comment.\n  lint:\n    runs-on: ubuntu-latest\n")
	choices := choicesFor(t, original)
	choices[0].selected = true
	added, err := setJobGates(original, choices)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"needs: [lint, quota]", "(github.ref == 'refs/heads/main') && (" + quotaExpression("quota", "50") + ")", "# Existing build dependency.", "# Keep dependency comment.", "# Keep condition comment.", "      - run: echo hello # Keep step comment.", "# Keep this comment."} {
		if !strings.Contains(added, fragment) {
			t.Fatalf("missing %q:\n%s", fragment, added)
		}
	}
	selected := choicesFor(t, added)
	if !selected[0].selected || selected[0].threshold != "50" {
		t.Fatal("gate not recognized")
	}
	again, err := setJobGates(added, selected)
	if err != nil || again != added {
		t.Fatalf("non-idempotent: %v\n%s", err, again)
	}
	selected[0].selected = false
	removed, err := setJobGates(added, selected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(removed, "usage_percent") || strings.Contains(removed, "# Quota threshold") || strings.Contains(removed, "[lint, quota]") {
		t.Fatalf("gate not removed:\n%s", removed)
	}
	for _, fragment := range []string{"needs: [lint]", "${{ github.ref == 'refs/heads/main' }}", "# Keep condition comment.", "# Keep dependency comment."} {
		if !strings.Contains(removed, fragment) {
			t.Fatalf("missing %q:\n%s", fragment, removed)
		}
	}
}

func TestJobGateInsertsFieldsInDifferentOrders(t *testing.T) {
	for _, body := range []string{
		"    runs-on: ubuntu-latest\n", "    needs: lint\n    runs-on: ubuntu-latest\n", "    if: always()\n    runs-on: ubuntu-latest\n", "    needs:\n      - lint # lint comment\n      - test\n    runs-on: ubuntu-latest\n", "    if: >-\n      github.event_name == 'push'\n      && success()\n    runs-on: ubuntu-latest\n", "    if: github.event_name == 'push'\n      && success()\n    runs-on: ubuntu-latest\n",
	} {
		t.Run(body, func(t *testing.T) {
			original := jobWorkflow("  build:\n" + body)
			choices := choicesFor(t, original)
			choices[0].selected = true
			added, err := setJobGates(original, choices)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := parseWorkflow(added)
			if err != nil {
				t.Fatal(err)
			}
			_, job := mappingValue(doc.jobs, "build")
			_, needs := mappingValue(job, "needs")
			ids, err := dependencyIDs(needs)
			if err != nil || ids[len(ids)-1] != "quota" {
				t.Fatalf("bad needs: %v %v", ids, err)
			}
			if strings.Contains(body, "lint comment") && !strings.Contains(added, "lint comment") {
				t.Fatal("comment lost")
			}
			selected := doc.choices("")
			selected[0].selected = false
			removed, err := setJobGates(added, selected)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(removed, "usage_percent") {
				t.Fatal("gate retained")
			}
			if strings.Contains(body, "always()") && !strings.Contains(removed, "always()") {
				t.Fatal("status function lost")
			}
		})
	}
}

func TestJobGatesPreserveThresholdsAndLineEndings(t *testing.T) {
	original := jobWorkflow("  build:\n    runs-on: ubuntu-latest\n  publish:\n    runs-on: ubuntu-latest\n")
	choices := choicesFor(t, original)
	for i := range choices {
		choices[i].selected = true
	}
	added, err := setJobGates(original, choices)
	if err != nil {
		t.Fatal(err)
	}
	added = strings.Replace(added, " < 50", " < 75.5", 1)
	added = strings.ReplaceAll(added, "\n", "\r\n")
	choices = choicesFor(t, added)
	if choices[0].threshold != "75.5" || choices[1].threshold != "50" {
		t.Fatal("threshold not recognized")
	}
	repeated, err := setJobGates(added, choices)
	if err != nil || repeated != added {
		t.Fatalf("custom threshold changed: %v", err)
	}
	choices[1].selected = false
	changed, err := setJobGates(added, choices)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(changed, "\r\n", ""), "\n") {
		t.Fatal("line endings changed")
	}
	if !strings.Contains(changed, " < 75.5") {
		t.Fatal("other job threshold changed")
	}
}

func TestLegacyJobGatesUseCallerThreshold(t *testing.T) {
	for _, threshold := range []string{"", "75", "${{ inputs.threshold }}"} {
		caller := strings.Replace(quotaCallerBlock, "  quota:", "  billing:", 1)
		if threshold != "" {
			caller += "    with:\n      threshold: " + threshold + "\n"
		}
		original := "jobs:\n" + caller + "  build:\n    needs: billing\n    if: needs.billing.outputs.allowed == 'true'\n    runs-on: ubuntu-latest\n"
		choices := choicesFor(t, original)
		if !choices[0].selected {
			t.Fatal("legacy gate not selected")
		}
		if strings.Contains(threshold, "${{") {
			if choices[0].disabled == "" {
				t.Fatal("dynamic threshold should be disabled")
			}
			unchanged, err := setJobGates(original, choices)
			if err != nil || unchanged != original {
				t.Fatal("disabled gate changed")
			}
			continue
		}
		want := threshold
		if want == "" {
			want = "50"
		}
		migrated, err := setJobGates(original, choices)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(migrated, quotaExpression("billing", want)) || strings.Contains(migrated, "outputs.allowed") {
			t.Fatalf("migration failed:\n%s", migrated)
		}
	}
}

func TestUnsafeJobConditionsAreDisabled(t *testing.T) {
	for _, body := range []string{
		"    if: needs.quota.outputs.allowed == 'true' || failure()\n",
		"    needs: ${{ inputs.dependencies }}\n",
		"    if: ${{ github.ref }} == 'main'\n",
		"    <<: *defaults\n",
	} {
		original := "defaults: &defaults\n  runs-on: ubuntu-latest\n" + jobWorkflow("  build:\n"+body+"    runs-on: ubuntu-latest\n")
		choices := choicesFor(t, original)
		if choices[0].disabled == "" {
			t.Fatalf("unsafe job accepted: %s", body)
		}
	}
	if _, err := parseWorkflow("jobs:\n  test:\n    if: true\n    if: false\n"); err == nil {
		t.Fatal("duplicate keys accepted")
	}
}

func TestGroupedJobSelectionScrollsAndSkipsDisabled(t *testing.T) {
	var choices []jobChoice
	for i := 0; i < 30; i++ {
		choices = append(choices, jobChoice{path: fmt.Sprintf(".github/workflows/%d.yml", i/10), id: fmt.Sprintf("job-%d", i)})
	}
	choices[15].disabled = "custom quota expression requires manual editing"
	if nextSelectableJob(choices, 14, 1) != 16 || nextSelectableJob(choices, 16, -1) != 14 {
		t.Fatal("disabled job received focus")
	}
	for _, cursor := range []int{0, 16, 29} {
		lines := jobSelectionLines(choices, cursor, 80, 12)
		joined := strings.Join(lines, "\n")
		if len(lines) >= 12 || !strings.Contains(joined, "> [ ]   "+choices[cursor].id) || !strings.Contains(joined, filepath.Base(choices[cursor].path)) || !strings.Contains(joined, "Enter apply") {
			t.Fatalf("bad viewport:\n%s", joined)
		}
	}
}

func TestYesNoDoesNotRequireEnter(t *testing.T) {
	for _, key := range []string{"y", "Y", "n", "N", "?\ny", "\x03"} {
		yes, cancelled, err := readYesNo(strings.NewReader(key))
		if err != nil || yes != strings.HasSuffix(strings.ToLower(key), "y") || cancelled != (key == "\x03") {
			t.Fatalf("wrong response %q: %v %v %v", key, yes, cancelled, err)
		}
	}
}

func TestJobChangesValidateAllFilesBeforeWriting(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	os.MkdirAll(dir, 0755)
	first := jobWorkflow("  build:\n    runs-on: ubuntu-latest\n")
	os.WriteFile(filepath.Join(dir, "a.yml"), []byte(first), 0640)
	os.WriteFile(filepath.Join(dir, "b.yml"), []byte("jobs: [invalid]"), 0644)
	choices := choicesFor(t, first)
	choices[0].path = ".github/workflows/a.yml"
	choices[0].selected = true
	choices = append(choices, jobChoice{path: ".github/workflows/b.yml", id: "build", selected: true})
	if _, err := applyJobChoices(root, choices); err == nil {
		t.Fatal("invalid file accepted")
	}
	unchanged, _ := os.ReadFile(filepath.Join(dir, "a.yml"))
	if string(unchanged) != first {
		t.Fatal("partial edits written")
	}
}

// Run explicitly under a PTY to exercise raw input and restoration. Normal
// CI executes the pure rendering and key tests without requiring a terminal.
func TestInteractiveJobFlow(t *testing.T) {
	scenario := os.Getenv("ACTIONS_QUOTA_PTY_SCENARIO")
	if scenario == "" {
		t.Skip("requires PTY harness")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	os.MkdirAll(dir, 0755)
	original := jobWorkflow("  build:\n    runs-on: ubuntu-latest\n")
	if scenario == "scroll" {
		var jobs strings.Builder
		for i := 0; i < 30; i++ {
			fmt.Fprintf(&jobs, "  job-%d:\n    runs-on: ubuntu-latest\n", i)
		}
		original = jobWorkflow(jobs.String())
	}
	path := filepath.Join(dir, "ci.yml")
	os.WriteFile(path, []byte(original), 0644)
	if err := initializeWorkflows(root, os.Stdin, os.Stdout); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if scenario == "yes" || scenario == "scroll" {
		if !bytes.Contains(data, []byte("usage_percent")) {
			t.Fatal("selected gate missing")
		}
		if scenario == "scroll" {
			choices := choicesFor(t, string(data))
			if !choices[29].selected || choices[0].selected {
				t.Fatal("scrolling selected the wrong job")
			}
		}
	} else if string(data) != original {
		t.Fatal("cancelled or skipped step edited jobs")
	}
}

func TestJobGatePreservesCommentsExactlyOnce(t *testing.T) {
	original := jobWorkflow("  build:\n    # needs head\n    needs:\n      # sequence head\n      - lint # item comment\n      # sequence footer\n    # if head\n    if: success() # if inline\n    # condition footer\n\n    runs-on: ubuntu-latest\n")
	choices := choicesFor(t, original)
	choices[0].selected = true
	added, err := setJobGates(original, choices)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"# needs head", "# sequence head", "# item comment", "# sequence footer", "# if head", "# if inline", "# condition footer"} {
		if strings.Count(added, comment) != 1 {
			t.Fatalf("comment duplicated or lost %q:\n%s", comment, added)
		}
	}
	choices = choicesFor(t, added)
	choices[0].selected = false
	removed, err := setJobGates(added, choices)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"# needs head", "# sequence head", "# item comment", "# sequence footer", "# if head", "# if inline", "# condition footer"} {
		if strings.Count(removed, comment) != 1 {
			t.Fatalf("comment duplicated or lost %q:\n%s", comment, removed)
		}
	}
}

func TestRemovingGateKeepsDependencyComments(t *testing.T) {
	for _, needs := range []string{"    needs: quota # dependency note\n", "    needs:\n      # quota dependency\n      - quota # quota note\n", "    needs:\n      - quota # quota note\n      - lint # lint note\n"} {
		text := jobWorkflow("  build:\n" + needs + "    if: " + quotaExpression("quota", "50") + "\n    runs-on: ubuntu-latest\n")
		choices := choicesFor(t, text)
		choices[0].selected = false
		removed, err := setJobGates(text, choices)
		if err != nil {
			t.Fatal(err)
		}
		for _, comment := range []string{"# dependency note", "# quota dependency", "# quota note", "# lint note"} {
			if strings.Contains(text, comment) && strings.Count(removed, comment) != 1 {
				t.Fatalf("lost comment %s:\n%s", comment, removed)
			}
		}
	}
}

func TestGeneratedThresholdWorkflow(t *testing.T) {
	text := jobWorkflow("  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo build\n  publish:\n    needs: build\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo publish\n")
	choices := choicesFor(t, text)
	for i := range choices {
		choices[i].selected = true
	}
	generated, err := setJobGates(text, choices)
	if err != nil {
		t.Fatal(err)
	}
	generated = strings.Replace(generated, " < 50", " < 75", 1)
	if output := os.Getenv("ACTIONS_QUOTA_WORKFLOW_FIXTURE"); output != "" {
		if err := os.WriteFile(output, []byte(generated), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(output), "gh-actions-quota.yml"), []byte(reusableWorkflow), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJobGateRepairsMissingQuotaDependency(t *testing.T) {
	text := jobWorkflow("  build:\n    # Quota threshold (%): change 75 below to adjust this job's limit.\n    if: " + quotaExpression("quota", "75") + "\n    runs-on: ubuntu-latest\n")
	repaired, err := setJobGates(text, choicesFor(t, text))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repaired, "needs: quota") || !strings.Contains(repaired, " < 75") {
		t.Fatal("missing dependency not repaired")
	}
}

func TestUnrelatedBracketDependencyConditionCanBeGated(t *testing.T) {
	text := jobWorkflow("  build:\n    if: needs['lint'].result == 'success'\n    runs-on: ubuntu-latest\n")
	choices := choicesFor(t, text)
	if choices[0].disabled != "" {
		t.Fatal("unrelated condition disabled")
	}
	choices[0].selected = true
	added, err := setJobGates(text, choices)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(added, "(needs['lint'].result == 'success')") {
		t.Fatal("existing condition lost")
	}
}

func TestJobChoicesRespectSelectedFilesAndCallerAncestors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	text := jobWorkflow("  build:\n    runs-on: ubuntu-latest\n  test:\n    runs-on: ubuntu-latest\n")
	for _, name := range []string{"a.yml", "b.yml", "ignored.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	choices, err := scanJobChoices(root, []workflowChoice{{path: ".github/workflows/b.yml", selected: true}, {path: ".github/workflows/ignored.yml"}, {path: ".github/workflows/a.yml", selected: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 4 || !strings.HasSuffix(choices[0].path, "a.yml") || choices[0].id != "build" || choices[1].id != "test" || !strings.HasSuffix(choices[2].path, "b.yml") {
		t.Fatalf("bad grouping: %+v", choices)
	}
	text = strings.Replace(text, "    secrets: inherit", "    secrets: inherit\n    needs: build", 1)
	choices = choicesFor(t, text)
	if choices[0].disabled == "" || choices[1].disabled != "" {
		t.Fatal("quota caller ancestors not protected")
	}
}
