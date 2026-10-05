package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/philippwallrafen/gh-actions-quota/internal/quota"
)

const secretName = "ACTIONS_QUOTA_TOKEN"

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`)
var accountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Run configures quota checks for private repositories; public repositories need no setup.
func Run(ctx context.Context, input io.Reader, output io.Writer) error {
	root, err := os.Getwd()
	if err != nil {
		return errors.New("could not resolve the current working directory")
	}
	s := newSetup(input, output)
	s.root = root
	return s.run(ctx)
}

func newSetup(input io.Reader, output io.Writer) *setup {
	return &setup{
		gh:          ghRunner{},
		client:      newClient(),
		input:       input,
		output:      output,
		browser:     openBrowser,
		clipboard:   copyToClipboard,
		credentials: newCredentialStore(),
	}
}

type setup struct {
	gh          runner
	client      *client
	input       io.Reader
	output      io.Writer
	browser     func(context.Context, string) error
	clipboard   func(context.Context, string) error
	credentials credentialStore
	root        string
}

func (s *setup) run(ctx context.Context) error {
	if _, err := s.gh.Run(ctx, []string{"--version"}, nil); err != nil {
		return errors.New("install GitHub CLI (gh) before running setup")
	}
	data, err := s.gh.Run(ctx, []string{"repo", "view", "--json", "nameWithOwner,url,isPrivate"}, nil)
	if err != nil {
		return errors.New("could not resolve the current repository; run setup from its checkout")
	}
	var repo struct {
		Name    string `json:"nameWithOwner"`
		URL     string `json:"url"`
		Private *bool  `json:"isPrivate"`
	}
	if json.Unmarshal(data, &repo) != nil || !repositoryPattern.MatchString(repo.Name) || repo.URL != "https://github.com/"+repo.Name || repo.Private == nil {
		return errors.New("setup requires a current repository hosted on github.com")
	}
	if !*repo.Private {
		fmt.Fprintf(s.output, "Repository: %s\nVisibility: Public (unmetered)\n\nSetup is not required for public repositories.\n", repo.Name)
		return nil
	}
	owner, _, _ := strings.Cut(repo.Name, "/")
	if _, err := s.gh.Run(ctx, []string{"auth", "status", "--hostname", "github.com"}, nil); err != nil {
		return errors.New("authenticate GitHub CLI first with gh auth login --hostname github.com")
	}
	if err := s.checkOwner(ctx, owner); err != nil {
		return err
	}

	fmt.Fprintf(s.output, "Repository: %s\n", repo.Name)
	created, err := ensureReusableWorkflow(s.root)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(s.output, "Created %s\n", workflowPath)
	} else {
		fmt.Fprintf(s.output, "%s already exists\n", workflowPath)
	}

	fmt.Fprintln(s.output, "Private repository: requesting gh-actions-quota Account Plan read access...")

	token, plan, quota, err := s.authorizationForOwner(ctx, owner)
	if err != nil {
		return err
	}
	used, err := s.client.checkBilling(ctx, owner, token, "user")
	if err != nil {
		return err
	}
	// Token reaches gh only through this pipe, never through argv or command output.
	if _, err := s.gh.Run(ctx, []string{"secret", "set", secretName, "--repo", repo.Name}, strings.NewReader(token)); err != nil {
		return errors.New("could not store ACTIONS_QUOTA_TOKEN; check your local gh login and repository secret write access, then run setup again")
	}
	fmt.Fprintf(s.output, "\nPlan: %s\nActions usage: %.2f / %d Linux-equivalent minutes\nStored repository secret: %s\n", plan, used, quota, secretName)

	if err := initializeWorkflows(s.root, s.input, s.output); err != nil {
		return err
	}

	fmt.Fprintln(s.output, "\nQuota setup complete. Jobs without quota conditions must be configured manually if you want to gate them.")
	return nil
}

func (s *setup) checkOwner(ctx context.Context, owner string) error {
	ownerType, err := s.gh.Run(ctx, []string{"api", "users/" + owner, "--hostname", "github.com", "--jq", ".type"}, nil)
	if err != nil {
		return errors.New("could not determine the repository owner account type")
	}
	if strings.EqualFold(strings.TrimSpace(string(ownerType)), "Organization") {
		return errors.New("organization-owned private repositories are not supported in v1; gh-actions-quota only requests personal Account Plan read access")
	}
	if !strings.EqualFold(strings.TrimSpace(string(ownerType)), "User") {
		return errors.New("unsupported repository owner account type")
	}
	return nil
}

func (s *setup) authorize(ctx context.Context, copyCode bool) (string, error) {
	device, err := s.client.requestDeviceCode(ctx)
	if err != nil {
		return "", err
	}

	clipboardCtx, clipboardCancel := context.WithTimeout(ctx, 2*time.Second)
	copied := copyCode && s.clipboard != nil && s.clipboard(clipboardCtx, device.UserCode) == nil
	clipboardCancel()
	fmt.Fprintf(s.output, "\nOpen: %s\n", device.VerificationURI)
	if copied {
		fmt.Fprintf(s.output, "Code copied to clipboard: %s\n", device.UserCode)
	} else {
		fmt.Fprintf(s.output, "Code: %s\n", device.UserCode)
	}

	browserCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if s.browser(browserCtx, device.VerificationURI) != nil {
		fmt.Fprintln(s.output, "Open the URL in your browser to continue.")
	}
	cancel()
	fmt.Fprintln(s.output, "Waiting for GitHub authorization...")
	return s.client.pollToken(ctx, device)
}

func (c *client) quotaClient() *quota.Client {
	return &quota.Client{HTTP: c.http, APIBase: c.apiBase, Now: c.now}
}

func (c *client) checkAccount(ctx context.Context, owner, token string) (string, int, error) {
	return c.quotaClient().CheckAccount(ctx, owner, token)
}

func (c *client) checkBilling(ctx context.Context, owner, token, ownerType string) (float64, error) {
	return c.quotaClient().UsedMinutes(ctx, owner, token, ownerType)
}
