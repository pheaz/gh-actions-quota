package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/term"
)

const quotaCallerBlock = `  gh-actions-quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets: inherit

`

type workflowChoice struct {
	path     string
	selected bool
}

func initializeWorkflows(root string, input io.Reader, output io.Writer) error {
	choices, err := scanWorkflowChoices(root)
	if err != nil {
		return err
	}
	if len(choices) == 0 {
		fmt.Fprintln(output, "\nNo existing workflow files found.")
		return nil
	}

	file, ok := input.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return errors.New("workflow selection requires an interactive terminal")
	}
	if err := selectWorkflowChoices(file, output, choices); err != nil {
		return err
	}

	changed, err := applyWorkflowChoices(root, choices)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Fprintln(output, "\nNo workflow changes.")
	} else {
		fmt.Fprintln(output, "\nUpdated workflows:")
		for _, path := range changed {
			fmt.Fprintf(output, "  %s\n", path)
		}
	}
	return initializeJobs(root, file, output, choices)
}

func scanWorkflowChoices(root string) ([]workflowChoice, error) {
	workflowDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.New("could not scan .github/workflows")
	}

	var choices []workflowChoice
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yml" && ext != ".yaml" {
			continue
		}
		relative := filepath.ToSlash(filepath.Join(".github", "workflows", entry.Name()))
		if relative == workflowPath {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return nil, fmt.Errorf("%s: could not read workflow", relative)
		}
		document, err := parseWorkflow(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", relative, err)
		}
		choices = append(choices, workflowChoice{path: relative, selected: document.caller == quotaJobID})
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].path < choices[j].path })
	return choices, nil
}

func selectWorkflowChoices(input *os.File, output io.Writer, choices []workflowChoice) error {
	fd := int(input.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return errors.New("could not enter interactive selection mode")
	}
	defer term.Restore(fd, state)

	cursor := 0
	first := true
	for {
		drawWorkflowChoices(output, choices, cursor, first)
		first = false

		key, err := readChecklistKey(input)
		if err != nil {
			return errors.New("could not read workflow selection")
		}
		switch key {
		case "up":
			if cursor > 0 {
				cursor--
			}
		case "down":
			if cursor < len(choices)-1 {
				cursor++
			}
		case "toggle":
			choices[cursor].selected = !choices[cursor].selected
		case "apply":
			fmt.Fprint(output, "\r\n")
			return nil
		case "cancel":
			fmt.Fprint(output, "\r\n")
			return errors.New("workflow initialization cancelled")
		}
	}
}

func drawWorkflowChoices(output io.Writer, choices []workflowChoice, cursor int, first bool) {
	lineCount := len(choices) + 3
	if !first {
		fmt.Fprintf(output, "\x1b[%dA", lineCount)
	}
	fmt.Fprint(output, "\r\x1b[2KSelect workflows for quota:\r\n")
	for i, choice := range choices {
		pointer := "  "
		if i == cursor {
			pointer = "> "
		}
		marker := " "
		if choice.selected {
			marker = "*"
		}
		fmt.Fprintf(output, "\r\x1b[2K%s%s %s\r\n", pointer, marker, filepath.Base(choice.path))
	}
	fmt.Fprint(output, "\r\x1b[2K\r\n")
	fmt.Fprint(output, "\r\x1b[2KUp/Down move, Space toggle, Enter apply, Ctrl-C cancel\r\n")
}

func readChecklistKey(input io.Reader) (string, error) {
	var b [1]byte
	if _, err := io.ReadFull(input, b[:]); err != nil {
		return "", err
	}
	switch b[0] {
	case ' ':
		return "toggle", nil
	case '\r', '\n':
		return "apply", nil
	case 3:
		return "cancel", nil
	case 27:
		var seq [2]byte
		if _, err := io.ReadFull(input, seq[:]); err != nil {
			return "cancel", nil
		}
		if seq[0] == '[' {
			switch seq[1] {
			case 'A':
				return "up", nil
			case 'B':
				return "down", nil
			}
		}
		return "cancel", nil
	}
	return "", nil
}

func applyWorkflowChoices(root string, choices []workflowChoice) ([]string, error) {
	type update struct {
		path    string
		content []byte
		mode    os.FileMode
	}
	var updates []update

	for _, choice := range choices {
		path := filepath.Join(root, filepath.FromSlash(choice.path))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: could not read workflow", choice.path)
		}
		updated, err := setQuotaCaller(string(data), choice.selected)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", choice.path, err)
		}
		if updated == string(data) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%s: could not inspect workflow", choice.path)
		}
		updates = append(updates, update{path: choice.path, content: []byte(updated), mode: info.Mode().Perm()})
	}

	var changed []string
	for _, update := range updates {
		path := filepath.Join(root, filepath.FromSlash(update.path))
		if err := os.WriteFile(path, update.content, update.mode); err != nil {
			return changed, fmt.Errorf("%s: could not write workflow", update.path)
		}
		changed = append(changed, update.path)
	}
	return changed, nil
}

func hasQuotaCaller(content string) bool {
	document, err := parseWorkflow(content)
	return err == nil && document.caller == quotaJobID
}

func setQuotaCaller(content string, selected bool) (string, error) {
	w, err := parseWorkflow(content)
	if err != nil {
		if !selected && errors.Is(err, errNoJobs) {
			return content, nil
		}
		return "", err
	}
	if selected == (w.caller == quotaJobID) {
		return content, nil
	}

	if !selected {
		key, _ := mappingValue(w.jobs, quotaJobID)
		start, end := key.Line-1, key.Line
		for end < len(w.lines) {
			trimmed := strings.TrimSpace(w.lines[end])
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") && leadingSpaces(w.lines[end]) < key.Column {
				break
			}
			end++
		}
		for end > start+1 {
			trimmed := strings.TrimSpace(w.lines[end-1])
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				break
			}
			end--
		}
		return w.applyEdits([]lineEdit{{start: start, end: end}})
	}

	blockLines := strings.Split(strings.TrimSuffix(quotaCallerBlock, "\n"), "\n")
	if len(w.jobs.Content) > 0 {
		indent := strings.Repeat(" ", w.jobs.Content[0].Column-1)
		for i, line := range blockLines {
			if line != "" {
				blockLines[i] = indent + strings.TrimPrefix(line, "  ")
			}
		}
	}
	return w.applyEdits([]lineEdit{{start: w.jobsLine + 1, end: w.jobsLine + 1, lines: blockLines}})
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}
