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

const quotaCallerBlock = `  quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets: inherit

`

type workflowChoice struct {
	path     string
	selected bool
}

type jobBlock struct {
	id    string
	start int
	end   int
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
		return errors.New("--init requires an interactive terminal")
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
		return nil
	}
	fmt.Fprintln(output, "\nUpdated workflows:")
	for _, path := range changed {
		fmt.Fprintf(output, "  %s\n", path)
	}
	return nil
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
		choices = append(choices, workflowChoice{path: relative, selected: hasQuotaCaller(string(data))})
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
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	_, callers, _ := analyzeJobs(lines)
	return len(callers) > 0
}

func setQuotaCaller(content string, selected bool) (string, error) {
	crlf := strings.Contains(content, "\r\n")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	jobsIndex, callers, blocks := analyzeJobs(lines)
	if jobsIndex < 0 {
		return "", errors.New("workflow has no top-level jobs: key")
	}
	if len(callers) > 1 {
		return "", errors.New("workflow defines multiple gh-actions-quota callers")
	}

	if !selected {
		if len(callers) == 0 {
			return content, nil
		}
		caller := callers[0]
		result := append([]string{}, lines[:caller.start]...)
		result = append(result, lines[caller.end:]...)
		return restoreLineEndings(strings.Join(result, "\n"), crlf), nil
	}

	if len(callers) == 1 {
		result := normalizeCallerSecrets(lines, callers[0])
		return restoreLineEndings(strings.Join(result, "\n"), crlf), nil
	}

	for _, block := range blocks {
		if block.id == "quota" {
			return "", errors.New("workflow already defines jobs.quota with different content")
		}
	}

	blockLines := strings.Split(strings.TrimSuffix(quotaCallerBlock, "\n"), "\n")
	result := make([]string, 0, len(lines)+len(blockLines))
	result = append(result, lines[:jobsIndex+1]...)
	result = append(result, blockLines...)
	result = append(result, lines[jobsIndex+1:]...)
	return restoreLineEndings(strings.Join(result, "\n"), crlf), nil
}

func normalizeCallerSecrets(lines []string, caller jobBlock) []string {
	result := append([]string{}, lines...)
	secretsIndex := -1
	secretsEnd := -1
	usesIndex := -1

	for i := caller.start + 1; i < caller.end; i++ {
		trimmed := strings.TrimSpace(result[i])
		if trimmed == "uses: ./.github/workflows/gh-actions-quota.yml" {
			usesIndex = i
		}
		if leadingSpaces(result[i]) == 4 && strings.HasPrefix(trimmed, "secrets:") {
			secretsIndex = i
			secretsEnd = i + 1
			for secretsEnd < caller.end {
				line := result[secretsEnd]
				if strings.TrimSpace(line) == "" {
					secretsEnd++
					continue
				}
				if leadingSpaces(line) <= 4 {
					break
				}
				secretsEnd++
			}
			break
		}
	}

	if secretsIndex >= 0 {
		replacement := []string{"    secrets: inherit"}
		updated := make([]string, 0, len(result)-(secretsEnd-secretsIndex)+1)
		updated = append(updated, result[:secretsIndex]...)
		updated = append(updated, replacement...)
		updated = append(updated, result[secretsEnd:]...)
		return updated
	}
	if usesIndex >= 0 {
		updated := make([]string, 0, len(result)+1)
		updated = append(updated, result[:usesIndex+1]...)
		updated = append(updated, "    secrets: inherit")
		updated = append(updated, result[usesIndex+1:]...)
		return updated
	}
	return result
}

func analyzeJobs(lines []string) (int, []jobBlock, []jobBlock) {
	jobsIndex := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "jobs:" && leadingSpaces(line) == 0 {
			jobsIndex = i
			break
		}
	}
	if jobsIndex < 0 {
		return -1, nil, nil
	}

	sectionEnd := len(lines)
	var starts []int
	for i := jobsIndex + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingSpaces(lines[i])
		if indent == 0 {
			sectionEnd = i
			break
		}
		if indent == 2 && strings.HasSuffix(strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0]), ":") {
			starts = append(starts, i)
		}
	}

	var blocks []jobBlock
	var callers []jobBlock
	for index, start := range starts {
		end := sectionEnd
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		for end > start+1 {
			trimmed := strings.TrimSpace(lines[end-1])
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				break
			}
			end--
		}
		id := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.SplitN(strings.TrimSpace(lines[start]), "#", 2)[0]), ":"))
		id = strings.Trim(id, "'\"")
		block := jobBlock{id: id, start: start, end: end}
		blocks = append(blocks, block)
		for i := start + 1; i < end; i++ {
			if strings.TrimSpace(lines[i]) == "uses: ./.github/workflows/gh-actions-quota.yml" {
				callers = append(callers, block)
				break
			}
		}
	}
	return jobsIndex, callers, blocks
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func restoreLineEndings(content string, crlf bool) string {
	if crlf {
		return strings.ReplaceAll(content, "\n", "\r\n")
	}
	return content
}
