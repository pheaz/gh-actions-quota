// Package action adapts GitHub Action inputs, outputs and summaries to quota.
package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/philippwallrafen/gh-actions-quota/internal/quota"
)

const unavailableMessage = "Billing usage unavailable. Check the token, account plan, billing access and action inputs."

type adapter struct {
	env    func(string) string
	output io.Writer
	client *quota.Client
}

// Run reports billing failures as allowed=false and exits successfully, matching
// the Action contract. Output/summary write failures return a sanitized error.
func Run(ctx context.Context, output io.Writer) error {
	return (&adapter{env: os.Getenv, output: output, client: quota.NewClient()}).run(ctx)
}

func (a *adapter) input(name string) string {
	key := "INPUT_" + strings.ToUpper(name)
	if value := a.env(key); value != "" {
		return value
	}
	return a.env(strings.ReplaceAll(key, "-", "_"))
}

func (a *adapter) publicRepository() bool {
	if a.env("GITHUB_REPOSITORY_VISIBILITY") == "public" {
		return true
	}
	data, err := os.ReadFile(a.env("GITHUB_EVENT_PATH"))
	if err != nil {
		return false
	}
	var event struct {
		Repository struct {
			Private *bool `json:"private"`
		} `json:"repository"`
	}
	return json.Unmarshal(data, &event) == nil && event.Repository.Private != nil && !*event.Repository.Private
}

type result struct {
	allowed, available, unmetered   bool
	used, quota, remaining, percent string
	owner, ownerType                string
}

func (a *adapter) run(ctx context.Context) error {
	r, summary, err := a.evaluate(ctx)
	if err != nil {
		fmt.Fprintf(a.output, "::warning title=Actions quota::%s\n", unavailableMessage)
		owner := a.env("GITHUB_REPOSITORY_OWNER")
		if owner == "" {
			owner = "unknown"
		}
		// Only echo a validated override; arbitrary invalid input can contain secrets.
		included := "unavailable"
		if value, parseErr := quota.ParsePositive(a.input("quota-minutes")); parseErr == nil {
			included = formatNumber(value)
		}
		r = result{used: "unavailable", quota: included, remaining: "unavailable", percent: "unavailable", owner: owner, ownerType: "unavailable"}
		summary = "## GitHub Actions quota\n\nUsage unavailable.\n\n" + unavailableMessage + "\n"
	}
	if err := a.writeResult(r); err != nil {
		return err
	}
	return a.appendFile("GITHUB_STEP_SUMMARY", summary)
}

func (a *adapter) evaluate(ctx context.Context) (result, string, error) {
	threshold, err := quota.ParseThreshold(a.input("threshold"))
	if err != nil {
		return result{}, "", err
	}
	owner := a.env("GITHUB_REPOSITORY_OWNER")
	if a.publicRepository() {
		if owner == "" {
			owner = "unknown"
		}
		return result{allowed: true, available: true, unmetered: true, used: "0", quota: "unmetered", remaining: "unmetered", percent: "0", owner: owner, ownerType: "unmetered"},
			"## GitHub Actions quota\n\nStandard GitHub-hosted runners are unmetered for this public repository.\n", nil
	}
	token := a.input("token")
	if token == "" {
		token = a.env("ACTIONS_QUOTA_TOKEN")
	}
	if owner == "" || token == "" {
		return result{}, "", errors.New("missing billing owner or token")
	}
	kind, err := a.client.OwnerType(ctx, owner, token)
	if err != nil {
		return result{}, "", err
	}
	if kind != "user" {
		return result{}, "", errors.New("organization billing is not supported in v1")
	}
	var included float64
	if explicit := a.input("quota-minutes"); explicit != "" {
		included, err = quota.ParsePositive(explicit)
	} else {
		var minutes int
		_, minutes, err = a.client.CheckAccount(ctx, owner, token)
		included = float64(minutes)
	}
	if err != nil {
		return result{}, "", err
	}
	used, err := a.client.UsedMinutes(ctx, owner, token, kind)
	if err != nil {
		return result{}, "", err
	}
	usage, err := quota.Calculate(used, included, threshold)
	if err != nil {
		return result{}, "", err
	}
	r := result{allowed: usage.Allowed, available: true, used: formatNumber(usage.UsedMinutes), quota: formatNumber(usage.QuotaMinutes), remaining: formatNumber(usage.RemainingMinutes), percent: formatNumber(usage.UsagePercent), owner: owner, ownerType: kind}
	summary := fmt.Sprintf("## GitHub Actions quota\n\nUsage: **%s / %s minutes (%s%%)**\n\nThreshold: **%s%%**\n\nAllowed: **%t**\n", r.used, r.quota, r.percent, formatNumber(threshold), r.allowed)
	return r, summary, nil
}

func formatNumber(value float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 6, 64), "0"), ".")
}

func (a *adapter) writeResult(r result) error {
	values := [][2]string{
		{"allowed", strconv.FormatBool(r.allowed)}, {"usage-available", strconv.FormatBool(r.available)},
		{"used-minutes", r.used}, {"quota-minutes", r.quota}, {"remaining-minutes", r.remaining},
		{"usage-percent", r.percent}, {"billing-owner", r.owner}, {"billing-owner-type", r.ownerType},
		{"unmetered", strconv.FormatBool(r.unmetered)},
	}
	var lines strings.Builder
	for _, value := range values {
		fmt.Fprintf(&lines, "%s=%s\n", value[0], strings.NewReplacer("\r", " ", "\n", " ").Replace(value[1]))
	}
	if a.env("GITHUB_OUTPUT") == "" {
		if _, err := io.WriteString(a.output, lines.String()); err != nil {
			return errors.New("could not write GitHub Action outputs")
		}
		return nil
	}
	return a.appendFile("GITHUB_OUTPUT", lines.String())
}

func (a *adapter) appendFile(key, text string) error {
	path := a.env(key)
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return errors.New("could not write GitHub Action output or summary")
	}
	_, writeErr := io.WriteString(file, text)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("could not write GitHub Action output or summary")
	}
	return nil
}
