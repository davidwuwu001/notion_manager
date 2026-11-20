package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBillingCreditBuckets(t *testing.T) {
	oldConfig, oldBase := AppConfig, NotionAPIBase
	AppConfig = DefaultConfig()
	defer func() { AppConfig, NotionAPIBase = oldConfig, oldBase }()
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"two resolved", `{"status":"not_applicable","allowances":[{"bucketId":"auto","cadence":"billing_period","state":"resolved","credits":{"used":21.03,"limit":1500}},{"bucketId":"flex","cadence":"billing_period","state":"resolved","credits":{"used":0,"limit":500}}]}`, true},
		{"missing amount", `{"allowances":[{"bucketId":"auto","cadence":"billing_period","state":"resolved","credits":{"used":0}}]}`, false},
		{"duplicate", `{"allowances":[{"bucketId":"auto","cadence":"billing_period","state":"resolved","credits":{"used":0,"limit":1}},{"bucketId":"auto","cadence":"billing_period","state":"resolved","credits":{"used":0,"limit":1}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer s.Close()
			NotionAPIBase = s.URL
			result, err := checkCreditRateLimit(&Account{SpaceID: "fixture"}, s.Client())
			if (err == nil) != tc.ok {
				t.Fatalf("error = %v", err)
			}
			if !tc.ok {
				return
			}
			data, _ := json.Marshal(result)
			var v map[string]any
			_ = json.Unmarshal(data, &v)
			auto, a := v["standard"].(map[string]any)
			flex, f := v["select"].(map[string]any)
			if !a || !f || auto["used"] != 21.03 || auto["limit"] != float64(1500) || flex["used"] != float64(0) || flex["limit"] != float64(500) {
				t.Fatalf("wrong buckets: %s", data)
			}
		})
	}
}

func TestBillingCreditBucketsIndependentFailure(t *testing.T) {
	oldConfig, oldBase := AppConfig, NotionAPIBase
	AppConfig = DefaultConfig()
	defer func() { AppConfig, NotionAPIBase = oldConfig, oldBase }()
	for _, body := range []string{
		`{"allowances":[{"bucketId":"auto","cadence":"billing_period","state":"pending"},{"bucketId":"auto","cadence":"billing_period","state":"resolved","credits":{"used":1,"limit":1500}},{"bucketId":"flex","cadence":"billing_period","state":"resolved","credits":{"used":0,"limit":500}}]}`,
		`{"allowances":[{"bucketId":"auto","cadence":"billing_period","state":"resolved","credits":{"used":-1,"limit":1500}},{"bucketId":"flex","cadence":"billing_period","state":"resolved","credits":{"used":0,"limit":500}}]}`,
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		NotionAPIBase = s.URL
		got, err := checkCreditRateLimit(&Account{SpaceID: "fixture"}, s.Client())
		s.Close()
		if err != nil || got.Standard != nil || got.Select == nil || got.Select.Used != 0 {
			t.Fatalf("independent bucket parse got=%+v err=%v", got, err)
		}
	}
}

func TestCreditBucketFreshness(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	current := &CreditBucket{Used: 0, Limit: 500, PeriodEndMs: now.Add(time.Hour).UnixMilli()}
	for _, tt := range []struct {
		name    string
		bucket  *CreditBucket
		source  QuotaSourceStatus
		checked time.Time
		want    string
	}{
		{"fresh", current, QuotaSourceStatus{SucceededAtMs: now.UnixMilli()}, time.Time{}, "current"},
		{"failed", current, QuotaSourceStatus{SucceededAtMs: now.UnixMilli(), Error: "network_error"}, time.Time{}, "stale"},
		{"new check without success", current, QuotaSourceStatus{SucceededAtMs: now.Add(-time.Minute).UnixMilli(), CheckedAtMs: now.UnixMilli()}, time.Time{}, "stale"},
		{"old", current, QuotaSourceStatus{SucceededAtMs: now.Add(-25 * time.Hour).UnixMilli()}, time.Time{}, "stale"},
		{"future", current, QuotaSourceStatus{SucceededAtMs: now.Add(6 * time.Minute).UnixMilli()}, time.Time{}, "stale"},
		{"no period", &CreditBucket{Used: 0, Limit: 500}, QuotaSourceStatus{SucceededAtMs: now.UnixMilli()}, time.Time{}, "stale"},
		{"expired", &CreditBucket{Used: 0, Limit: 500, PeriodEndMs: now.UnixMilli()}, QuotaSourceStatus{SucceededAtMs: now.UnixMilli()}, time.Time{}, "expired"},
		{"legacy", current, QuotaSourceStatus{}, now, "stale"},
		{"no timestamp", current, QuotaSourceStatus{}, time.Time{}, "unknown"},
		{"nil", nil, QuotaSourceStatus{}, time.Time{}, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := creditBucketStatus(tt.bucket, tt.source, tt.checked, now); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestCreditBucketFailureKeepsLastSuccess(t *testing.T) {
	now := time.Now()
	previous := &QuotaInfo{RateLimit: &CreditRateLimit{Standard: &CreditBucket{Used: 1.25, Limit: 1500, PeriodEndMs: now.Add(time.Hour).UnixMilli()}, Select: &CreditBucket{Used: 0, Limit: 500, PeriodEndMs: now.Add(time.Hour).UnixMilli()}}, Sources: map[string]QuotaSourceStatus{"allowance": {CheckedAtMs: now.UnixMilli(), SucceededAtMs: now.UnixMilli()}}}
	update := &QuotaInfo{Sources: map[string]QuotaSourceStatus{"allowance": {CheckedAtMs: now.Add(time.Second).UnixMilli(), Error: "network_error"}}, RateLimitError: "network_error"}
	got := mergeQuotaUpdate(previous, update)
	if got.RateLimit == nil || got.RateLimit.Standard == nil || got.RateLimit.Standard.Used != 1.25 || got.RateLimit.Select == nil || got.Sources["allowance"].SucceededAtMs != now.UnixMilli() || creditBucketStatus(got.RateLimit.Standard, got.Sources["allowance"], time.Time{}, now.Add(time.Second)) != "stale" {
		t.Fatalf("last good credit was lost or misreported: %+v", got)
	}
	got.RateLimit.Standard.Used = 42
	if previous.RateLimit.Standard.Used != 1.25 {
		t.Fatal("credit snapshot shares mutable bucket")
	}
}

func TestCreditBucketSummaryIncludesUnknown(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	fresh := map[string]QuotaSourceStatus{"allowance": {SucceededAtMs: now.UnixMilli(), CheckedAtMs: now.UnixMilli()}}
	failed := map[string]QuotaSourceStatus{"allowance": {SucceededAtMs: now.Add(-time.Hour).UnixMilli(), CheckedAtMs: now.UnixMilli(), Error: "network_error"}}
	s := summarizeAccountsAt([]map[string]interface{}{
		{"rate_limit": &CreditRateLimit{Standard: &CreditBucket{Used: 1.25, Limit: 1500, PeriodEndMs: now.Add(time.Hour).UnixMilli()}, Select: &CreditBucket{Used: 0, Limit: 500, PeriodEndMs: now.Add(time.Hour).UnixMilli()}}, "quota_sources": fresh},
		{"rate_limit": &CreditRateLimit{Standard: &CreditBucket{Used: 2, Limit: 1500, PeriodEndMs: now.Add(time.Hour).UnixMilli()}}, "quota_sources": failed},
		{"rate_limit": &CreditRateLimit{Standard: &CreditBucket{Used: 20, Limit: 1500, PeriodEndMs: now.Add(-time.Second).UnixMilli()}}, "quota_sources": fresh},
		{},
	}, now)
	if s.StandardCredits.Used != 3.25 || s.StandardCredits.Limit != 3000 || s.StandardCredits.Known != 2 || s.StandardCredits.Unknown != 2 || s.StandardCredits.Stale != 1 || s.StandardCredits.Remaining != 2996.75 || s.SelectCredits.Known != 1 || s.SelectCredits.Unknown != 3 {
		t.Fatalf("unexpected summary: %+v", s)
	}
}
