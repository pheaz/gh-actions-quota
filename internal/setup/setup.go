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
	return RunWithQuota(ctx, input, output, "")
}

// RunWithQuota permits an explicit allowance when GitHub cannot expose one.
func RunWithQuota(ctx context.Context, input io.Reader, output io.Writer, included string) error {
	root, err := os.Getwd()
	if err != nil {
		return errors.New("could not resolve the current working directory")
	}
	s := newSetup(input, output)
	s.root = root
	s.quotaOverride = included
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
	gh            runner
	client        *client
	input         io.Reader
	output        io.Writer
	browser       func(context.Context, string) error
	clipboard     func(context.Context, string) error
	credentials   credentialStore
	root          string
	quotaOverride string
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
	kind, err := s.checkOwner(ctx, owner)
	if err != nil {
		return err
	}
	if s.quotaOverride != "" {
		if _, err := quota.ParsePositive(s.quotaOverride); err != nil {
			return err
		}
	} else {
		s.quotaOverride, err = workflowQuotaOverride(s.root)
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(s.output, "Repository: %s\n", repo.Name)
	created, err := ensureReusableWorkflowWithQuota(s.root, s.quotaOverride)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(s.output, "Created %s\n", workflowPath)
	} else {
		fmt.Fprintf(s.output, "%s already exists\n", workflowPath)
	}

	fmt.Fprintf(s.output, "Account: %s\nOwner type: %s\n", owner, kind)
	if kind == "organization" {
		fmt.Fprintln(s.output, "Organization access requires an owner-approved App installation with Administration read and Organization plan read access; authorize as a user with organization billing privileges.")
	} else {
		fmt.Fprintln(s.output, "Private repository: requesting gh-actions-quota Account Plan read access...")
	}

	token, plan, included, err := s.authorizationForOwner(ctx, owner, kind)
	if err != nil {
		return err
	}
	used, err := s.client.checkBilling(ctx, owner, token, kind)
	if err != nil {
		return err
	}
	allowance := float64(included)
	if s.quotaOverride != "" {
		allowance, _ = quota.ParsePositive(s.quotaOverride)
		plan = "explicit override"
	}
	if allowance == 0 {
		fmt.Fprintf(s.output, "Actions usage obtained: %.2f Linux-equivalent minutes\nIncluded quota: unavailable\n", used)
		return quota.ErrQuotaUnavailable
	}
	// Token reaches gh only through this pipe, never through argv or command output.
	if _, err := s.gh.Run(ctx, []string{"secret", "set", secretName, "--repo", repo.Name}, strings.NewReader(token)); err != nil {
		return errors.New("could not store ACTIONS_QUOTA_TOKEN; check your local gh login and repository secret write access, then run setup again")
	}
	fmt.Fprintf(s.output, "\nPlan: %s\nActions usage: %.2f / %g Linux-equivalent minutes\nStored repository secret: %s\n", plan, used, allowance, secretName)

	if err := initializeWorkflows(s.root, s.input, s.output); err != nil {
		return err
	}

	fmt.Fprintln(s.output, "\nQuota setup complete. Jobs without quota conditions must be configured manually if you want to gate them.")
	return nil
}

func (s *setup) checkOwner(ctx context.Context, owner string) (string, error) {
	ownerType, err := s.gh.Run(ctx, []string{"api", "users/" + owner, "--hostname", "github.com", "--jq", ".type"}, nil)
	if err != nil {
		return "", errors.New("could not determine the repository owner account type")
	}
	kind := strings.ToLower(strings.TrimSpace(string(ownerType)))
	if kind != "user" && kind != "organization" {
		return "", errors.New("unsupported repository owner account type")
	}
	return kind, nil
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

func (c *client) checkBillingOwner(ctx context.Context, owner, token, kind string) (string, int, error) {
	if kind == "organization" {
		plan, included, err := c.quotaClient().CheckOrganization(ctx, owner, token)
		if errors.Is(err, quota.ErrQuotaUnavailable) {
			return plan, 0, nil
		}
		return plan, included, err
	}
	return c.checkAccount(ctx, owner, token)
}

func (c *client) checkAuthorization(ctx context.Context, owner, token, kind string) error {
	if kind == "organization" {
		_, err := c.checkBilling(ctx, owner, token, kind)
		return err
	}
	_, _, err := c.checkAccount(ctx, owner, token)
	return err
}
