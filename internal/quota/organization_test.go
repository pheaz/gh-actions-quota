package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOrganizationPlans(t *testing.T) {
	for _, test := range []struct {
		plan     string
		included int
	}{{"free", 2000}, {"Team", 3000}, {"pro", 0}, {"business", 0}, {"enterprise", 0}, {"enterprise cloud", 0}, {"Medium", 0}, {"", 0}, {fakeToken, 0}} {
		t.Run(test.plan, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/orgs/owner" {
					t.Errorf("wrong plan endpoint: %s", r.URL)
				}
				json.NewEncoder(w).Encode(map[string]any{"login": "OWNER", "type": "Organization", "plan": map[string]string{"name": test.plan}})
			})
			_, included, err := c.CheckOrganization(context.Background(), "owner", fakeToken)
			if included != test.included || (test.included == 0) != errors.Is(err, ErrQuotaUnavailable) {
				t.Fatalf("included=%d err=%v", included, err)
			}
			if err != nil && strings.Contains(err.Error(), fakeToken) {
				t.Fatal("unsanitized plan")
			}
		})
	}
	for _, body := range []string{`null`, `{}`, `{"login":"another","type":"Organization"}`, `{"login":"owner","type":"User"}`, `{"login":"owner","type":"Organization","plan":[]}`} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
		if _, _, err := c.CheckOrganization(context.Background(), "owner", fakeToken); err == nil || errors.Is(err, ErrQuotaUnavailable) {
			t.Fatalf("invalid organization accepted: %v", err)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) })
	if _, _, err := c.CheckOrganization(context.Background(), "owner", fakeToken); !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatal(err)
	}
}

func TestOrganizationReportFiltersAndUTC(t *testing.T) {
	lookups := map[string]int{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-GitHub-Api-Version") != APIVersion || r.Header.Get("Authorization") != "Bearer "+fakeToken {
			t.Error("wrong request headers")
		}
		if r.URL.Path == "/organizations/owner/settings/billing/usage" {
			if r.URL.Query().Get("year") != "2026" || r.URL.Query().Get("month") != "12" || len(r.URL.Query()) != 2 {
				t.Errorf("undocumented filters or wrong UTC month: %s", r.URL)
			}
			items := []any{billingItem(100, nil), billingItem(200, nil), billingItem(9000, map[string]any{"repositoryName": "owner/public"})}
			for _, sku := range []string{"actions_self_hosted", "actions_linux_4_core", "actions_macos_12_core", "actions_storage"} {
				items = append(items, billingItem("ignored", map[string]any{"sku": sku, "repositoryName": nil}))
			}
			items = append(items, billingItem("ignored", map[string]any{"product": "Packages"}))
			json.NewEncoder(w).Encode(map[string]any{"usageItems": items})
			return
		}
		lookups[r.URL.Path]++
		json.NewEncoder(w).Encode(map[string]bool{"private": r.URL.Path == "/repos/owner/private"})
	})
	now, _ := time.Parse(time.RFC3339, "2027-01-01T00:30:00+02:00")
	c.Now = func() time.Time { return now }
	used, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "organization")
	if err != nil || used != 300 || len(lookups) != 2 || lookups["/repos/owner/private"] != 1 || lookups["/repos/owner/public"] != 1 {
		t.Fatalf("used=%v err=%v lookups=%v", used, err, lookups)
	}
}

func TestOrganizationBillingAccessFailures(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); io.WriteString(w, fakeToken) })
		_, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "organization")
		var access *OrganizationAccessError
		if err == nil || strings.Contains(err.Error(), fakeToken) || errors.As(err, &access) != (status == 403 || status == 404) {
			t.Fatalf("status=%d err=%v", status, err)
		}
	}
}

func TestOrganizationReportCannotSilentlyOmitUsage(t *testing.T) {
	for _, body := range []string{`{}`, `{"usageItems":null}`, `{"usageItems":[null]}`, `{"usageItems":[{}]}`, `{"usageItems":[{"product":"Actions","unitType":"minutes","sku":"actions_linux","quantity":10}]}`, `{"usageItems":[{"product":"Actions","unitType":"minutes","sku":"actions_linux","quantity":10,"repositoryName":"someone/private"}]}`} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) })
		if _, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "organization"); err == nil {
			t.Fatalf("malformed report accepted: %s", body)
		}
	}
	for _, body := range []string{`{}`, `null`, `{"private":null}`, `{"private":"true"}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/repos/") {
				io.WriteString(w, body)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(10, nil)}})
		})
		if _, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "organization"); err == nil {
			t.Fatal("unknown visibility reduced usage")
		}
	}
	for _, status := range []int{401, 403, 404, 500} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/repos/") {
				w.WriteHeader(status)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"usageItems": []any{billingItem(10, nil)}})
		})
		if _, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "organization"); err == nil {
			t.Fatal("unreadable private repository reduced usage")
		}
	}
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<https://example.invalid/next>; rel="next"`)
		io.WriteString(w, `{"usageItems":[]}`)
	})
	if _, err := c.UsedMinutes(context.Background(), "owner", fakeToken, "organization"); err == nil {
		t.Fatal("partial report accepted")
	}
}
