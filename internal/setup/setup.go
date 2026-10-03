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
const linuxMinutePriceUSD = 0.006

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`)
var skuSeparators = regexp.MustCompile(`[\s-]+`)
var includedSKUs = map[string]bool{
	"actions_linux_slim":  true,
	"actions_linux":       true,
	"actions_linux_arm":   true,
	"actions_windows":     true,
	"actions_windows_arm": true,
	"actions_macos":       true,
}

func normalizeSKU(sku string) string {
	return skuSeparators.ReplaceAllString(strings.ToLower(strings.TrimSpace(sku)), "_")
}

// Run installs the reusable quota workflow, configures callers, and authorizes the app only when the repository needs a billing token.
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
		gh:        ghRunner{},
		client:    newClient(),
		input:     input,
		output:    output,
		browser:   openBrowser,
		clipboard: copyToClipboard,
	}
}

type setup struct {
	gh        runner
	client    *client
	input     io.Reader
	output    io.Writer
	browser   func(context.Context, string) error
	clipboard func(context.Context, string) error
	root      string
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
		Private bool   `json:"isPrivate"`
	}
	if json.Unmarshal(data, &repo) != nil || !repositoryPattern.MatchString(repo.Name) || repo.URL != "https://github.com/"+repo.Name {
		return errors.New("setup requires a current repository hosted on github.com")
	}
	owner, _, _ := strings.Cut(repo.Name, "/")
	if repo.Private {
		if _, err := s.gh.Run(ctx, []string{"auth", "status", "--hostname", "github.com"}, nil); err != nil {
			return errors.New("authenticate GitHub CLI first with gh auth login --hostname github.com")
		}
		if err := s.checkOwner(ctx, owner); err != nil {
			return err
		}
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

	if !repo.Private {
		fmt.Fprintf(s.output, "Public repository: %s is not required.\n", secretName)
		if err := initializeWorkflows(s.root, s.input, s.output); err != nil {
			return err
		}
		fmt.Fprintln(s.output, "\nQuota setup complete. Jobs without quota conditions must be configured manually if you want to gate them.")
		return nil
	}

	fmt.Fprintln(s.output, "Private repository: requesting gh-actions-quota Account Plan read access...")

	token, err := s.authorize(ctx, true)
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
	if err := c.request(ctx, http.MethodGet, c.apiBase+"/users/"+url.PathEscape(owner)+"/settings/billing/usage?"+query.Encode(), token, nil, &report); err != nil {
		return 0, err
	}
	var items []*struct {
		Product        any             `json:"product"`
		UnitType       any             `json:"unitType"`
		SKU            any             `json:"sku"`
		RepositoryName any             `json:"repositoryName"`
		DiscountAmount json.RawMessage `json:"discountAmount"`
	}
	if len(report.UsageItems) == 0 || json.Unmarshal(report.UsageItems, &items) != nil || items == nil {
		return 0, errors.New("GitHub billing response must contain usageItems")
	}
	publicRepositories := make(map[string]bool)
	var discountedUSD float64
	for _, item := range items {
		if item == nil {
			return 0, errors.New("GitHub billing usage item is invalid")
		}
		sku, _ := item.SKU.(string)
		repository, hasRepository := item.RepositoryName.(string)
		if item.Product != "Actions" || item.UnitType != "minutes" || !includedSKUs[normalizeSKU(sku)] || !hasRepository {
			continue
		}
		public, cached := publicRepositories[repository]
		if !cached {
			var err error
			public, err = c.isPublicRepository(ctx, repository, token)
			if err != nil {
				return 0, err
			}
			publicRepositories[repository] = public
		}
		if public {
			continue
		}
		var discount *float64
		if json.Unmarshal(item.DiscountAmount, &discount) != nil || discount == nil || math.IsInf(*discount, 0) || math.IsNaN(*discount) || *discount < 0 {
			return 0, errors.New("Invalid discountAmount")
		}
		discountedUSD += *discount
	}
	used := discountedUSD / linuxMinutePriceUSD
	if math.IsInf(used, 0) || math.IsNaN(used) {
		return 0, errors.New("GitHub billing usage is out of range")
	}
	return used, nil
}

func (c *client) isPublicRepository(ctx context.Context, repository, token string) (bool, error) {
	parts := strings.SplitN(repository, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return false, nil
	}
	var repo *struct {
		Private json.RawMessage `json:"private"`
	}
	err := c.request(ctx, http.MethodGet, c.apiBase+"/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), token, nil, &repo)
	if err != nil {
		var httpError *githubHTTPError
		if errors.As(err, &httpError) && httpError.status == http.StatusNotFound {
			return false, nil
		}
		return false, errors.New("GitHub repository lookup failed")
	}
	if repo == nil {
		return false, errors.New("GitHub returned an invalid response")
	}
	return strings.TrimSpace(string(repo.Private)) == "false", nil
}
