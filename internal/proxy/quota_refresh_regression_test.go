package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitQuotaRechecks(t *testing.T, pool *AccountPool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		pool.routingMu.Lock()
		busy := false
		for _, state := range pool.routingStates {
			busy = busy || state.rechecking
		}
		pool.routingMu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("quota recheck did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestQuotaSourceFailureStillQueriesOtherEndpoints(t *testing.T) {
	previousConfig := AppConfig
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig = previousConfig })
	previous := NotionAPIBase
	t.Cleanup(func() { NotionAPIBase = previous })
	var seen atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		switch r.URL.Path {
		case "/getAIUsageEligibility":
			w.WriteHeader(http.StatusUnauthorized)
		case "/getAIUsageEligibilityV2":
			w.Write([]byte(`{"premiumCredits":{"totalCreditBalance":0,"perSource":{"monthlyAllocated":{"limit":300}}}}`))
		case "/getCreditRateLimitStatus":
			w.Write([]byte(`{"window":{"used":100,"limit":100}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	NotionAPIBase = server.URL
	info, err := checkQuotaWithClient(&Account{UserID: "fixture", SpaceID: "fixture"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 3 || info.Sources["basic"].Error == "" || info.Sources["basic"].SucceededAtMs != 0 {
		t.Fatalf("incorrect source states: %+v", info.Sources)
	}
	if !info.PremiumBalanceKnown || info.PremiumBalance != 0 || info.RateLimit == nil {
		t.Fatalf("other sources were discarded: %+v", info)
	}
}

func TestQuotaWaitCancellationDoesNotCancelOtherWaiters(t *testing.T) {
	previous := quotaFetcher
	t.Cleanup(func() { quotaFetcher = previous })
	pool := routingPool(1)
	acc := pool.AccountsSnapshot()[0]
	started, finish := make(chan struct{}), make(chan struct{})
	quotaFetcher = func(*Account) (*QuotaInfo, error) { close(started); <-finish; return &QuotaInfo{IsEligible: true}, nil }
	flight := pool.startQuotaRefresh(acc)
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if pool.RefreshAccountQuotaContext(ctx, acc, 0) {
		t.Fatal("cancelled caller accepted a result")
	}
	close(finish)
	<-flight.done
	if !accountHasInferenceQuota(acc) {
		t.Fatal("one cancelled waiter discarded shared result")
	}
}

func TestQuotaSourceStateSurvivesBothPersistencePaths(t *testing.T) {
	pool := routingPool(1)
	acc := pool.AccountsSnapshot()[0]
	acc.UserEmail = "fixture@example.com"
	acc.TokenV2 = "fixture-token"
	info := acc.quotaInfoSnapshot()
	info.Sources = map[string]QuotaSourceStatus{"basic": {CheckedAtMs: 20, SucceededAtMs: 10, Error: "unavailable"}}
	acc.setQuotaInfo(info, nil)
	dir := t.TempDir()
	if _, err := SaveAccountToFile(acc, dir); err != nil {
		t.Fatal(err)
	}
	for _, update := range []bool{false, true} {
		if update {
			if err := saveAccountFile(dir, acc); err != nil {
				t.Fatal(err)
			}
		}
		reloaded := NewAccountPool()
		if err := reloaded.LoadFromDir(dir); err != nil {
			t.Fatal(err)
		}
		got := reloaded.AccountsSnapshot()[0].quotaInfoSnapshot().Sources["basic"]
		if got.SucceededAtMs != 10 || got.CheckedAtMs != 20 || got.Error != "unavailable" {
			t.Fatalf("lost source state: %+v", got)
		}
	}
}

func TestHealthyCacheAndResetRechecksDoNotBlockOtherTraffic(t *testing.T) {
	previous := quotaFetcher
	t.Cleanup(func() { quotaFetcher = previous })
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	checked := time.Now().Add(-30 * time.Second)
	accounts[1].setQuotaInfo(accounts[1].quotaInfoSnapshot(), &checked)
	setRoutingAllowance(accounts[0], 100, 0, time.Now().Add(-time.Second))
	finish := make(chan struct{})
	quotaFetcher = func(*Account) (*QuotaInfo, error) { <-finish; return &QuotaInfo{IsEligible: true}, nil }
	defer func() {
		pool.liveQuotaMu.Lock()
		flights := make([]*quotaFlight, 0, len(pool.liveQuotaInflight))
		for _, flight := range pool.liveQuotaInflight {
			flights = append(flights, flight)
		}
		pool.liveQuotaMu.Unlock()
		close(finish)
		for _, flight := range flights {
			<-flight.done
		}
		waitQuotaRechecks(t, pool)
	}()
	selected := make(chan *Account, 1)
	go func() {
		acc, release, err := pool.SelectForRequest("", false, nil, "")
		if err == nil {
			release()
		}
		selected <- acc
	}()
	select {
	case acc := <-selected:
		if acc != accounts[1] {
			t.Fatal("reset-pending exhausted member became available")
		}
	case <-time.After(time.Second):
		t.Fatal("reset checks blocked healthy selection")
	}
	healthy := make(chan bool, 1)
	go func() { healthy <- pool.RefreshAccountQuotaContext(context.Background(), accounts[1], time.Second) }()
	select {
	case eligible := <-healthy:
		if !eligible {
			t.Fatal("healthy cache rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("stale healthy quota cache blocked request")
	}
}

func TestPartialQuotaRefreshPreservesEachLastGoodSource(t *testing.T) {
	pool := routingPool(1)
	acc := pool.AccountsSnapshot()[0]
	setRoutingAllowance(acc, 100, 40, time.Now().Add(time.Hour))
	previous := acc.quotaInfoSnapshot()
	previous.HasPremium, previous.PremiumBalanceKnown, previous.PremiumBalance = true, true, 42
	previous.Sources = map[string]QuotaSourceStatus{
		"basic":     {CheckedAtMs: 10, SucceededAtMs: 10},
		"premium":   {CheckedAtMs: 10, SucceededAtMs: 10},
		"allowance": {CheckedAtMs: 10, SucceededAtMs: 10},
	}
	acc.setQuotaInfo(previous, nil)
	pool.applyQuotaInfo(acc, &QuotaInfo{IsEligible: true, Sources: map[string]QuotaSourceStatus{
		"basic":     {CheckedAtMs: 20, SucceededAtMs: 20},
		"premium":   {CheckedAtMs: 20, Error: "unavailable"},
		"allowance": {CheckedAtMs: 20, Error: "unavailable"},
	}})
	got := acc.quotaInfoSnapshot()
	if got.PremiumBalance != 42 || !got.PremiumBalanceKnown || got.RateLimit == nil || got.RateLimit.Window.Used != 100 {
		t.Fatalf("partial refresh discarded last-good values: %+v", got)
	}
	if got.Sources["premium"].SucceededAtMs != 10 || got.Sources["allowance"].SucceededAtMs != 10 {
		t.Fatal("failed source advanced its success timestamp")
	}
	if accountHasInferenceQuota(acc) {
		t.Fatal("failed allowance refresh released exhausted member")
	}
}

func TestQuotaFetchStartedBeforeExhaustionCannotRecoverIt(t *testing.T) {
	previous := quotaFetcher
	t.Cleanup(func() { quotaFetcher = previous })
	pool := routingPool(1)
	acc := pool.AccountsSnapshot()[0]
	started, finish := make(chan struct{}), make(chan struct{})
	quotaFetcher = func(*Account) (*QuotaInfo, error) { close(started); <-finish; return &QuotaInfo{IsEligible: true}, nil }
	flight := pool.startQuotaRefresh(acc)
	<-started
	pool.MarkQuotaExhausted(acc)
	close(finish)
	<-flight.done
	if accountHasInferenceQuota(acc) {
		t.Fatal("older in-flight refresh cleared newer inference exhaustion")
	}
}

func TestResearchExhaustionRequiresNewSuccessfulBasicCheck(t *testing.T) {
	for _, basicSuccess := range []bool{false, true} {
		t.Run(fmt.Sprint("basic_success=", basicSuccess), func(t *testing.T) {
			previous := quotaFetcher
			t.Cleanup(func() { quotaFetcher = previous })
			pool := routingPool(1)
			acc := pool.AccountsSnapshot()[0]
			started, finish := make(chan struct{}), make(chan struct{})
			quotaFetcher = func(*Account) (*QuotaInfo, error) {
				close(started)
				<-finish
				info := &QuotaInfo{IsEligible: true, HasPremium: true, ResearchModeUsage: 0, Sources: map[string]QuotaSourceStatus{
					"basic":   {CheckedAtMs: 20, Error: "unavailable"},
					"premium": {CheckedAtMs: 20, SucceededAtMs: 20},
				}}
				if basicSuccess {
					info.Sources["basic"] = QuotaSourceStatus{CheckedAtMs: 20, SucceededAtMs: 20}
				}
				return info, nil
			}
			flight := pool.startQuotaRefresh(acc)
			<-started
			pool.SetRoutingFailure(acc.AccountID, "", "research_exhausted", time.Time{})
			close(finish)
			<-flight.done
			assertBlocked := func(want bool) {
				t.Helper()
				pool.routingMu.Lock()
				blocked := pool.routingStateLocked(acc).researchExhausted
				pool.routingMu.Unlock()
				if blocked != want {
					t.Fatalf("research blocked = %v, want %v", blocked, want)
				}
			}
			assertBlocked(true)
			// Starting after the failure still cannot use a stale Basic result.
			quotaFetcher = func(*Account) (*QuotaInfo, error) {
				return &QuotaInfo{HasPremium: true, Sources: map[string]QuotaSourceStatus{
					"basic": {CheckedAtMs: 30, Error: "unavailable"}, "premium": {CheckedAtMs: 30, SucceededAtMs: 30},
				}}, nil
			}
			pool.RefreshAccountQuota(acc, 0)
			assertBlocked(true)
			quotaFetcher = func(*Account) (*QuotaInfo, error) {
				return &QuotaInfo{IsEligible: true, ResearchModeUsage: 0, Sources: map[string]QuotaSourceStatus{
					"basic": {CheckedAtMs: 40, SucceededAtMs: 40},
				}}, nil
			}
			pool.RefreshAccountQuota(acc, 0)
			assertBlocked(false)
		})
	}
}

func TestQuotaRecheckCannotClearNewRoutingFailures(t *testing.T) {
	for _, reason := range []string{"invalid_credentials", "rate_limited", "transient"} {
		t.Run(reason, func(t *testing.T) {
			previous := quotaFetcher
			t.Cleanup(func() { quotaFetcher = previous })
			pool := routingPool(1)
			acc := pool.AccountsSnapshot()[0]
			started, finish := make(chan struct{}), make(chan struct{})
			quotaFetcher = func(*Account) (*QuotaInfo, error) { close(started); <-finish; return &QuotaInfo{IsEligible: true}, nil }
			pool.SetRoutingFailure(acc.AccountID, "", "rate_limited", time.Now().Add(-time.Second))
			pool.recheckRoutingAccounts([]*Account{acc})
			<-started
			until := time.Now().Add(time.Hour)
			pool.SetRoutingFailure(acc.AccountID, "", reason, until)
			close(finish)
			waitQuotaRechecks(t, pool)
			status := pool.RoutingSnapshot().Accounts[0]
			if status.Reason != reason {
				t.Fatalf("new %s was cleared by older recheck: %+v", reason, status)
			}
			if reason != "invalid_credentials" && status.CooldownUntilMs != until.UnixMilli() {
				t.Fatalf("new cooldown changed: %+v", status)
			}
		})
	}
}

func TestConcurrentQuotaRefreshesJoinOneFlight(t *testing.T) {
	previous := quotaFetcher
	t.Cleanup(func() { quotaFetcher = previous })
	pool := routingPool(1)
	acc := pool.AccountsSnapshot()[0]
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	quotaFetcher = func(*Account) (*QuotaInfo, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-finish
		return &QuotaInfo{IsEligible: true}, nil
	}
	pool.RefreshAccountQuotaAsync(acc)
	<-started
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); pool.RefreshAccountQuota(acc, 0) }()
	}
	// Waiters must join the already-running fetch rather than starting their own.
	time.Sleep(20 * time.Millisecond)
	close(finish)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("%d overlapping quota fetches, want 1", got)
	}
}

func TestRoutingNewSessionsShareLoadAndKeepBinding(t *testing.T) {
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	setRoutingAllowance(accounts[0], 0, 0, time.Now().Add(time.Hour))
	setRoutingAllowance(accounts[1], 10, 0, time.Now().Add(time.Hour))
	first, release, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	second, releaseSecond, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond()
	if first == second {
		t.Fatal("new session ignored in-flight load")
	}
	bound, releaseBound, err := pool.SelectForRequest("", false, nil, first.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	releaseBound()
	if bound != first {
		t.Fatal("load balancing moved an existing binding")
	}
}

func TestAvailableCountUsesRoutingAndModelsExcludePausedMembers(t *testing.T) {
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	accounts[0].setModels([]ModelEntry{{ID: "paused-only"}})
	accounts[1].setModels([]ModelEntry{{ID: "active"}})
	if err := pool.UpdateRouting(RoutingConfig{DisabledAccountIDs: []string{accounts[0].AccountID}}); err != nil {
		t.Fatal(err)
	}
	if pool.AvailableCount() != pool.RoutingSnapshot().Available || pool.AvailableCount() != 1 {
		t.Fatal("availability disagrees with routing")
	}
	models := pool.AllModels()
	if len(models) != 1 || models[0].ID != "active" {
		t.Fatalf("advertised paused models: %+v", models)
	}
}
