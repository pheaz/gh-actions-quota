package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const secretName = "ACTIONS_QUOTA_TOKEN"

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`)

// Run authorizes the app, writes the repository secret, and installs the reusable quota workflow.
func Run(ctx context.Context, input io.Reader, output io.Writer, initialize bool) error {
	root, err := os.Getwd()
	if err != nil {
		return errors.New("could not resolve the current working directory")
	}
	return (&setup{
		gh:         ghRunner{},
		client:     newClient(),
		input:      input,
		output:     output,
		browser:    openBrowser,
		clipboard:  copyToClipboard,
		root:       root,
		initialize: initialize,
	}).run(ctx)
}

type setup struct {
	gh         runner
	client     *client
	input      io.Reader
	output     io.Writer
	browser    func(context.Context, string) error
	clipboard  func(context.Context, string) error
	root       string
	initialize bool
}

func (s *setup) run(ctx context.Context) error {
	if _, err := s.gh.Run(ctx, []string{"--version"}, nil); err != nil {
		return errors.New("install GitHub CLI (gh) before running setup")
	}
	if _, err := s.gh.Run(ctx, []string{"auth", "status", "--hostname", "github.com"}, nil); err != nil {
		return errors.New("authenticate GitHub CLI first with gh auth login --hostname github.com")
	}
	data, err := s.gh.Run(ctx, []string{"repo", "view", "--json", "nameWithOwner,url"}, nil)
	if err != nil {
		return errors.New("could not resolve the current repository; run setup from its checkout")
	}
	var repo struct {
		Name string `json:"nameWithOwner"`
		URL  string `json:"url"`
	}
	if json.Unmarshal(data, &repo) != nil || !repositoryPattern.MatchString(repo.Name) || repo.URL != "https://github.com/"+repo.Name {
		return errors.New("setup requires a current repository hosted on github.com")
	}
	owner, _, _ := strings.Cut(repo.Name, "/")
	ownerType, err := s.gh.Run(ctx, []string{"api", "users/" + owner, "--hostname", "github.com", "--jq", ".type"}, nil)
	if err != nil {
		return errors.New("could not determine the repository owner account type")
	}
	if strings.EqualFold(strings.TrimSpace(string(ownerType)), "Organization") {
		return errors.New("organization-owned repositories are not supported in v1; actions-quota only requests personal Account Plan read access")
	}
	if !strings.EqualFold(strings.TrimSpace(string(ownerType)), "User") {
		return errors.New("unsupported repository owner account type")
	}

	fmt.Fprintf(s.output, "Repository: %s\n", repo.Name)
	created, err := ensureReusableWorkflow(s.root)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(s.output, "Created reusable workflow: %s\n", workflowPath)
	} else {
		fmt.Fprintf(s.output, "Reusable workflow already current: %s\n", workflowPath)
	}
	fmt.Fprintln(s.output, "Requesting actions-quota Account Plan read access...")

	device, err := s.client.requestDeviceCode(ctx)
	if err != nil {
		return err
	}

	clipboardCtx, clipboardCancel := context.WithTimeout(ctx, 2*time.Second)
	copied := s.clipboard != nil && s.clipboard(clipboardCtx, device.UserCode) == nil
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
	token, err := s.client.pollToken(ctx, device)
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
	// Token exists only in memory and this pipe, never in argv or a local file.
	if _, err := s.gh.Run(ctx, []string{"secret", "set", secretName, "--repo", repo.Name}, strings.NewReader(token)); err != nil {
		return errors.New("could not store ACTIONS_QUOTA_TOKEN; check your local gh login and repository secret write access, then run setup again")
	}
	fmt.Fprintf(s.output, "\nPlan: %s\nActions usage: %.2f / %d Linux-equivalent minutes\nStored repository secret: %s\n", plan, used, quota, secretName)

	if s.initialize {
		if err := initializeWorkflows(s.root, s.input, s.output); err != nil {
			return err
		}
	}

	fmt.Fprintln(s.output, `
Gate each expensive job with:

needs: quota
if: needs.quota.outputs.allowed == 'true'

Run "gh actions-quota setup --init" to interactively add the quota caller to existing workflow files.`)
	return nil
}

func (c *client) checkAccount(ctx context.Context, owner, token string) (string, int, error) {
	var account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
		Plan  struct {
			Name string `json:"name"`
		} `json:"plan"`
	}
	if err := c.request(ctx, http.MethodGet, c.apiBase+"/user", token, nil, &account); err != nil {
		return "", 0, err
	}
	if !strings.EqualFold(account.Login, owner) || !strings.EqualFold(account.Type, "User") {
		return "", 0, errors.New("the authorized personal GitHub account must be the repository owner; run setup again and authorize as the owner")
	}
	plan := strings.ToLower(strings.TrimSpace(account.Plan.Name))
	quota := map[string]int{"free": 2000, "pro": 3000, "team": 3000, "enterprise": 50000, "enterprise cloud": 50000}[plan]
	if quota == 0 {
		return "", 0, errors.New("GitHub did not return a supported account plan; check Account Plan read access")
	}
	return plan, quota, nil
}

func (c *client) checkBilling(ctx context.Context, owner, token string) (float64, error) {
	now := c.now().UTC()
	query := url.Values{"year": {fmt.Sprint(now.Year())}, "month": {fmt.Sprint(int(now.Month()))}, "product": {"Actions"}}
	var report struct {
		UsageItems json.RawMessage `json:"usageItems"`
	}
	if err := c.request(ctx, http.MethodGet, c.apiBase+"/users/"+url.PathEscape(owner)+"/settings/billing/usage/summary?"+query.Encode(), token, nil, &report); err != nil {
		return 0, err
	}
	var items []struct {
		Product        *string  `json:"product"`
		UnitType       *string  `json:"unitType"`
		DiscountAmount *float64 `json:"discountAmount"`
	}
	if len(report.UsageItems) == 0 || json.Unmarshal(report.UsageItems, &items) != nil || items == nil {
		return 0, errors.New("GitHub billing response must contain usageItems")
	}
	var discount float64
	for _, item := range items {
		if item.Product == nil || item.UnitType == nil {
			return 0, errors.New("GitHub billing usage item is invalid")
		}
		if *item.Product == "Actions" && *item.UnitType == "minutes" {
			if item.DiscountAmount == nil || *item.DiscountAmount < 0 {
				return 0, errors.New("GitHub billing discountAmount is invalid")
			}
			discount += *item.DiscountAmount
		}
	}
	used := discount / 0.006
	if math.IsInf(used, 0) || math.IsNaN(used) {
		return 0, errors.New("GitHub billing usage is out of range")
	}
	return used, nil
}
