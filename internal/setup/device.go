package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type deviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken           string          `json:"access_token"`
	TokenType             string          `json:"token_type"`
	Scope                 string          `json:"scope"`
	ExpiresIn             json.RawMessage `json:"expires_in"`
	RefreshToken          json.RawMessage `json:"refresh_token"`
	RefreshTokenExpiresIn json.RawMessage `json:"refresh_token_expires_in"`
	Error                 string          `json:"error"`
	Interval              int             `json:"interval"`
}

func (c *client) requestDeviceCode(ctx context.Context) (deviceCode, error) {
	var device deviceCode
	err := c.request(ctx, http.MethodPost, c.oauthBase+"/login/device/code", "", url.Values{"client_id": {clientID}}, &device)
	if err != nil {
		return deviceCode{}, err
	}
	if device.DeviceCode == "" || !regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`).MatchString(device.UserCode) ||
		device.VerificationURI != "https://github.com/login/device" || device.ExpiresIn <= 0 || device.ExpiresIn > 3600 ||
		device.Interval < 0 || device.Interval > device.ExpiresIn {
		return deviceCode{}, errors.New("invalid device authorization response; check that Device Flow is enabled for actions-quota")
	}
	if device.Interval == 0 {
		device.Interval = 5
	}
	return device, nil
}

func validateToken(response tokenResponse) (string, error) {
	// Presence matters: even expires_in: 0 or null must be rejected.
	if len(response.ExpiresIn) != 0 || len(response.RefreshToken) != 0 || len(response.RefreshTokenExpiresIn) != 0 {
		return "", errors.New("the GitHub App issued an expiring user token; disable user-to-server token expiration for actions-quota and run setup again")
	}
	if response.Error != "" || !strings.HasPrefix(response.AccessToken, "ghu_") || len(response.AccessToken) <= 4 ||
		strings.ContainsAny(response.AccessToken, " \t\r\n") || !strings.EqualFold(response.TokenType, "bearer") || response.Scope != "" {
		return "", errors.New("GitHub did not issue a valid GitHub App user access token")
	}
	return response.AccessToken, nil
}

func (c *client) pollToken(ctx context.Context, device deviceCode) (string, error) {
	deadline := c.now().Add(time.Duration(device.ExpiresIn) * time.Second)
	interval := time.Duration(device.Interval) * time.Second
	for {
		if !c.now().Add(interval).Before(deadline) {
			return "", errors.New("device authorization expired; run setup again")
		}
		if err := c.sleep(ctx, interval); err != nil {
			return "", errors.New("authorization canceled")
		}
		if !c.now().Before(deadline) {
			return "", errors.New("device authorization expired; run setup again")
		}
		pollCtx, cancel := context.WithDeadline(ctx, deadline)
		var response tokenResponse
		err := c.request(pollCtx, http.MethodPost, c.oauthBase+"/login/oauth/access_token", "", url.Values{
			"client_id": {clientID}, "device_code": {device.DeviceCode},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &response)
		cancel()
		if err != nil {
			return "", err
		}
		if response.AccessToken != "" {
			return validateToken(response)
		}
		switch response.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			// GitHub returns the new minimum, not an increment.
			if response.Interval > 0 && response.Interval <= device.ExpiresIn && time.Duration(response.Interval)*time.Second > interval {
				interval = time.Duration(response.Interval) * time.Second
			}
		case "access_denied":
			return "", errors.New("GitHub authorization was denied; run setup again to retry")
		case "expired_token", "token_expired":
			return "", errors.New("device authorization expired; run setup again")
		case "device_flow_disabled":
			return "", errors.New("enable Device Flow for the actions-quota GitHub App")
		case "incorrect_client_credentials":
			return "", errors.New("GitHub rejected the actions-quota client ID")
		default:
			return "", errors.New("GitHub device authorization failed; run setup again")
		}
	}
}
