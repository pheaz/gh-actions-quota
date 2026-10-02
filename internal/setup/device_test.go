package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const fakeToken = "ghu_test_only_not_a_real_token"
const deviceJSON = `{"device_code":"test-device","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`

func testClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := newClient()
	c.oauthBase, c.apiBase = server.URL, server.URL
	now := time.Now()
	c.now = func() time.Time { return now }
	c.sleep = func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil }
	return c
}

func TestRequestDeviceCode(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/login/device/code" {
			t.Error("wrong device request")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_id") != clientID || len(r.Form) != 1 {
			t.Error("device request must only send public client ID")
		}
		io.WriteString(w, deviceJSON)
	})
	device, err := c.requestDeviceCode(context.Background())
	if err != nil || device.UserCode != "ABCD-EFGH" || device.Interval != 5 {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestInvalidDeviceResponses(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `not json`, `{"error":"device_flow_disabled"}`,
		strings.Replace(deviceJSON, "https://github.com/login/device", "https://example.com", 1),
		strings.Replace(deviceJSON, `"expires_in":900`, `"expires_in":-1`, 1),
		strings.Replace(deviceJSON, `"interval":5`, `"interval":-1`, 1)} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
			if _, err := c.requestDeviceCode(context.Background()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPollingPendingSlowDownThenToken(t *testing.T) {
	responses := []string{`{"error":"authorization_pending"}`, `{"error":"slow_down","interval":10}`, `{"error":"authorization_pending"}`, `{"access_token":"` + fakeToken + `","token_type":"bearer","scope":""}`}
	index := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" || r.Method != http.MethodPost {
			t.Error("wrong polling request")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_id") != clientID || r.Form.Get("device_code") != "test-device" ||
			r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || len(r.Form) != 3 {
			t.Error("wrong polling form")
		}
		io.WriteString(w, responses[index])
		index++
	})
	var delays []time.Duration
	sleep := c.sleep
	c.sleep = func(ctx context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return sleep(ctx, delay)
	}
	token, err := c.pollToken(context.Background(), deviceCode{DeviceCode: "test-device", ExpiresIn: 900, Interval: 5})
	if err != nil || token != fakeToken {
		t.Fatalf("poll failed: %v", err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	if len(delays) != len(want) {
		t.Fatal("wrong number of polls")
	}
	for i := range want {
		if want[i] != delays[i] {
			t.Errorf("poll %d delay = %s", i, delays[i])
		}
	}
}

func TestTokenValidation(t *testing.T) {
	for _, extra := range []string{``, `,"expires_in":28800`, `,"expires_in":0`, `,"expires_in":null`,
		`,"refresh_token":"test-only-refresh"`, `,"refresh_token":null`, `,"refresh_token_expires_in":0`} {
		t.Run(extra, func(t *testing.T) {
			var response tokenResponse
			if err := json.Unmarshal([]byte(`{"access_token":"`+fakeToken+`","token_type":"bearer"`+extra+`}`), &response); err != nil {
				t.Fatal(err)
			}
			token, err := validateToken(response)
			if extra == "" {
				if err != nil || token != fakeToken {
					t.Fatal("valid token rejected")
				}
				return
			}
			if err == nil || token != "" {
				t.Fatal("expiring token accepted")
			}
			if strings.Contains(err.Error(), fakeToken) {
				t.Fatal("token leaked in diagnostic")
			}
		})
	}
	for _, response := range []tokenResponse{{}, {AccessToken: "gho_test_only", TokenType: "bearer"}, {AccessToken: fakeToken, TokenType: "other"},
		{AccessToken: fakeToken, TokenType: "bearer", Scope: "repo"}, {AccessToken: fakeToken + "\n", TokenType: "bearer"}} {
		if _, err := validateToken(response); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}

func TestPollingErrors(t *testing.T) {
	for _, code := range []string{"access_denied", "expired_token", "token_expired", "device_flow_disabled", "incorrect_client_credentials", "incorrect_device_code", "unsupported_grant_type", ""} {
		t.Run(code, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": fakeToken})
			})
			token, err := c.pollToken(context.Background(), deviceCode{DeviceCode: "test", ExpiresIn: 900, Interval: 5})
			if err == nil || token != "" {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), fakeToken) {
				t.Fatal("error_description leaked")
			}
		})
	}
}

func TestPollingExpirationAndCancellation(t *testing.T) {
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		io.WriteString(w, `{"error":"authorization_pending"}`)
	})
	if _, err := c.pollToken(context.Background(), deviceCode{ExpiresIn: 10, Interval: 5}); err == nil || requests != 1 {
		t.Fatal("poll continued beyond expiry")
	}
	c.sleep = func(context.Context, time.Duration) error { return errors.New(fakeToken) }
	if _, err := c.pollToken(context.Background(), deviceCode{ExpiresIn: 900, Interval: 5}); err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatal("cancellation not sanitized")
	}
}

func TestHTTPFailuresDoNotExposeResponseBody(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); io.WriteString(w, fakeToken) })
		if _, err := c.requestDeviceCode(context.Background()); err == nil || strings.Contains(err.Error(), fakeToken) {
			t.Fatal("HTTP error not sanitized")
		}
	}
}

func TestRedirectRefused(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/?token="+url.QueryEscape(fakeToken), http.StatusFound)
	})
	if _, _, err := c.checkAccount(context.Background(), "owner", fakeToken); err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatal("redirect was not safely refused")
	}
}
