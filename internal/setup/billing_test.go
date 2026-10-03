package setup

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func billingItem(discount any, overrides map[string]any) map[string]any {
	item := map[string]any{"product": "Actions", "unitType": "minutes", "sku": "actions_linux", "repositoryName": "owner/private", "discountAmount": discount}
	for key, value := range overrides {
		item[key] = value
	}
	return item
}

func TestBillingExcludesPublicRepositoriesAndCachesVisibility(t *testing.T) {
	lookups := map[string]int{}
	items := []any{
		billingItem(1.2, nil), billingItem(1.8, nil),
		billingItem(6, map[string]any{"repositoryName": "owner/public"}),
		billingItem(12, map[string]any{"repositoryName": "owner/public", "sku": "Actions Windows"}),
		billingItem("ignored", map[string]any{"repositoryName": "owner/public"}),
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+fakeToken ||
			r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "gh-actions-quota" || r.Header.Get("X-GitHub-Api-Version") != apiVersion {
			t.Error("wrong billing/repository request headers or method")
		}
		switch r.URL.Path {
		case "/users/owner/settings/billing/usage":
			if r.URL.Query().Get("year") != "2026" || r.URL.Query().Get("month") != "8" || r.URL.Query().Get("product") != "Actions" {
				t.Error("wrong billing query or UTC month")
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": items})
		case "/repos/owner/private", "/repos/owner/public":
			lookups[r.URL.Path]++
			json.NewEncoder(w).Encode(map[string]bool{"private": r.URL.Path == "/repos/owner/private"})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	now, _ := time.Parse(time.RFC3339, "2026-09-01T00:30:00+02:00")
	c.now = func() time.Time { return now }
	used, err := c.checkBilling(context.Background(), "owner", fakeToken)
	if err != nil || used != 500 {
		t.Fatalf("used = %v, err = %v; want 500", used, err)
	}
	if !reflect.DeepEqual(lookups, map[string]int{"/repos/owner/private": 1, "/repos/owner/public": 1}) {
		t.Fatalf("visibility was not cached: %v", lookups)
	}
}

func TestBillingRepositoryLookupFailures(t *testing.T) {
	for _, status := range []int{404, 401, 403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			lookups := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/users/owner/settings/billing/usage" {
					json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(1.2, nil), billingItem(1.8, nil)}})
					return
				}
				lookups++
				w.WriteHeader(status)
				io.WriteString(w, fakeToken)
			})
			used, err := c.checkBilling(context.Background(), "owner", fakeToken)
			if status == 404 {
				if err != nil || used != 500 || lookups != 1 {
					t.Fatalf("404 should count and cache: used=%v err=%v lookups=%d", used, err, lookups)
				}
			} else if err == nil || err.Error() != "GitHub repository lookup failed" {
				t.Fatalf("unsafe or missing lookup error: %v", err)
			}
		})
	}
}

func TestBillingFiltersNonQuotaItemsBeforeLookup(t *testing.T) {
	items := []any{}
	for _, sku := range []any{"actions_linux_4_core", "Actions Windows 8 Core", "actions_storage", "actions_self_hosted", "unknown", nil} {
		items = append(items, billingItem("invalid", map[string]any{"sku": sku}))
	}
	for _, overrides := range []map[string]any{{"product": "Packages"}, {"unitType": "gigabytes"}, {"repositoryName": nil}, {"product": map[string]any{}}, {"unitType": []any{}}} {
		items = append(items, billingItem("invalid", overrides))
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/owner/settings/billing/usage" {
			t.Errorf("ignored items must not trigger lookup: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"usageItems": items})
	})
	if used, err := c.checkBilling(context.Background(), "owner", fakeToken); err != nil || used != 0 {
		t.Fatalf("ignored items counted: used=%v err=%v", used, err)
	}
}

func TestBillingStandardRunnerSKUs(t *testing.T) {
	for _, sku := range []string{"actions_linux_slim", "actions_linux", "actions_linux_arm", "actions_windows", "actions_windows_arm", "actions_macos", " Actions Linux Slim ", "Actions-Linux-ARM"} {
		t.Run(sku, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/repos/") {
					io.WriteString(w, `{"private":true}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(3, map[string]any{"sku": sku})}})
			})
			if used, err := c.checkBilling(context.Background(), "owner", fakeToken); err != nil || used != 500 {
				t.Fatalf("standard runner excluded: used=%v err=%v", used, err)
			}
		})
	}
}

func TestBillingValidation(t *testing.T) {
	for _, body := range []string{`{}`, `{"usageItems":null}`, `{"usageItems":{}}`, `{"usageItems":[null]}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
			if _, err := c.checkBilling(context.Background(), "owner", fakeToken); err == nil {
				t.Fatal("invalid billing report accepted")
			}
		})
	}
	for _, discount := range []string{"", `,"discountAmount":null`, `,"discountAmount":"6"`, `,"discountAmount":-1`, `,"discountAmount":1e999`} {
		t.Run(discount, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/repos/") {
					io.WriteString(w, `{"private":true}`)
					return
				}
				io.WriteString(w, `{"usageItems":[{"product":"Actions","unitType":"minutes","sku":"actions_linux","repositoryName":"owner/private"`+discount+`}]}`)
			})
			if _, err := c.checkBilling(context.Background(), "owner", fakeToken); err == nil || err.Error() != "Invalid discountAmount" {
				t.Fatalf("invalid discount accepted: %v", err)
			}
		})
	}
	for _, test := range []struct {
		items []any
		want  float64
	}{
		{[]any{}, 0},
		{[]any{billingItem(3, map[string]any{"repositoryName": "missing-owner"})}, 500},
		{[]any{billingItem(3, nil)}, 500},
		{[]any{billingItem(math.MaxFloat64, nil)}, math.Inf(1)},
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/repos/") {
				io.WriteString(w, `{}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": test.items})
		})
		used, err := c.checkBilling(context.Background(), "owner", fakeToken)
		if math.IsInf(test.want, 0) {
			if err == nil || !strings.Contains(err.Error(), "out of range") {
				t.Fatalf("overflow not rejected: %v", err)
			}
		} else if err != nil || used != test.want {
			t.Fatalf("used=%v err=%v want=%v", used, err, test.want)
		}
	}
}
