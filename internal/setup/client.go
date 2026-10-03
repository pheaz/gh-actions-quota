package setup

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

// The client ID is public. Device authorization needs no client secret.
const clientID = "Iv23liXk29OIBFBTJjap"
const apiVersion = "2026-03-10"

const maxResponseBytes = 4 << 20

type githubHTTPError struct {
	status int
}

func (e *githubHTTPError) Error() string {
	return fmt.Sprintf("GitHub returned HTTP %d; check authorization and Plan read access", e.status)
}

type client struct {
	http      *http.Client
	oauthBase string
	apiBase   string
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error
}

func newClient() *client {
	return &client{
		http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not allowed")
		}},
		oauthBase: "https://github.com",
		apiBase:   "https://api.github.com",
		now:       time.Now,
		sleep: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return errors.New("authorization canceled")
			case <-timer.C:
				return nil
			}
		},
	}
}

func (c *client) request(ctx context.Context, method, endpoint, token string, form url.Values, result any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("could not construct GitHub request")
	}
	req.Header.Set("User-Agent", "gh-actions-quota")
	if form != nil {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", apiVersion)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(req)
	if err != nil {
		// Transport errors and response bodies can contain credentials. Never wrap them.
		return errors.New("GitHub request failed; check your connection and retry setup")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &githubHTTPError{status: response.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, result) != nil {
		return errors.New("GitHub returned an invalid response")
	}
	return nil
}
