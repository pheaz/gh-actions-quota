package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Status reports account quota usage without changing repository files or secrets.
// Fresh authorization is persisted in the OS credential store.
func Status(ctx context.Context, input io.Reader, output io.Writer) error {
	return newSetup(input, output).status(ctx)
}

func (s *setup) status(ctx context.Context) error {
	data, err := s.gh.Run(ctx, []string{"repo", "view", "--json", "nameWithOwner,isPrivate"}, nil)
	if err != nil {
		return errors.New("could not resolve the current repository; run status from its checkout")
	}
	var repo struct {
		Name    string `json:"nameWithOwner"`
		Private *bool  `json:"isPrivate"`
	}
	if json.Unmarshal(data, &repo) != nil || !repositoryPattern.MatchString(repo.Name) || repo.Private == nil {
		return errors.New("status requires a current repository hosted on github.com")
	}
	fmt.Fprintf(s.output, "Repository: %s\n", repo.Name)
	var account string
	if *repo.Private {
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
	token, plan, quota, err := s.authorizationForOwner(ctx, account)
	if err != nil {
		return err
	}
	used, err := s.client.checkBilling(ctx, account, token)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.output, "\nActions quota:\n  Account: %s\n  Plan:    %s\n  Used:    %.2f / %d min  ( %.2f%% )\n",
		account, displayPlan(plan), used, quota, used/float64(quota)*100)
	return nil
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
		return "", errors.New("status requires a supported personal GitHub account signed in with gh on github.com")
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
