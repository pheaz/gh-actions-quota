package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const quotaCallerBlock = `  quota:
    uses: ./.github/workflows/gh-actions-quota.yml
    secrets:
      ACTIONS_QUOTA_TOKEN: ${{ secrets.ACTIONS_QUOTA_TOKEN }}

`

func initializeWorkflows(root string, input io.Reader, output io.Writer) error {
	workflowDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(output, "\nNo existing workflow files found.")
			return nil
		}
		return errors.New("could not scan .github/workflows")
	}

	var paths []string
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
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		fmt.Fprintln(output, "\nNo existing workflow files found.")
		return nil
	}

	fmt.Fprintln(output, "\nWorkflow initialization:")
	reader := bufio.NewReader(input)
	var failures []error
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		data, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: could not read workflow", relative))
			continue
		}
		if hasQuotaCaller(string(data)) {
			fmt.Fprintf(output, "Already configured: %s\n", relative)
			continue
		}

		fmt.Fprintf(output, "Add quota caller to %s? [y/N] ", relative)
		answer, err := readAnswer(reader)
		if err != nil {
			return err
		}
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(output, "Skipped.")
			continue
		}
		if err := addQuotaCaller(path); err != nil {
			fmt.Fprintf(output, "Could not update %s: %v\n", relative, err)
			failures = append(failures, fmt.Errorf("%s: %w", relative, err))
			continue
		}
		fmt.Fprintf(output, "Updated: %s\n", relative)
	}
	return errors.Join(failures...)
}

func readAnswer(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("could not read setup selection")
	}
	return strings.ToLower(strings.TrimSpace(line)), nil
}

func hasQuotaCaller(content string) bool {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		if strings.TrimSpace(line) == "uses: ./.github/workflows/gh-actions-quota.yml" {
			return true
		}
	}
	return false
}

func addQuotaCaller(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return errors.New("could not read workflow")
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	if hasQuotaCaller(normalized) {
		return nil
	}

	lines := strings.Split(normalized, "\n")
	jobsIndex := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "jobs:" && len(line) == len(trimmed) {
			jobsIndex = i
			break
		}
	}
	if jobsIndex < 0 {
		return errors.New("workflow has no top-level jobs: key")
	}

	for i := jobsIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(line) == len(trimmed) {
			break
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			key := strings.TrimSpace(strings.TrimSuffix(strings.SplitN(trimmed, "#", 2)[0], ":"))
			key = strings.Trim(key, "'\"")
			if key == "quota" {
				return errors.New("workflow already defines jobs.quota with different content")
			}
		}
	}

	blockLines := strings.Split(strings.TrimSuffix(quotaCallerBlock, "\n"), "\n")
	result := make([]string, 0, len(lines)+len(blockLines))
	result = append(result, lines[:jobsIndex+1]...)
	result = append(result, blockLines...)
	result = append(result, lines[jobsIndex+1:]...)
	updated := strings.Join(result, "\n")
	if strings.Contains(string(content), "\r\n") {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}

	info, err := os.Stat(path)
	if err != nil {
		return errors.New("could not inspect workflow permissions")
	}
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return errors.New("could not write workflow")
	}
	return nil
}
