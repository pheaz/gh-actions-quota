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

	"github.com/philippwallrafen/gh-actions-quota/internal/quota"
)

// Status reports account quota usage without changing repository files or secrets.
// Metered quota requires a stored authorization; status never starts device flow.
func Status(ctx context.Context, input io.Reader, output io.Writer) error {
	return StatusWithQuota(ctx, input, output, "")
}

// StatusWithQuota overrides the allowance for this invocation without editing files.
func StatusWithQuota(ctx context.Context, input io.Reader, output io.Writer, included string) error {
	s := newSetup(input, output)
	s.quotaOverride = included
	return s.status(ctx)
}

func (s *setup) status(ctx context.Context) error {
	repo, found, err := s.statusRepository(ctx)
	if err != nil {
		return err
	}

	var account string
	kind := "user"
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
			kind, err = s.checkOwner(ctx, account)
			if err != nil {
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

	if s.quotaOverride != "" {
		if _, err := quota.ParsePositive(s.quotaOverride); err != nil {
			return err
		}
	} else if found && repo.Private {
		s.quotaOverride, err = workflowQuotaOverride(s.root)
		if err != nil {
			return err
		}
	}
	token, plan, included, err := s.storedAuthorizationForOwner(ctx, account, kind)
	if err != nil {
		if found && !repo.Private && errors.Is(err, errAuthenticationRequired) {
			fmt.Fprintln(s.output, "\nStandard GitHub-hosted runners are unmetered for this public repository.")
			return nil
		}
		return err
	}
	used, err := s.client.checkBilling(ctx, account, token, kind)
	if err != nil {
		return err
	}
	allowance := float64(included)
	if s.quotaOverride != "" {
		allowance, _ = quota.ParsePositive(s.quotaOverride)
		plan = "explicit override"
	}
	if allowance == 0 {
		fmt.Fprintf(s.output, "\nActions quota:\n  Account: %s\n  Owner type: %s\n  Used: %.2f min\n  Included quota: unavailable\n", account, kind, used)
		return quota.ErrQuotaUnavailable
	}
	usage, err := quota.Calculate(used, allowance, quota.DefaultThreshold)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.output, "\nActions quota:\n  Account: %s\n  Owner type: %s\n  Used:    %.2f / %g min  ( %.2f%% )\n  Plan:    %s\n",
		account, kind, usage.UsedMinutes, allowance, usage.UsagePercent, displayPlan(plan))
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
