package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreditRateLimit(t *testing.T) {
	oldConfig := AppConfig
	AppConfig = DefaultConfig()
	defer func() { AppConfig = oldConfig }()
	oldBase := NotionAPIBase
	defer func() { NotionAPIBase = oldBase }()
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"business", `{"status":"within_limit","window":{"window":"6h","used":7.37,"limit":100},"resetsInSeconds":120,"billingPeriodWindow":{"used":0.88,"limit":100,"periodEndMs":1794978000000}}`, 200, false},
		{"unsupported", `{}`, 200, true},
		{"malformed", `invalid`, 200, true},
		{"unauthorized", `{}`, 401, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/getCreditRateLimitStatus" {
					t.Error("wrong request route")
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["spaceId"] != "test-space" {
					t.Error("wrong workspace")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			NotionAPIBase = server.URL
			result, err := checkCreditRateLimit(&Account{SpaceID: "test-space"}, server.Client())
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v", err)
			}
			if tc.wantError {
				return
			}
			if result.Window.Used != 7.37 || result.BillingPeriodWindow.Used != 0.88 {
				t.Fatalf("lost fractional usage: %+v", result)
			}
			if delta := result.RollingResetAtMs - time.Now().UnixMilli(); delta < 118000 || delta > 121000 {
				t.Fatalf("invalid reset: %d", delta)
			}
			fixture := `{"quota_info":{"rate_limit":` + tc.body + `}}`
			restored := loadPersistedQuotaInfo([]byte(fixture))
			if restored == nil || restored.RateLimit == nil || restored.RateLimit.Window.Used != 7.37 {
				t.Fatal("usage not restored")
			}
			pool := NewAccountPool()
			pool.AddAccount(&Account{UserID: "test-user", SpaceID: "test-space", QuotaInfo: &QuotaInfo{RateLimit: result, IsEligible: true}})
			encoded, err := json.Marshal(pool.GetAccountDetails())
			if err != nil || !strings.Contains(string(encoded), `"used":7.37`) {
				t.Fatalf("usage not exposed: %s %v", encoded, err)
			}
		})
	}
}
