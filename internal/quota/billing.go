// Package quota owns the shared account, billing and quota rules for the CLI and Action.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const linuxBasePriceUSD = 0.006

var skuSeparators = regexp.MustCompile(`[\s-]+`)

// Normative calculation contract: spec/quota-calculation.md.
// Quota usage is normalized to the standard Linux 2-core rate:
// effective minutes = runtime minutes × (SKU price / $0.006).
// Keep prices and the corresponding Linux-equivalent factors visible here
// because they define the quota estimate.
var skuPricePerMinuteUSD = map[string]float64{
	"actions_linux_slim":  0.002, // 0.3333× Linux
	"actions_linux_arm":   0.005, // 0.8333× Linux
	"actions_linux":       0.006, // 1.0000× Linux
	"actions_windows":     0.010, // 1.6667× Linux
	"actions_windows_arm": 0.010, // 1.6667× Linux
	"actions_macos":       0.062, // 10.3333× Linux
}

func normalizeSKU(sku string) string {
	normalized := skuSeparators.ReplaceAllString(strings.ToLower(strings.TrimSpace(sku)), "_")
	// Standard macOS runners have 3 or 4 cores; larger core counts stay excluded.
	if normalized == "actions_macos_3_core" || normalized == "actions_macos_4_core" {
		return "actions_macos"
	}
	// Match only exact standard names; never let generic Linux/Windows matches
	// absorb Slim/ARM variants or larger-runner core-count suffixes.
	return normalized
}

func (c *Client) CheckAccount(ctx context.Context, owner, token string) (string, int, error) {
	var account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
		Plan  struct {
			Name string `json:"name"`
		} `json:"plan"`
	}
	if err := c.request(ctx, c.APIBase+"/user", token, &account); err != nil {
		return "", 0, err
	}
	if !strings.EqualFold(account.Login, owner) || !strings.EqualFold(account.Type, "User") {
		return "", 0, ErrAccountMismatch
	}
	plan := strings.ToLower(strings.TrimSpace(account.Plan.Name))
	quota, err := IncludedMinutesForPlan(plan)
	if err != nil {
		return "", 0, errors.New("GitHub did not return a supported account plan; check Account Plan read access")
	}
	return plan, quota, nil
}

func (c *Client) UsedMinutes(ctx context.Context, owner, token, ownerType string) (float64, error) {
	now := c.Now().UTC()
	endpoint := "users"
	switch ownerType {
	case "organization":
		endpoint = "organizations"
	case "user":
	default:
		return 0, errors.New("unsupported GitHub owner type")
	}
	query := url.Values{"year": {fmt.Sprint(now.Year())}, "month": {fmt.Sprint(int(now.Month()))}, "product": {"Actions"}}
	var report struct {
		UsageItems json.RawMessage `json:"usageItems"`
	}
	if err := c.request(ctx, c.APIBase+"/"+endpoint+"/"+url.PathEscape(owner)+"/settings/billing/usage?"+query.Encode(), token, &report); err != nil {
		return 0, err
	}
	var items []*struct {
		Product        any             `json:"product"`
		UnitType       any             `json:"unitType"`
		SKU            any             `json:"sku"`
		RepositoryName any             `json:"repositoryName"`
		Quantity       json.RawMessage `json:"quantity"`
	}
	if len(report.UsageItems) == 0 || json.Unmarshal(report.UsageItems, &items) != nil || items == nil {
		return 0, errors.New("GitHub billing response must contain usageItems")
	}
	// Visibility is current; historical public/private changes are not reflected here.
	privateRepositories := make(map[string]bool)
	var usedMinutes float64
	for _, item := range items {
		if item == nil {
			return 0, errors.New("GitHub billing usage item is invalid")
		}
		sku, _ := item.SKU.(string)
		product, _ := item.Product.(string)
		unitType, _ := item.UnitType.(string)
		skuPrice, standardRunner := skuPricePerMinuteUSD[normalizeSKU(sku)]
		repository, hasRepository := item.RepositoryName.(string)
		if !strings.EqualFold(strings.TrimSpace(product), "Actions") || !strings.EqualFold(strings.TrimSpace(unitType), "Minutes") || !standardRunner || !hasRepository {
			continue
		}
		private, cached := privateRepositories[repository]
		if !cached {
			var err error
			private, err = c.isPrivateRepository(ctx, repository, token)
			if err != nil {
				return 0, err
			}
			privateRepositories[repository] = private
		}
		if !private {
			continue
		}
		var quantity *float64
		if json.Unmarshal(item.Quantity, &quantity) != nil || quantity == nil || math.IsInf(*quantity, 0) || math.IsNaN(*quantity) || *quantity < 0 {
			return 0, errors.New("Invalid quantity")
		}
		factor := skuPrice / linuxBasePriceUSD
		usedMinutes += *quantity * factor
	}
	if math.IsInf(usedMinutes, 0) || math.IsNaN(usedMinutes) {
		return 0, errors.New("GitHub billing usage is out of range")
	}
	return usedMinutes, nil
}

func (c *Client) isPrivateRepository(ctx context.Context, repository, token string) (bool, error) {
	parts := strings.SplitN(repository, "/", 3)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false, nil
	}
	var repo *struct {
		Private json.RawMessage `json:"private"`
	}
	err := c.request(ctx, c.APIBase+"/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), token, &repo)
	if err != nil {
		var httpError *HTTPError
		if errors.As(err, &httpError) && httpError.Status == http.StatusNotFound {
			return false, nil
		}
		return false, errors.New("GitHub repository lookup failed")
	}
	if repo == nil {
		return false, errors.New("GitHub returned an invalid response")
	}
	return strings.TrimSpace(string(repo.Private)) == "true", nil
}
