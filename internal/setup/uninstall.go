package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Uninstall removes repository-specific gh-actions-quota setup from the current
// checkout and repository. Account-scoped authentication is intentionally kept.
func Uninstall(ctx context.Context, input io.Reader, output io.Writer) error {
	root, err := os.Getwd()
	if err != nil {
		return errors.New("could not resolve the current working directory")
	}
	s := newSetup(input, output)
	s.root = root
	return s.uninstall(ctx)
}

type workflowUninstallUpdate struct {
	path string
	data []byte
	mode os.FileMode
}

func (s *setup) uninstall(ctx context.Context) error {
	repository, err := s.currentRepositoryForUninstall(ctx)
	if err != nil {
		return err
	}

	secretPresent, err := s.repositorySecretPresent(ctx, repository)
	if err != nil {
		return err
	}
	updates, helperPresent, err := prepareWorkflowUninstall(s.root)
	if err != nil {
		return err
	}

	for _, update := range updates {
		absolute := filepath.Join(s.root, filepath.FromSlash(update.path))
		if err := os.WriteFile(absolute, update.data, update.mode); err != nil {
			return fmt.Errorf("%s: could not remove quota integration", update.path)
		}
	}
	if helperPresent {
		if err := os.Remove(filepath.Join(s.root, filepath.FromSlash(workflowPath))); err != nil {
			return fmt.Errorf("could not remove %s", workflowPath)
		}
	}
	if secretPresent {
		if _, err := s.gh.Run(ctx, []string{"secret", "delete", secretName, "--repo", repository}, nil); err != nil {
			return errors.New("could not delete ACTIONS_QUOTA_TOKEN repository secret")
		}
	}

	fmt.Fprintf(s.output, "Repository: %s\n\nUninstall:\n", repository)
	fmt.Fprintf(s.output, "  Workflow integrations: %s\n", removalSummary(len(updates)))
	fmt.Fprintf(s.output, "  Helper workflow:       %s\n", removedOrMissing(helperPresent))
	fmt.Fprintf(s.output, "  Secret:                %s\n", removedOrMissing(secretPresent))
	fmt.Fprintln(s.output, "\nAuthentication: kept")
	return nil
}

func (s *setup) currentRepositoryForUninstall(ctx context.Context) (string, error) {
	data, err := s.gh.Run(ctx, []string{"repo", "view", "--json", "nameWithOwner,url"}, nil)
	if err != nil {
		return "", errors.New("could not resolve the current repository; run uninstall from its checkout")
	}
	var repo struct {
		Name string `json:"nameWithOwner"`
		URL  string `json:"url"`
	}
	if json.Unmarshal(data, &repo) != nil || !repositoryPattern.MatchString(repo.Name) || repo.URL != "https://github.com/"+repo.Name {
		return "", errors.New("uninstall requires a current repository hosted on github.com")
	}
	return repo.Name, nil
}

func prepareWorkflowUninstall(root string) ([]workflowUninstallUpdate, bool, error) {
	choices, err := scanWorkflowChoices(root)
	if err != nil {
		return nil, false, err
	}
	var updates []workflowUninstallUpdate
	for _, choice := range choices {
		absolute := filepath.Join(root, filepath.FromSlash(choice.path))
		data, err := os.ReadFile(absolute)
		if err != nil {
			return nil, false, fmt.Errorf("%s: could not read workflow", choice.path)
		}
		document, err := parseWorkflow(string(data))
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", choice.path, err)
		}
		if document.caller != quotaJobID {
			continue
		}
		updated, err := removeQuotaIntegration(string(data))
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", choice.path, err)
		}
		if updated == string(data) {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, false, fmt.Errorf("%s: could not inspect workflow", choice.path)
		}
		updates = append(updates, workflowUninstallUpdate{path: choice.path, data: []byte(updated), mode: info.Mode().Perm()})
	}

	helper := filepath.Join(root, filepath.FromSlash(workflowPath))
	data, err := os.ReadFile(helper)
	if errors.Is(err, os.ErrNotExist) {
		return updates, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("could not inspect %s", workflowPath)
	}
	if string(data) != reusableWorkflow {
		return nil, false, fmt.Errorf("%s differs from the generated template; remove it manually", workflowPath)
	}
	return updates, true, nil
}

func removeQuotaIntegration(text string) (string, error) {
	w, err := parseWorkflow(text)
	if err != nil {
		return "", err
	}
	if w.caller != quotaJobID {
		return text, nil
	}

	choices := w.choices("")
	byID := make(map[string]jobChoice, len(choices))
	for _, choice := range choices {
		byID[choice.id] = choice
	}

	var edits []lineEdit
	for i := 0; i < len(w.jobs.Content); i += 2 {
		key, job := w.jobs.Content[i], w.jobs.Content[i+1]
		if key.Value == quotaJobID {
			continue
		}
		choice := byID[key.Value]

		_, needs := mappingValue(job, "needs")
		ids, dependencyErr := dependencyIDs(needs)
		hasCallerDependency := false
		if dependencyErr == nil {
			for _, id := range ids {
				if id == quotaJobID {
					hasCallerDependency = true
					break
				}
			}
		} else if yamlNodeContains(needs, quotaJobID) {
			return "", fmt.Errorf("job %s has a quota dependency that requires manual editing", key.Value)
		}

		_, condition := mappingValue(job, "if")
		hasRecognizedGate := choice.selected
		if condition != nil && !hasRecognizedGate {
			if condition.Kind != yaml.ScalarNode {
				if yamlNodeContains(condition, quotaJobID) {
					return "", fmt.Errorf("job %s has a quota condition that requires manual editing", key.Value)
				}
			} else {
				expression, unwrapErr := unwrapExpression(condition.Value)
				if unwrapErr != nil {
					if strings.Contains(condition.Value, quotaJobID) {
						return "", fmt.Errorf("job %s has a quota condition that requires manual editing", key.Value)
					}
				} else if referencesQuota(expression, quotaJobID) {
					return "", fmt.Errorf("job %s has a custom quota expression; edit it manually before uninstalling", key.Value)
				}
			}
		}

		if !hasCallerDependency && !hasRecognizedGate {
			continue
		}
		if choice.disabled != "" {
			return "", fmt.Errorf("job %s requires manual editing: %s", key.Value, choice.disabled)
		}
		if hasRecognizedGate {
			choice.selected = false
			changes, err := w.gateEdits(choice)
			if err != nil {
				return "", fmt.Errorf("job %s: %w", key.Value, err)
			}
			edits = append(edits, changes...)
			continue
		}

		needsCopy := cloneYAMLNode(needs)
		clearTrailingFootComments(needsCopy)
		if needsCopy.Kind == yaml.ScalarNode {
			needsCopy = nil
		} else {
			var retained []*yaml.Node
			for _, item := range needsCopy.Content {
				if item.Value != quotaJobID {
					retained = append(retained, item)
				}
			}
			needsCopy.Content = retained
			if len(retained) == 0 {
				needsCopy = nil
			}
		}
		edit, err := w.fieldEdit(job, "needs", needsCopy)
		if err != nil {
			return "", fmt.Errorf("job %s: %w", key.Value, err)
		}
		edits = append(edits, edit)
	}

	updated, err := w.applyEdits(edits)
	if err != nil {
		return "", err
	}
	return setQuotaCaller(updated, false)
}

func yamlNodeContains(node *yaml.Node, value string) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.ScalarNode && strings.Contains(node.Value, value) {
		return true
	}
	for _, child := range node.Content {
		if yamlNodeContains(child, value) {
			return true
		}
	}
	return false
}

func removalSummary(count int) string {
	if count == 0 {
		return "missing"
	}
	if count == 1 {
		return "removed (1 file)"
	}
	return fmt.Sprintf("removed (%d files)", count)
}

func removedOrMissing(present bool) string {
	if present {
		return "removed"
	}
	return "missing"
}
