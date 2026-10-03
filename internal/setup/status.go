package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// Status reports quota usage without writing secrets, files, or local credentials.
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
	if !*repo.Private {
		fmt.Fprintln(s.output, "Visibility: public\nActions quota: unmetered")
		return nil
	}
	fmt.Fprintln(s.output, "Visibility: private")
	owner, _, _ := strings.Cut(repo.Name, "/")
	if err := s.checkOwner(ctx, owner); err != nil {
		return err
	}
	token, err := s.authorize(ctx, false)
	if err != nil {
		return err
	}
	plan, quota, err := s.client.checkAccount(ctx, owner, token)
	if err != nil {
		return err
	}
	used, err := s.client.checkBilling(ctx, owner, token)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.output, "\nPlan: %s\n\nActions quota:\n  Used:       %.2f / %d min\n  Remaining: %.2f min\n  Usage:      %.2f%%\n",
		plan, used, quota, math.Max(0, float64(quota)-used), used/float64(quota)*100)
	return nil
}
