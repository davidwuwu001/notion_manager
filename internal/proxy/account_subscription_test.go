package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBusinessSubscriptionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		allowed    bool
	}{
		{"business coupon is not a trial", `{"billingData":{"type":"public","subscription":{"items":[{"price":{"product":"business"}}]}}}`, true},
		{"admin business", `{"billingData":{"type":"admin","subscription":{"items":[{"price":{"product":"business"}}]}}}`, true},
		{"credit card trial", `{"billingData":{"type":"public","subscription":{"trialEnd":"2099-01-01T00:00:00Z","items":[{"price":{"product":"business"}}]}}}`, false},
		{"reverse trial", `{"billingData":{"type":"public","trial":{"endDate":"2099-01-01T00:00:00Z","items":[{"price":{"product":"business"}}]}}}`, false},
		{"stacked trial", `{"billingData":{"type":"public","trial":{},"subscription":{"items":[{"price":{"product":"business"}}]}}}`, false},
		{"converted trial", `{"billingData":{"type":"public","subscription":{"trialEnd":"2020-01-01T00:00:00Z","items":[{"price":{"product":"business"}}]}}}`, true},
		{"invalid trial timestamp", `{"billingData":{"type":"public","subscription":{"trialEnd":"invalid","items":[{"price":{"product":"business"}}]}}}`, false},
		{"free", `{"billingData":{"type":"public"}}`, false},
		{"plus", `{"billingData":{"type":"public","subscription":{"items":[{"price":{"product":"plus"}}]}}}`, false},
		{"enterprise", `{"billingData":{"type":"public","subscription":{"items":[{"price":{"product":"enterprise"}}]}}}`, false},
		{"unknown shape", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data featureBillingResponse
			if err := json.Unmarshal([]byte(tc.body), &data); err != nil {
				t.Fatal(err)
			}
			if err := data.requireBusiness(time.Now()); (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
		})
	}
}

func TestImportRejectedSubscriptionDoesNotWrite(t *testing.T) {
	for _, billing := range []string{
		`{"billingData":{"type":"public","trial":{"endDate":"2099-01-01T00:00:00Z"}}}`,
		`{"billingData":{"type":"public"}}`,
		`{}`,
		`not json`,
	} {
		t.Run(billing, func(t *testing.T) {
			content := `{"recordMap":{"notion_user":{"u":{"value":{"name":"User"}}},"user_root":{"u":{"value":{"space_view_pointers":[{"spaceId":"s"}]}}},"space":{"s":{"value":{"id":"s","plan_type":"business"}}}}}`
			stubAccountDiscovery(t, content, nil)
			clientFactory := discoveryHTTPClient
			discoveryHTTPClient = func(d time.Duration) *http.Client {
				client := clientFactory(d)
				base := client.Transport
				client.Transport = accountDiscoveryRoundTripper(func(req *http.Request) (*http.Response, error) {
					if strings.HasSuffix(req.URL.Path, "/getFeatureBillingData") {
						if req.Header.Get("x-notion-active-user-header") != "u" {
							t.Fatal("missing identity")
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(billing))}, nil
					}
					return base.RoundTrip(req)
				})
				return client
			}
			dir := t.TempDir()
			pool := NewAccountPool()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/admin/accounts/add", strings.NewReader(`{"token_v2":"fixture","space_id":"s"}`))
			HandleAddAccount(pool, dir, NewDashboardAuth("", "")).ServeHTTP(rec, req)
			files, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Code != 400 || pool.Count() != 0 || len(files) != 0 {
				t.Fatalf("status=%d pool=%d files=%d", rec.Code, pool.Count(), len(files))
			}
		})
	}
}
