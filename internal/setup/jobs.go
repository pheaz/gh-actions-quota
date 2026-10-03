package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const thresholdComment = "Quota threshold (%%): change %s below to adjust this job's limit."

type jobChoice struct {
	path, id, caller, threshold, original string
	selected, legacy                      bool
	disabled                              string
}

type workflowDocument struct {
	text             string
	lines            []string
	jobs             *yaml.Node
	caller           string
	threshold        string
	dynamicThreshold bool
}

func mappingValue(node *yaml.Node, name string) (*yaml.Node, *yaml.Node) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == name {
			return node.Content[i], node.Content[i+1]
		}
	}
	return nil, nil
}

func parseWorkflow(text string) (*workflowDocument, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(text))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid workflow YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("workflow must contain a single YAML document")
	}
	if len(doc.Content) != 1 {
		return nil, errors.New("workflow must contain a mapping")
	}
	if err := validateMappingKeys(&doc); err != nil {
		return nil, err
	}
	_, jobs := mappingValue(doc.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode || jobs.Style&yaml.FlowStyle != 0 {
		return nil, errors.New("workflow must contain a block jobs mapping")
	}
	w := &workflowDocument{text: text, lines: strings.Split(text, "\n"), jobs: jobs, threshold: "50"}
	for i := 0; i < len(jobs.Content); i += 2 {
		job := jobs.Content[i+1]
		_, uses := mappingValue(job, "uses")
		if uses != nil && uses.Value == "./"+workflowPath {
			if w.caller != "" {
				return nil, errors.New("workflow defines multiple gh-actions-quota callers")
			}
			w.caller = jobs.Content[i].Value
			_, with := mappingValue(job, "with")
			if with != nil && with.Kind != yaml.MappingNode {
				w.dynamicThreshold = true
			}
			if merge, _ := mappingValue(with, "<<"); merge != nil {
				w.dynamicThreshold = true
			}
			_, threshold := mappingValue(with, "threshold")
			if threshold != nil {
				w.threshold = threshold.Value
				value, err := strconv.ParseFloat(w.threshold, 64)
				w.dynamicThreshold = w.dynamicThreshold || threshold.Kind != yaml.ScalarNode || err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 100
			}
		}
	}
	return w, nil
}

func validateMappingKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || seen[key.Value] {
				return errors.New("workflow contains duplicate or non-scalar mapping keys")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := validateMappingKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func quotaExpression(caller, threshold string) string {
	return fmt.Sprintf("needs.%s.outputs.usage_available == 'true' && fromJSON(needs.%s.outputs.usage_percent) < %s", caller, caller, threshold)
}

func unwrapExpression(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${{") && strings.HasSuffix(value, "}}") {
		value = strings.TrimSpace(value[3 : len(value)-2])
	}
	if strings.Contains(value, "${{") || strings.Contains(value, "}}") {
		return "", errors.New("mixed expression syntax requires manual editing")
	}
	return value, nil
}

// Recognize only the whole gate or our outer AND wrapper. Never rewrite a
// quota reference embedded in a custom boolean expression.
func recognizeGate(expression, caller string) (threshold, original string, legacy, ok bool) {
	expression = strings.TrimSpace(expression)
	match := regexp.MustCompile(`^needs\.` + regexp.QuoteMeta(caller) + `\.outputs\.usage_available\s*==\s*'true'\s*&&\s*fromJSON\(needs\.` + regexp.QuoteMeta(caller) + `\.outputs\.usage_percent\)\s*<\s*([0-9]+(?:\.[0-9]+)?)$`).FindStringSubmatch(expression)
	if match != nil {
		n, err := strconv.ParseFloat(match[1], 64)
		if err == nil && n > 0 && n <= 100 {
			return match[1], "", false, true
		}
	}
	if expression == "needs."+caller+".outputs.allowed == 'true'" {
		return "", "", true, true
	}
	// The renderer always places the original expression first, with the gate
	// enclosed in a separate pair of parentheses.
	if strings.HasPrefix(expression, "(") && strings.HasSuffix(expression, ")") {
		end := closingParen(expression, 0)
		if end > 0 {
			rest := strings.TrimSpace(expression[end+1:])
			if strings.HasPrefix(rest, "&&") {
				right := strings.TrimSpace(rest[2:])
				if len(right) > 2 && right[0] == '(' && closingParen(right, 0) == len(right)-1 {
					threshold, _, legacy, ok = recognizeGate(right[1:len(right)-1], caller)
					if ok {
						return threshold, expression[1:end], legacy, true
					}
				}
			}
		}
	}
	return "", "", false, false
}

func referencesQuota(expression, caller string) bool {
	pattern := `needs\s*(?:\.\s*` + regexp.QuoteMeta(caller) + `(?:\s*\.|\s*\[|\s*$)|\[\s*'` + regexp.QuoteMeta(caller) + `'\s*\]|\.\s*\*)`
	return regexp.MustCompile(pattern).MatchString(expression)
}

func closingParen(value string, start int) int {
	depth := 0
	quoted := false
	for i := start; i < len(value); i++ {
		if value[i] == '\'' {
			if quoted && i+1 < len(value) && value[i+1] == '\'' {
				i++
				continue
			}
			quoted = !quoted
		}
		if quoted {
			continue
		}
		if value[i] == '(' {
			depth++
		}
		if value[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func hasYAMLAnchor(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return true
	}
	for _, child := range node.Content {
		if hasYAMLAnchor(child) {
			return true
		}
	}
	return false
}

func dependencyIDs(node *yaml.Node) ([]string, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && !strings.Contains(node.Value, "${{") {
		return []string{node.Value}, nil
	}
	if node.Kind == yaml.SequenceNode {
		var ids []string
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || strings.Contains(item.Value, "${{") {
				return nil, errors.New("dynamic or aliased dependencies require manual editing")
			}
			ids = append(ids, item.Value)
		}
		return ids, nil
	}
	return nil, errors.New("dynamic or aliased dependencies require manual editing")
}

func (w *workflowDocument) choices(path string) []jobChoice {
	// Gating an ancestor of the quota caller would introduce a dependency cycle.
	ancestors := make(map[string]bool)
	var visit func(string)
	visit = func(id string) {
		if ancestors[id] {
			return
		}
		ancestors[id] = true
		_, job := mappingValue(w.jobs, id)
		_, needs := mappingValue(job, "needs")
		ids, _ := dependencyIDs(needs)
		for _, dependency := range ids {
			visit(dependency)
		}
	}
	visit(w.caller)
	var choices []jobChoice
	for i := 0; i < len(w.jobs.Content); i += 2 {
		key, job := w.jobs.Content[i], w.jobs.Content[i+1]
		if key.Value == w.caller {
			continue
		}
		c := jobChoice{path: path, id: key.Value, caller: w.caller, threshold: "50"}
		switch {
		case job.Kind != yaml.MappingNode || job.Style&yaml.FlowStyle != 0 || job.Anchor != "":
			c.disabled = "flow, anchored or aliased jobs require manual editing"
		case ancestors[c.id]:
			c.disabled = "quota caller depends on this job"
		default:
			_, needs := mappingValue(job, "needs")
			if _, err := dependencyIDs(needs); err != nil {
				c.disabled = err.Error()
			}
			if hasYAMLAnchor(needs) {
				c.disabled = "anchored dependencies require manual editing"
			}
			for j := 0; j < len(job.Content); j += 2 {
				if job.Content[j].Value == "<<" {
					c.disabled = "merged job fields require manual editing"
				}
			}
			_, condition := mappingValue(job, "if")
			if condition != nil {
				if hasYAMLAnchor(condition) {
					c.disabled = "anchored conditions require manual editing"
				}
				if condition.Kind != yaml.ScalarNode {
					c.disabled = "non-scalar condition requires manual editing"
				} else {
					expression, err := unwrapExpression(condition.Value)
					if err != nil {
						c.disabled = err.Error()
					} else {
						threshold, original, legacy, recognized := recognizeGate(expression, w.caller)
						if recognized {
							c.selected, c.legacy, c.original = true, legacy, original
							if legacy {
								c.threshold = w.threshold
								if w.dynamicThreshold {
									c.disabled = "dynamic caller threshold requires manual editing"
								}
							} else {
								c.threshold = threshold
							}
						} else if referencesQuota(expression, w.caller) {
							c.disabled = "custom quota expression requires manual editing"
						} else {
							c.original = expression
						}
					}
				}
			}
		}
		choices = append(choices, c)
	}
	return choices
}

func scanJobChoices(root string, workflows []workflowChoice) ([]jobChoice, error) {
	var choices []jobChoice
	for _, workflow := range workflows {
		if !workflow.selected {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(workflow.path)))
		if err != nil {
			return nil, err
		}
		document, err := parseWorkflow(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", workflow.path, err)
		}
		if document.caller == "" {
			return nil, fmt.Errorf("%s: quota caller not found", workflow.path)
		}
		choices = append(choices, document.choices(workflow.path)...)
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].path < choices[j].path })
	return choices, nil
}

type lineEdit struct {
	start, end int
	lines      []string
}

// Replace only job-level fields; the rest of the original document remains
// byte-for-byte intact, including comments, ordering, and line endings.
func (w *workflowDocument) fieldEdit(job *yaml.Node, name string, value *yaml.Node) (lineEdit, error) {
	key, old := mappingValue(job, name)
	indent := job.Content[0].Column - 1
	if key != nil {
		indent = key.Column - 1
	}
	start, end := job.Content[0].Line-1, job.Content[0].Line-1
	if key == nil {
		for start > 0 && strings.HasPrefix(strings.TrimSpace(w.lines[start-1]), "#") && leadingSpaces(w.lines[start-1]) >= indent {
			start--
		}
		end = start
	}
	if key != nil {
		start = key.Line - 1
		end = start + 1
		for end < len(w.lines) {
			trimmed := strings.TrimSpace(w.lines[end])
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") && leadingSpaces(w.lines[end]) <= indent {
				break
			}
			end++
		}
		// Leave trailing whitespace and comments belonging to the next field alone.
		for end > start+1 {
			trimmed := strings.TrimSpace(w.lines[end-1])
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				break
			}
			end--
		}
	}
	edit := lineEdit{start: start, end: end}
	if value != nil {
		newKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
		if key != nil {
			newKey.LineComment = key.LineComment
		}
		mapping := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{newKey, value}}
		var buffer bytes.Buffer
		encoder := yaml.NewEncoder(&buffer)
		encoder.SetIndent(2)
		if err := encoder.Encode(mapping); err != nil {
			return edit, err
		}
		encoder.Close()
		for _, line := range strings.Split(strings.TrimSuffix(buffer.String(), "\n"), "\n") {
			edit.lines = append(edit.lines, strings.Repeat(" ", indent)+line)
		}
	} else if old != nil {
		// Keep standalone comments inside the removed field and inline YAML
		// comments, without re-emitting foot comments outside its source span.
		for _, line := range w.lines[start:end] {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				edit.lines = append(edit.lines, line)
			}
		}
		var inlineComments func(*yaml.Node)
		inlineComments = func(node *yaml.Node) {
			if node.LineComment != "" {
				edit.lines = append(edit.lines, strings.Repeat(" ", indent)+node.LineComment)
			}
			for _, child := range node.Content {
				inlineComments(child)
			}
		}
		inlineComments(old)
		if key.LineComment != "" {
			edit.lines = append(edit.lines, strings.Repeat(" ", indent)+key.LineComment)
		}
	}
	return edit, nil
}

// Trailing foot comments lie outside the replaced source span and remain in
// the original lines. Do not emit a second copy when rewriting a field.
func clearTrailingFootComments(node *yaml.Node) {
	node.FootComment = ""
	if len(node.Content) > 0 {
		clearTrailingFootComments(node.Content[len(node.Content)-1])
	}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	copy := *node
	copy.Content = nil
	for _, child := range node.Content {
		copy.Content = append(copy.Content, cloneYAMLNode(child))
	}
	return &copy
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func (w *workflowDocument) gateEdits(choice jobChoice) ([]lineEdit, error) {
	_, job := mappingValue(w.jobs, choice.id)
	if job == nil || len(job.Content) == 0 {
		return nil, errors.New("job no longer exists or is empty")
	}
	_, oldNeeds := mappingValue(job, "needs")
	ids, err := dependencyIDs(oldNeeds)
	if err != nil {
		return nil, err
	}
	var needs *yaml.Node
	if oldNeeds != nil {
		needs = cloneYAMLNode(oldNeeds)
		clearTrailingFootComments(needs)
	}
	hasCaller := false
	for _, id := range ids {
		if id == choice.caller {
			hasCaller = true
		}
	}
	if choice.selected && !hasCaller {
		if needs == nil {
			needs = scalar(choice.caller)
		} else if needs.Kind == yaml.ScalarNode {
			item := *needs
			item.HeadComment, item.LineComment, item.FootComment = "", "", ""
			needs.Kind, needs.Tag, needs.Style, needs.Value = yaml.SequenceNode, "!!seq", yaml.FlowStyle, ""
			needs.Content = []*yaml.Node{&item, scalar(choice.caller)}
		} else {
			needs.Content = append(needs.Content, scalar(choice.caller))
		}
	}
	if !choice.selected && hasCaller {
		if needs.Kind == yaml.ScalarNode {
			needs = nil
		} else {
			var retained []*yaml.Node
			var comments []string
			for _, item := range needs.Content {
				if item.Value == choice.caller {
					for _, comment := range []string{item.HeadComment, item.LineComment, item.FootComment} {
						if comment != "" {
							comments = append(comments, comment)
						}
					}
				} else {
					retained = append(retained, item)
				}
			}
			if len(comments) > 0 && len(retained) > 0 {
				retained[0].HeadComment = strings.Join(append(comments, retained[0].HeadComment), "\n")
			}
			needs.Content = retained
			if len(retained) == 0 {
				needs = nil
			}
		}
	}
	var edits []lineEdit
	if choice.selected != hasCaller {
		edit, err := w.fieldEdit(job, "needs", needs)
		if err != nil {
			return nil, err
		}
		edits = append(edits, edit)
	}
	_, oldIf := mappingValue(job, "if")
	if choice.selected && !choice.legacy && oldIf != nil {
		expression, err := unwrapExpression(oldIf.Value)
		if err == nil {
			_, _, legacy, recognized := recognizeGate(expression, choice.caller)
			if recognized && !legacy {
				return edits, nil
			}
		}
	}
	expression := choice.original
	if choice.selected {
		gate := quotaExpression(choice.caller, choice.threshold)
		if expression == "" {
			expression = gate
		} else {
			expression = "(" + expression + ") && (" + gate + ")"
		}
	}
	var condition *yaml.Node
	if expression != "" {
		condition = scalar("${{ " + expression + " }}")
		if oldIf != nil {
			condition.LineComment = oldIf.LineComment
		}
	}
	edit, err := w.fieldEdit(job, "if", condition)
	if err != nil {
		return nil, err
	}
	// Remove only the comment generated by this extension.
	if edit.start > 0 && strings.HasPrefix(strings.TrimSpace(w.lines[edit.start-1]), "# Quota threshold (%): change ") && strings.HasSuffix(strings.TrimSpace(w.lines[edit.start-1]), " below to adjust this job's limit.") {
		edit.start--
	}
	if choice.selected {
		edit.lines = append([]string{strings.Repeat(" ", job.Content[0].Column-1) + "# " + fmt.Sprintf(thresholdComment, choice.threshold)}, edit.lines...)
	}
	edits = append(edits, edit)
	return edits, nil
}

func setJobGates(text string, choices []jobChoice) (string, error) {
	w, err := parseWorkflow(text)
	if err != nil {
		return "", err
	}
	fresh := w.choices("")
	var edits []lineEdit
	for _, choice := range choices {
		if choice.disabled != "" {
			continue
		}
		var current *jobChoice
		for i := range fresh {
			if fresh[i].id == choice.id {
				current = &fresh[i]
				break
			}
		}
		if current == nil || current.disabled != "" {
			return "", fmt.Errorf("%s: job requires manual editing", choice.id)
		}
		if choice.selected == current.selected && !current.legacy {
			if !choice.selected {
				continue
			}
			_, job := mappingValue(w.jobs, current.id)
			_, needs := mappingValue(job, "needs")
			ids, _ := dependencyIDs(needs)
			hasCaller := false
			for _, id := range ids {
				if id == current.caller {
					hasCaller = true
				}
			}
			if hasCaller {
				continue
			}
		}
		current.selected = choice.selected
		changes, err := w.gateEdits(*current)
		if err != nil {
			return "", fmt.Errorf("%s: %w", choice.id, err)
		}
		edits = append(edits, changes...)
	}
	return w.applyEdits(edits)
}

func (w *workflowDocument) applyEdits(edits []lineEdit) (string, error) {
	// Stable order preserves needs before if when both are inserted at one point.
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start == edits[j].start {
			return edits[i].end < edits[j].end
		}
		return edits[i].start < edits[j].start
	})
	var result []string
	offset := 0
	for _, edit := range edits {
		if edit.start < offset {
			return "", errors.New("overlapping job edits require manual editing")
		}
		result = append(result, w.lines[offset:edit.start]...)
		result = append(result, edit.lines...)
		offset = edit.end
	}
	result = append(result, w.lines[offset:]...)
	updated := strings.Join(result, "\n")
	if strings.Contains(w.text, "\r\n") {
		updated = strings.ReplaceAll(strings.ReplaceAll(updated, "\r\n", "\n"), "\n", "\r\n")
	}
	if _, err := parseWorkflow(updated); err != nil {
		return "", fmt.Errorf("edited workflow is invalid: %w", err)
	}
	return updated, nil
}

func applyJobChoices(root string, choices []jobChoice) ([]string, error) {
	type update struct {
		path string
		data []byte
		mode os.FileMode
	}
	var updates []update
	grouped := make(map[string][]jobChoice)
	for _, choice := range choices {
		grouped[choice.path] = append(grouped[choice.path], choice)
	}
	var paths []string
	for path := range grouped {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		data, err := os.ReadFile(absolute)
		if err != nil {
			return nil, err
		}
		updated, err := setJobGates(string(data), grouped[path])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if updated == string(data) {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update{path, []byte(updated), info.Mode().Perm()})
	}
	var changed []string
	for _, update := range updates {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(update.path)), update.data, update.mode); err != nil {
			return changed, err
		}
		changed = append(changed, update.path)
	}
	return changed, nil
}
