package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"notion-manager/internal/proxy"
)

// Opt-in browser fixture: only synthetic accounts and temporary runtime data.
func TestDeveloperDashboardBrowserFixture(t *testing.T) {
	if os.Getenv("NOTION_MANAGER_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in browser fixture")
	}
	t.Chdir(t.TempDir())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "fixture upstream disabled", 503) }))
	defer upstream.Close()
	cfg := proxy.DefaultConfig()
	cfg.Proxy.NotionAPIBase = upstream.URL
	previous := proxy.AppConfig
	proxy.AppConfig = cfg
	t.Cleanup(func() { proxy.AppConfig = previous })
	proxy.ApplyConfig(cfg)
	pool := proxy.NewAccountPool()
	now := time.Now()
	var ids []string
	for i := 0; i < 5; i++ {
		limit := &proxy.CreditRateLimit{Status: "within_limit"}
		limit.Window.Used, limit.Window.Limit, limit.Window.Window = float64(i*12), 100, "6h"
		limit.BillingPeriodWindow.Used, limit.BillingPeriodWindow.Limit = float64(i*3), 100
		status := proxy.QuotaSourceStatus{CheckedAtMs: now.UnixMilli(), SucceededAtMs: now.UnixMilli()}
		sources := map[string]proxy.QuotaSourceStatus{"basic": status, "premium": status, "allowance": status}
		if i == 0 {
			sources["allowance"] = proxy.QuotaSourceStatus{CheckedAtMs: now.UnixMilli(), SucceededAtMs: now.Add(-time.Hour).UnixMilli(), Error: "AI usage unavailable"}
		}
		acc := &proxy.Account{UserID: fmt.Sprintf("fixture-user-%d", i), SpaceID: fmt.Sprintf("fixture-space-%d", i), UserEmail: "developer@example.test", UserName: "Fixture Developer", SpaceName: fmt.Sprintf("Fixture Business %d", i+1), PlanType: "business", QuotaCheckedAt: &now,
			Models: []proxy.ModelEntry{{ID: "avocado-froyo-medium", Name: "Opus 4.6"}}, QuotaInfo: &proxy.QuotaInfo{IsEligible: true, HasPremium: true, PremiumBalanceKnown: true, PremiumBalance: 1000, PremiumLimit: 1000, RateLimit: limit, Sources: sources}}
		pool.AddAccount(acc)
		ids = append(ids, acc.AccountID)
	}
	if err := pool.UpdateRouting(proxy.RoutingConfig{DisabledAccountIDs: ids[3:]}); err != nil {
		t.Fatal(err)
	}
	store, err := proxy.NewRequestLogStore(filepath.Join("accounts", "fixture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	addLog := func(id string) {
		ms := time.Now().UnixMilli()
		duration, first := int64(1200), int64(350)
		if err := store.BeginRequest(proxy.RequestLog{RequestID: id, StartedAtMs: ms, Status: "in_progress", Protocol: "anthropic_messages"}); err != nil {
			t.Error(err)
			return
		}
		if err := store.FinalizeRequest(proxy.RequestLog{RequestID: id, StartedAtMs: ms, FinishedAtMs: &ms, Status: "success", Protocol: "anthropic_messages", RequestedModel: "opus-4.6", ResolvedModel: "avocado-froyo-medium", HTTPStatus: 200, DurationMs: &duration, FirstTokenMs: &first, StageDurationsMs: map[string]int64{"account_selection": 1, "quota_check": 0, "upstream": 1100, "tool_parse": 2}, CompletionEmitted: true}, nil); err != nil {
			t.Error(err)
		}
	}
	addLog("req_fixture_initial")
	auth := proxy.NewDashboardAuth("", "fixture-not-a-secret")
	deps := &proxy.RegisterJobsDeps{Pool: pool, AccountsDir: "accounts", Auth: auth, BusinessOnly: true}
	mux := newMux(pool, "accounts", "fixture-not-a-secret", auth, proxy.InitUsageStats(""), deps, store)
	stop := make(chan struct{})
	var once sync.Once
	mux.HandleFunc("/fixture/add-request", func(w http.ResponseWriter, r *http.Request) { addLog("req_fixture_poll_update"); w.WriteHeader(204) })
	mux.HandleFunc("/fixture/stop", func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(stop) }); w.WriteHeader(204) })
	server := httptest.NewServer(mux)
	defer server.Close()
	fmt.Println("BROWSER_FIXTURE_URL=" + server.URL + "/dashboard/#accounts")
	<-stop
}
