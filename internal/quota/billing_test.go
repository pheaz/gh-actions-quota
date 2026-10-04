package quota

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This corpus is consumed by the single shared Go quota implementation.
func TestSharedBillingContract(t *testing.T) {
	data, err := os.ReadFile("../../test/fixtures/billing.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name         string          `json:"name"`
		OwnerType    string          `json:"ownerType"`
		UsageItems   json.RawMessage `json:"usageItems"`
		Repositories map[string]struct {
			Status  int `json:"status"`
			Private any `json:"private"`
		} `json:"repositories"`
		UsedMinutes float64        `json:"usedMinutes"`
		Error       string         `json:"error"`
		Lookups     map[string]int `json:"lookups"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			ownerType := fixture.OwnerType
			if ownerType == "" {
				ownerType = "user"
			}
			endpoint := "users"
			if ownerType == "organization" {
				endpoint = "organizations"
			}
			lookups := map[string]int{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/"+endpoint+"/owner/settings/billing/usage" {
					if r.URL.Query().Get("year") != "2026" || r.URL.Query().Get("month") != "8" || r.URL.Query().Get("product") != "Actions" {
						t.Error("wrong billing query or UTC month")
					}
					json.NewEncoder(w).Encode(map[string]any{"usageItems": fixture.UsageItems})
					return
				}
				repository := strings.TrimPrefix(r.URL.Path, "/repos/")
				response, ok := fixture.Repositories[repository]
				if !ok {
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				lookups[repository]++
				w.WriteHeader(response.Status)
				json.NewEncoder(w).Encode(map[string]any{"private": response.Private})
			})
			now, _ := time.Parse(time.RFC3339, "2026-09-01T00:30:00+02:00")
			c.Now = func() time.Time { return now }
			used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, ownerType)
			if fixture.Error != "" {
				if err == nil || err.Error() != fixture.Error {
					t.Fatalf("billing error = %v, want %s", err, fixture.Error)
				}
			} else if err != nil || math.IsNaN(used) || math.Abs(used-fixture.UsedMinutes) > 1e-8 {
				t.Fatalf("used=%v err=%v, want %v", used, err, fixture.UsedMinutes)
			}
			if !reflect.DeepEqual(lookups, fixture.Lookups) {
				t.Fatalf("lookups=%v, want %v", lookups, fixture.Lookups)
			}
		})
	}
}

func billingItem(quantity any, overrides map[string]any) map[string]any {
	item := map[string]any{"product": "actions", "unitType": "Minutes", "sku": "actions_linux", "repositoryName": "owner/private", "quantity": quantity,
		"pricePerUnit": 0.006, "grossAmount": 3, "discountAmount": 0, "netAmount": 3}
	for key, value := range overrides {
		item[key] = value
	}
	return item
}

func TestBillingExcludesPublicRepositoriesAndCachesVisibility(t *testing.T) {
	lookups := map[string]int{}
	items := []any{
		billingItem(200, nil), billingItem(300, nil),
		billingItem(1000, map[string]any{"repositoryName": "owner/public"}),
		billingItem(1200, map[string]any{"repositoryName": "owner/public", "sku": "Actions Windows"}),
		billingItem("ignored", map[string]any{"repositoryName": "owner/public"}),
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+fakeToken ||
			r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "gh-actions-quota" || r.Header.Get("X-GitHub-Api-Version") != APIVersion {
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
	c.Now = func() time.Time { return now }
	used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user")
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
					json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(200, nil), billingItem(300, nil)}})
					return
				}
				lookups++
				w.WriteHeader(status)
				io.WriteString(w, fakeToken)
			})
			used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user")
			if status == 404 {
				if err != nil || used != 0 || lookups != 1 {
					t.Fatalf("404 should be excluded and cached: used=%v err=%v lookups=%d", used, err, lookups)
				}
			} else if err == nil || err.Error() != "GitHub repository lookup failed" {
				t.Fatalf("unsafe or missing lookup error: %v", err)
			}
		})
	}
}

func TestBillingFiltersNonQuotaItemsBeforeLookup(t *testing.T) {
	items := []any{}
	for _, sku := range []any{"actions_linux_4_core", "Actions Windows 8 Core", "Actions macOS 12-core", "actions_storage", "actions_self_hosted", "unknown", "constructor", nil} {
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
	if used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user"); err != nil || used != 0 {
		t.Fatalf("ignored items counted: used=%v err=%v", used, err)
	}
}

func TestBillingStandardRunnerSKUs(t *testing.T) {
	for _, test := range []struct {
		sku  string
		want float64
	}{
		{"actions_linux_slim", 33.33333333333333}, {"actions_linux_arm", 83.33333333333333},
		{"actions_linux", 100}, {"actions_windows", 166.66666666666666},
		{"actions_windows_arm", 166.66666666666666}, {"actions_macos", 1033.3333333333333},
		{" Actions Linux Slim ", 33.33333333333333}, {"Actions-Linux-ARM", 83.33333333333333},
	} {
		t.Run(test.sku, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/repos/") {
					io.WriteString(w, `{"private":true}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(100, map[string]any{"sku": test.sku})}})
			})
			if used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user"); err != nil || math.IsNaN(used) || math.Abs(used-test.want) > 1e-8 {
				t.Fatalf("standard runner excluded: used=%v err=%v", used, err)
			}
		})
	}
}

func TestBillingValidation(t *testing.T) {
	for _, body := range []string{`{}`, `{"usageItems":null}`, `{"usageItems":{}}`, `{"usageItems":[null]}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
			if _, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user"); err == nil {
				t.Fatal("invalid billing report accepted")
			}
		})
	}
	for _, quantity := range []string{"", `,"quantity":null`, `,"quantity":"6"`, `,"quantity":-1`, `,"quantity":1e999`} {
		t.Run(quantity, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/repos/") {
					io.WriteString(w, `{"private":true}`)
					return
				}
				io.WriteString(w, `{"usageItems":[{"product":"actions","unitType":"Minutes","sku":"actions_linux","repositoryName":"owner/private"`+quantity+`}]}`)
			})
			if _, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user"); err == nil || err.Error() != "Invalid quantity" {
				t.Fatalf("invalid quantity accepted: %v", err)
			}
		})
	}
	for _, test := range []struct {
		items []any
		want  float64
	}{
		{[]any{}, 0},
		{[]any{billingItem(500, map[string]any{"repositoryName": "missing-owner"})}, 0},
		{[]any{billingItem(500, nil)}, 0},
		{[]any{billingItem(math.MaxFloat64, map[string]any{"sku": "actions_macos", "repositoryName": "owner/known-private"})}, math.Inf(1)},
		{[]any{billingItem(math.MaxFloat64, map[string]any{"repositoryName": "owner/known-private"}), billingItem(math.MaxFloat64, map[string]any{"repositoryName": "owner/known-private"})}, math.Inf(1)},
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/owner/known-private" {
				io.WriteString(w, `{"private":true}`)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/repos/") {
				io.WriteString(w, `{}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": test.items})
		})
		used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "user")
		if math.IsInf(test.want, 0) {
			if err == nil || !strings.Contains(err.Error(), "out of range") {
				t.Fatalf("overflow not rejected: %v", err)
			}
		} else if err != nil || used != test.want {
			t.Fatalf("used=%v err=%v want=%v", used, err, test.want)
		}
	}
}

const fakeToken = "ghu_test_only_not_a_real_token"

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := NewClient()
	c.APIBase = server.URL
	return c
}
