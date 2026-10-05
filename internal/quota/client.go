package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const APIVersion = "2026-03-10"
const maxResponseBytes = 4 << 20

var ErrAccountMismatch = errors.New("the authorized personal GitHub account must match the quota account; run the command again and authorize as that account")

// HTTPError contains only a status code, never a response body or credentials.
type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("GitHub returned HTTP %d; check authorization and Plan read access", e.Status)
}

type Client struct {
	HTTP    *http.Client
	APIBase string
	Now     func() time.Time
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not allowed")
		}},
		APIBase: "https://api.github.com",
		Now:     time.Now,
	}
}

func (c *Client) request(ctx context.Context, endpoint, token string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("could not construct GitHub request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("User-Agent", "gh-actions-quota")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("GitHub API request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &HTTPError{Status: response.StatusCode}
	}
	// Billing usage currently has no documented pagination parameters. Refuse
	// a partial report if GitHub starts advertising another page.
	if strings.Contains(response.Header.Get("Link"), `rel="next"`) {
		return errors.New("GitHub returned a paginated response; complete billing usage cannot be determined safely")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, result) != nil {
		return errors.New("GitHub returned an invalid response")
	}
	return nil
}

func (c *Client) OwnerType(ctx context.Context, owner, token string) (string, error) {
	var account struct {
		Type string `json:"type"`
	}
	if err := c.request(ctx, c.APIBase+"/users/"+url.PathEscape(owner), token, &account); err != nil {
		return "", err
	}
	kind := strings.ToLower(strings.TrimSpace(account.Type))
	if kind != "user" && kind != "organization" {
		return "", errors.New("unsupported GitHub owner type")
	}
	return kind, nil
}
