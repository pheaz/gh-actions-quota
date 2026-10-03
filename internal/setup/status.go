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
)

// Status reports account quota usage without changing repository files or secrets.
// It requires a stored gh-actions-quota authorization and never starts device flow.
func Status(ctx context.Context, input io.Reader, output io.Writer) error {
	return newSetup(input, output).status(ctx)
}

func (s *setup) status(ctx context.Context) error {
	repo, found, err := s.statusRepository(ctx)
	if err != nil {
		return err
	}

	var account string
	if !found {
		fmt.Fprintln(s.output, "Repository: not found")
		account, err = s.currentPersonalAccount(ctx)
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(s.output, "Repository: %s\n", repo.Name)
		if repo.Private {
			fmt.Fprintln(s.output, "Visibility: Private (metered)")
			account, _, _ = strings.Cut(repo.Name, "/")
			if err := s.checkOwner(ctx, account); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(s.output, "Visibility: Public (unmetered)")
			account, err = s.currentPersonalAccount(ctx)
			if err != nil {
				return err
			}
		}

		workflowPresent, err := s.workflowPresent()
		if err != nil {
			return err
		}
		secretPresent, secretErr := s.repositorySecretPresent(ctx, repo.Name)
		if repo.Private && secretErr != nil {
			return secretErr
		}
		if repo.Private || workflowPresent || (secretErr == nil && secretPresent) {
			fmt.Fprintln(s.output, "\nSetup:")
			if repo.Private || workflowPresent {
				fmt.Fprintf(s.output, "  Workflow: %s\n", presenceLabel(workflowPresent))
			}
			if repo.Private || (secretErr == nil && secretPresent) {
				fmt.Fprintf(s.output, "  Secret:   %s\n", presenceLabel(secretPresent))
			}
		}
	}

	token, plan, quota, err := s.storedAuthorizationForOwner(ctx, account)
	if err != nil {
		return err
	}
	used, err := s.client.checkBilling(ctx, account, token)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.output, "\nActions quota:\n  Account: %s\n  Used:    %.2f / %d min  ( %.2f%% )\n  Plan:    %s\n",
		account, used, quota, used/float64(quota)*100, displayPlan(plan))
	return nil
}

type statusRepositoryInfo struct {
	Name    string
	Private bool
}

func (s *setup) statusRepository(ctx context.Context) (statusRepositoryInfo, bool, error) {
	data, err := s.gh.Run(ctx, []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}, nil)
	if err != nil {
		return statusRepositoryInfo{}, false, nil
	}
	var repo struct {
		Name    string `json:"nameWithOwner"`
		Private *bool  `json:"isPrivate"`
	}
	if json.Unmarshal(data, &repo) != nil || !repositoryPattern.MatchString(repo.Name) || repo.Private == nil {
		return statusRepositoryInfo{}, false, errors.New("GitHub returned invalid repository information")
	}
	return statusRepositoryInfo{Name: repo.Name, Private: *repo.Private}, true, nil
}

func (s *setup) workflowPresent() (bool, error) {
	path := filepath.Join(s.root, filepath.FromSlash(workflowPath))
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not inspect %s", workflowPath)
	}
	return info.Mode().IsRegular(), nil
}

func (s *setup) repositorySecretPresent(ctx context.Context, repository string) (bool, error) {
	data, err := s.gh.Run(ctx, []string{"secret", "list", "--repo", repository, "--json", "name"}, nil)
	if err != nil {
		return false, errors.New("could not inspect repository Actions secrets")
	}
	var secrets []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &secrets) != nil {
		return false, errors.New("GitHub CLI returned invalid repository secret metadata")
	}
	for _, secret := range secrets {
		if secret.Name == secretName {
			return true, nil
		}
	}
	return false, nil
}

func presenceLabel(present bool) string {
	if present {
		return "present"
	}
	return "missing"
}

func (s *setup) currentPersonalAccount(ctx context.Context) (string, error) {
	data, err := s.gh.Run(ctx, []string{"api", "user", "--hostname", "github.com"}, nil)
	if err != nil {
		return "", errors.New("could not determine the current GitHub account; authenticate GitHub CLI with gh auth login --hostname github.com")
	}
	var account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if json.Unmarshal(data, &account) != nil || !accountPattern.MatchString(account.Login) || !strings.EqualFold(account.Type, "User") {
		return "", errors.New("a supported personal GitHub account must be signed in with gh on github.com")
	}
	return account.Login, nil
}

func displayPlan(plan string) string {
	words := strings.Fields(strings.ToLower(plan))
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
