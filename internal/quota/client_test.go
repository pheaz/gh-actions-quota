package quota

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCurrentUTCYearAndMonth(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/owner/settings/billing/usage" || r.URL.Query().Get("year") != "2026" || r.URL.Query().Get("month") != "12" || r.URL.Query().Get("product") != "Actions" {
			t.Errorf("wrong detailed endpoint or UTC year/month: %s", r.URL)
		}
		io.WriteString(w, `{"usageItems":[]}`)
	})
	now, _ := time.Parse(time.RFC3339, "2027-01-01T00:30:00+02:00")
	c.Now = func() time.Time { return now }
	if used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user"); err != nil || used != 0 {
		t.Fatalf("used=%v err=%v", used, err)
	}
}

func TestOwnerType(t *testing.T) {
	for _, body := range []string{`{"type":"User"}`, `{"type":"Organization"}`, `{"type":"unknown"}`, `{}`, `null`, `[]`, fakeToken} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/users/owner" {
				t.Errorf("wrong owner endpoint: %s", r.URL)
			}
			io.WriteString(w, body)
		})
		kind, err := c.OwnerType(context.Background(), "owner", fakeToken)
		switch body {
		case `{"type":"User"}`:
			if err != nil || kind != "user" {
				t.Fatal(kind, err)
			}
		case `{"type":"Organization"}`:
			if err != nil || kind != "organization" {
				t.Fatal(kind, err)
			}
		default:
			if err == nil || strings.Contains(err.Error(), fakeToken) {
				t.Fatalf("unsafe owner failure: %v", err)
			}
		}
	}
}

func TestAccountIdentityAndPlan(t *testing.T) {
	for _, test := range []struct {
		body     string
		mismatch bool
		quota    int
	}{
		{`{"login":"OWNER","type":"User","plan":{"name":"PRO"}}`, false, 3000},
		{`{"login":"someone-else","type":"User","plan":{"name":"pro"}}`, true, 0},
		{`{"login":"owner","type":"Organization","plan":{"name":"pro"}}`, true, 0},
		{`{"login":"owner","type":"User","plan":{"name":"unknown"}}`, false, 0},
		{`{"login":"owner","type":"User"}`, false, 0},
		{`null`, true, 0},
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/user" {
				t.Errorf("wrong plan endpoint: %s", r.URL)
			}
			io.WriteString(w, test.body)
		})
		plan, minutes, err := c.CheckAccount(context.Background(), "owner", fakeToken)
		if test.quota != 0 {
			if err != nil || minutes != test.quota || plan != "pro" {
				t.Fatalf("plan=%s minutes=%d err=%v", plan, minutes, err)
			}
		} else if err == nil || errors.Is(err, ErrAccountMismatch) != test.mismatch || strings.Contains(err.Error(), fakeToken) {
			t.Fatalf("unsafe account error: %v", err)
		}
	}
}

func TestRequestsFailSafely(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); io.WriteString(w, fakeToken) })
		_, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user")
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != status || strings.Contains(err.Error(), fakeToken) {
			t.Fatalf("unsafe HTTP error: %v", err)
		}
	}
	for _, body := range []string{fakeToken, `[]`, `null`, `{"usageItems":[[]]}`, `{"usageItems":["invalid"]}`, strings.Repeat("x", maxResponseBytes+1)} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
		if _, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user"); err == nil || strings.Contains(err.Error(), fakeToken) {
			t.Fatalf("malformed report must fail safely: %v", err)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/secret-redirect?token="+fakeToken, http.StatusFound)
	})
	if _, _, err := c.CheckAccount(context.Background(), "owner", fakeToken); err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("redirect must fail safely: %v", err)
	}
}
