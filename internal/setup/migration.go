package setup

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Rename recognized callers together with their dependencies and canonical gates
// before the optional job checklist, so skipping that checklist is safe too.
func migrateQuotaCaller(text string) (string, error) {
	w, err := parseWorkflow(text)
	if err != nil {
		return "", err
	}
	if w.caller == quotaJobID {
		return text, nil
	}
	if key, _ := mappingValue(w.jobs, quotaJobID); key != nil {
		return "", fmt.Errorf("workflow already defines jobs.%s with different content", quotaJobID)
	}
	if hasYAMLAnchor(w.jobs) {
		return "", fmt.Errorf("anchored quota caller migration requires manual editing")
	}
	oldCaller := w.caller
	choices := w.choices("")
	var edits []lineEdit
	for i := 0; i < len(w.jobs.Content); i += 2 {
		key, job := w.jobs.Content[i], w.jobs.Content[i+1]
		_, oldNeeds := mappingValue(job, "needs")
		ids, err := dependencyIDs(oldNeeds)
		if err != nil {
			return "", fmt.Errorf("%s: %w", key.Value, err)
		}
		renamedNeeds := false
		for _, id := range ids {
			if id != oldCaller {
				continue
			}
			needs := cloneYAMLNode(oldNeeds)
			clearTrailingFootComments(needs)
			if needs.Kind == yaml.ScalarNode {
				needs.Value = quotaJobID
			} else {
				for _, item := range needs.Content {
					if item.Value == oldCaller {
						item.Value = quotaJobID
					}
				}
			}
			edit, err := w.fieldEdit(job, "needs", needs)
			if err != nil {
				return "", err
			}
			edits = append(edits, edit)
			renamedNeeds = true
			break
		}
		_, condition := mappingValue(job, "if")
		for _, choice := range choices {
			if choice.id != key.Value || !choice.selected {
				continue
			}
			if choice.disabled != "" {
				return "", fmt.Errorf("%s: %s; caller migration requires manual editing", key.Value, choice.disabled)
			}
			if referencesQuota(choice.original, oldCaller) {
				return "", fmt.Errorf("%s: custom quota reference requires manual editing before caller migration", key.Value)
			}
			choice.caller = quotaJobID
			choice.legacy = true // Force rendering with the new caller ID.
			changes, err := w.gateEdits(choice)
			if err != nil {
				return "", err
			}
			// Avoid overlapping dependency edits when the old dependency was renamed.
			if renamedNeeds {
				changes = changes[len(changes)-1:]
			}
			edits = append(edits, changes...)
			condition = nil
		}
		// Custom references cannot be rewritten as a canonical gate without changing
		// their meaning. Refuse the rename instead of leaving dangling references.
		var checkReferences func(*yaml.Node) bool
		checkReferences = func(node *yaml.Node) bool {
			if node.Kind == yaml.ScalarNode && referencesQuota(node.Value, oldCaller) {
				return true
			}
			for _, child := range node.Content {
				if checkReferences(child) {
					return true
				}
			}
			return false
		}
		for j := 0; j < len(job.Content); j += 2 {
			field, value := job.Content[j].Value, job.Content[j+1]
			if field == "needs" || (field == "if" && condition == nil) {
				continue
			}
			if checkReferences(value) {
				return "", fmt.Errorf("%s: custom quota reference requires manual editing before caller migration", key.Value)
			}
		}
		if key.Value == oldCaller {
			line := w.lines[key.Line-1]
			colon := strings.Index(line[key.Column-1:], ":") + key.Column - 1
			edits = append(edits, lineEdit{start: key.Line - 1, end: key.Line, lines: []string{line[:key.Column-1] + quotaJobID + line[colon:]}})
		}
	}
	return w.applyEdits(edits)
}
