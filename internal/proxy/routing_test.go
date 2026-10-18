package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func routingPool(size int) *AccountPool {
	pool := NewAccountPool()
	for i := 0; i < size; i++ {
		pool.AddAccount(&Account{
			UserID: fmt.Sprintf("user-%d", i), SpaceID: fmt.Sprintf("space-%d", i),
			UserEmail: "shared@example.com", PlanType: "business",
			QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 100, UserLimit: 100},
		})
	}
	return pool
}

func setRoutingAllowance(acc *Account, rollingUsed, monthlyUsed float64, reset time.Time) {
	limit := &CreditRateLimit{Status: "within_limit", RollingResetAtMs: reset.UnixMilli()}
	limit.Window.Limit = 100
	limit.Window.Used = rollingUsed
	limit.BillingPeriodWindow.Limit = 100
	limit.BillingPeriodWindow.Used = monthlyUsed
	limit.BillingPeriodWindow.PeriodEndMs = reset.UnixMilli()
	now := time.Now()
	acc.setQuotaInfo(&QuotaInfo{IsEligible: true, RateLimit: limit}, &now)
}

func TestRoutingQuotaUsesComparableWindowHeadroom(t *testing.T) {
	for _, size := range []int{1, 2, 5, 8} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			pool := routingPool(size)
			accounts := pool.AccountsSnapshot()
			for i, acc := range accounts {
				setRoutingAllowance(acc, float64(90-i*10), 0, time.Now().Add(time.Hour))
			}
			got, release, err := pool.SelectForRequest("", false, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if got != accounts[size-1] {
				t.Fatalf("selected low headroom account %s", got.AccountID)
			}
		})
	}
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	setRoutingAllowance(accounts[0], 0, 99, time.Now().Add(time.Hour))
	setRoutingAllowance(accounts[1], 70, 70, time.Now().Add(time.Hour))
	got, release, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if got != accounts[1] {
		t.Fatal("monthly constraint did not affect selection")
	}
}

func TestRoutingMixedStatesAndAccountIDBinding(t *testing.T) {
	pool := routingPool(5)
	accounts := pool.AccountsSnapshot()
	setRoutingAllowance(accounts[0], 100, 0, time.Now().Add(time.Hour))
	pool.SetRoutingFailure(accounts[1].AccountID, "", "rate_limited", time.Now().Add(time.Hour))
	pool.SetRoutingFailure(accounts[2].AccountID, "", "invalid_credentials", time.Time{})
	if err := pool.UpdateRouting(RoutingConfig{DisabledAccountIDs: []string{accounts[3].AccountID}}); err != nil {
		t.Fatal(err)
	}
	got, release, err := pool.SelectForRequest("", false, nil, accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if got != accounts[4] {
		t.Fatalf("mixed pool chose %s", got.AccountID)
	}
	if got := pool.GetByEmail("shared@example.com"); got != nil {
		t.Fatal("ambiguous email selected a workspace")
	}
	if got := pool.GetByAccountID(accounts[4].AccountID); got != accounts[4] {
		t.Fatal("ID lookup missed account")
	}
	status := pool.RoutingSnapshot()
	if status.Total != 5 || status.Available != 1 || status.Paused != 1 || status.Invalid != 1 || status.Exhausted != 1 || status.Cooldown != 1 {
		t.Fatalf("incorrect pool state: %+v", status)
	}
	_, _, err = pool.SelectForRequest("", false, map[string]bool{accounts[4].AccountID: true}, "")
	if !errors.Is(err, ErrNoRoutingAccount) {
		t.Fatalf("expected unavailable pool, got %v", err)
	}
}

func TestRoutingBoundAccountModelResearchAndBudget(t *testing.T) {
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	accounts[0].setModels([]ModelEntry{{Name: "A", ID: "routing-model-a"}})
	accounts[1].setModels([]ModelEntry{{Name: "B", ID: "routing-model-b"}})
	acc, release, err := pool.SelectForRequest("routing-model-b", false, nil, accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if acc != accounts[1] {
		t.Fatal("selected member lacking model capability")
	}
	accounts[0].setModels(nil)
	acc, release, err = pool.SelectForRequest("routing-model-b", false, nil, accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if acc != accounts[0] {
		t.Fatal("eligible bound session moved to another workspace")
	}
	pool.SetRoutingFailure(accounts[0].AccountID, "routing-model-b", "model_unavailable", time.Time{})
	acc, release, err = pool.SelectForRequest("routing-model-b", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if acc != accounts[1] {
		t.Fatal("known unsupported account/model pair retried")
	}
	pool.SetRoutingFailure(accounts[1].AccountID, "", "research_exhausted", time.Time{})
	acc, release, err = pool.SelectForRequest("researcher", true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if acc != accounts[0] {
		t.Fatal("research-exhausted member used")
	}
	if err := pool.UpdateRouting(RoutingConfig{MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	_, _, err = pool.SelectForRequest("", false, map[string]bool{accounts[0].AccountID: true}, "")
	if !errors.Is(err, ErrRoutingBudgetExhausted) {
		t.Fatalf("wrong budget outcome: %v", err)
	}
}

func TestRoutingConcurrentSlotsAndReimportPreserveIdentity(t *testing.T) {
	pool := routingPool(2)
	if err := pool.UpdateRouting(RoutingConfig{MaxConcurrentPerAccount: 1}); err != nil {
		t.Fatal(err)
	}
	first, release, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	replacement := &Account{UserID: first.UserID, SpaceID: first.SpaceID, UserEmail: first.UserEmail, PlanType: "business", QuotaInfo: &QuotaInfo{IsEligible: true}}
	pool.AddAccount(replacement)
	second, secondRelease, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if second.AccountID == first.AccountID {
		t.Fatal("reimport bypassed account concurrency slot")
	}
	_, _, err = pool.SelectForRequest("", false, nil, "")
	if !errors.Is(err, ErrRoutingBusy) {
		t.Fatalf("full pool result=%v", err)
	}
	release()
	release()
	secondRelease()
	if pool.RoutingSnapshot().Busy != 0 {
		t.Fatal("released slot leaked")
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, done, err := pool.SelectForRequest("", false, nil, "")
			if err != nil && !errors.Is(err, ErrRoutingBusy) {
				t.Errorf("unexpected concurrent error: %v", err)
				return
			}
			if err == nil {
				done()
			}
			for _, state := range pool.RoutingSnapshot().Accounts {
				if state.InFlight > 1 || state.InFlight < 0 {
					t.Errorf("slot overbooked: %+v", state)
				}
			}
		}()
	}
	wg.Wait()
	acc, done, err := pool.SelectForRequest("", false, map[string]bool{first.AccountID: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	done()
	if acc.AccountID == first.AccountID {
		t.Fatal("reimport bypassed per-request AccountID exclusion")
	}
}

func TestRoutingQuotaResetRequiresSuccessfulRecheck(t *testing.T) {
	oldFetcher := quotaFetcher
	t.Cleanup(func() { quotaFetcher = oldFetcher })
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	setRoutingAllowance(accounts[0], 100, 0, time.Now().Add(-time.Second))
	quotaFetcher = func(acc *Account) (*QuotaInfo, error) { return nil, errors.New("fixture unavailable") }
	acc, done, err := pool.SelectForRequest("", false, nil, accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	done()
	if acc != accounts[1] {
		t.Fatal("failed reset check recovered exhausted member")
	}
	// A new explicit cooldown deadline permits another confirmation without waiting in the test.
	pool.SetRoutingFailure(accounts[0].AccountID, "", "rate_limited", time.Now().Add(-time.Second))
	quotaFetcher = func(acc *Account) (*QuotaInfo, error) { return &QuotaInfo{IsEligible: true, SpaceLimit: 100}, nil }
	acc, done, err = pool.SelectForRequest("", false, nil, accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	done()
	if acc != accounts[0] {
		t.Fatal("confirmed quota reset did not recover member")
	}
	pool.SetRoutingFailure(accounts[0].AccountID, "", "invalid_credentials", time.Time{})
	pool.AddAccount(&Account{UserID: accounts[0].UserID, SpaceID: accounts[0].SpaceID, PlanType: "business", QuotaInfo: &QuotaInfo{IsEligible: true}})
	acc, done, err = pool.SelectForRequest("", false, nil, accounts[0].AccountID)
	if err != nil {
		t.Fatal(err)
	}
	done()
	if acc.AccountID != accounts[0].AccountID {
		t.Fatal("new credentials did not clear old invalid state")
	}
}

func TestBusinessWithoutPremiumIsNotLifetimeExhausted(t *testing.T) {
	pool := routingPool(1)
	acc := pool.AccountsSnapshot()[0]
	pool.applyQuotaInfo(acc, &QuotaInfo{IsEligible: false})
	if acc.quotaSnapshot().PermanentlyExhausted {
		t.Fatal("business without premium was made permanently unavailable")
	}
	pool.applyQuotaInfo(acc, &QuotaInfo{IsEligible: true})
	if pool.GetByAccountID(acc.AccountID) == nil {
		t.Fatal("business did not recover after confirmed eligibility")
	}
}

func TestRoutingFairnessAmongEligibleMembers(t *testing.T) {
	for _, mode := range []string{"quota", "round_robin"} {
		t.Run(mode, func(t *testing.T) {
			pool := routingPool(5)
			accounts := pool.AccountsSnapshot()
			if err := pool.UpdateRouting(RoutingConfig{Mode: mode, DisabledAccountIDs: []string{accounts[1].AccountID, accounts[2].AccountID, accounts[3].AccountID}}); err != nil {
				t.Fatal(err)
			}
			seen := make(map[string]int)
			for i := 0; i < 10; i++ {
				acc, release, err := pool.SelectForRequest("", false, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				seen[acc.AccountID]++
				release()
			}
			if seen[accounts[0].AccountID] != 5 || seen[accounts[4].AccountID] != 5 {
				t.Fatalf("filtered pool was unfair: %v", seen)
			}
		})
	}
}

func TestReloadRequiresBusinessOnlyForNewMembers(t *testing.T) {
	dir := writeTempAccounts(t, baseAccount("legacy", "space-old", "same@example.com", "Legacy"))
	pool := NewAccountPool()
	if err := pool.LoadFromDir(dir); err != nil {
		t.Fatal(err)
	}
	for _, space := range []string{"space-trial", "space-paid"} {
		data, _ := json.Marshal(baseAccount("new", space, "same@example.com", "New"))
		if err := os.WriteFile(filepath.Join(dir, space+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldChecker := businessSubscriptionChecker
	t.Cleanup(func() { businessSubscriptionChecker = oldChecker })
	checks := 0
	businessSubscriptionChecker = func(acc *Account) error {
		checks++
		if acc.UserID == "legacy" {
			t.Fatal("legacy account was reverified")
		}
		if pool.Count() < 1 {
			t.Fatal("pool unavailable during subscription check")
		}
		if acc.SpaceID == "space-trial" {
			return errors.New("fixture trial")
		}
		return nil
	}
	pool.ReloadFromDir(dir)
	if checks != 2 || pool.Count() != 2 {
		t.Fatalf("checks=%d members=%d", checks, pool.Count())
	}
	if _, err := os.Stat(filepath.Join(dir, "space-trial.json")); err != nil {
		t.Fatal("rejected account file removed")
	}
}

func TestLoadSingleRequiresBusinessAndDiscoversPlainTokenIdentity(t *testing.T) {
	oldChecker, oldDiscover := businessSubscriptionChecker, singleAccountDiscoverer
	t.Cleanup(func() { businessSubscriptionChecker = oldChecker; singleAccountDiscoverer = oldDiscover })
	singleAccountDiscoverer = func(token string) (*Account, error) {
		if token != "fixture-token" {
			t.Fatal("plain token not normalized")
		}
		return &Account{TokenV2: token, UserID: "actual-user", SpaceID: "actual-space"}, nil
	}
	for _, body := range []string{`{"token_v2":"fixture-token","user_id":"actual-user","space_id":"actual-space"}`, " fixture-token\n"} {
		path := filepath.Join(t.TempDir(), "token.txt")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		pool := NewAccountPool()
		businessSubscriptionChecker = func(*Account) error { return errors.New("fixture trial") }
		if err := pool.LoadSingle(path); err == nil || pool.Count() != 0 {
			t.Fatal("non-Business token file entered pool")
		}
		businessSubscriptionChecker = func(acc *Account) error {
			if acc.UserID != "actual-user" || acc.SpaceID != "actual-space" {
				t.Fatal("invented token-file identity")
			}
			now := time.Now()
			acc.BusinessVerifiedAt = &now
			return nil
		}
		if err := pool.LoadSingle(path); err != nil {
			t.Fatal(err)
		}
		if pool.Count() != 1 || pool.GetAccountDetails()[0]["subscription_status"] != "verified_business" {
			t.Fatal("verified token-file member not reflected")
		}
	}
	legacy := routingPool(1)
	if legacy.GetAccountDetails()[0]["subscription_status"] != "legacy_unverified" || legacy.RoutingSnapshot().Available != 1 {
		t.Fatal("legacy business label became verified or unavailable")
	}
}

func TestRoutingDynamicMembershipDoesNotShrinkInflightRequestBudget(t *testing.T) {
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	pool.RemoveAccountByAccountID(accounts[0].AccountID)
	acc, done, err := pool.SelectForRequest("", false, map[string]bool{accounts[0].AccountID: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	done()
	if acc != accounts[1] {
		t.Fatal("removing a tried member blocked a healthy member")
	}
	pool.AddAccount(&Account{UserID: "new-user", SpaceID: "new-space", PlanType: "business", QuotaInfo: &QuotaInfo{IsEligible: true}})
	if pool.RoutingSnapshot().Available != 2 {
		t.Fatal("new member did not join without a backup list")
	}
}

func TestRoutingUnavailableReportsReasonAndNextCheck(t *testing.T) {
	pool := routingPool(1)
	acc := pool.AccountsSnapshot()[0]
	reset := time.Now().Add(time.Hour)
	setRoutingAllowance(acc, 100, 0, reset)
	_, _, err := pool.SelectForRequest("", false, nil, "")
	var unavailable *RoutingSelectionError
	if !errors.Is(err, ErrNoRoutingAccount) || !errors.As(err, &unavailable) || unavailable.RetryAtMs != reset.UnixMilli() || unavailable.Reasons["allowance_exhausted"] != 1 {
		t.Fatalf("missing routing explanation: %v", err)
	}
}

func TestRefreshAccountQuotaRejectsFreshAndCachedAllowanceExhaustion(t *testing.T) {
	oldFetcher := quotaFetcher
	t.Cleanup(func() { quotaFetcher = oldFetcher })
	for _, monthly := range []bool{false, true} {
		t.Run(fmt.Sprint(monthly), func(t *testing.T) {
			pool := routingPool(1)
			acc := pool.AccountsSnapshot()[0]
			fixture := routingPool(1).AccountsSnapshot()[0]
			rollingUsed, monthlyUsed := 100.0, 0.0
			if monthly {
				rollingUsed, monthlyUsed = 0, 100
			}
			setRoutingAllowance(fixture, rollingUsed, monthlyUsed, time.Now().Add(time.Hour))
			quotaFetcher = func(*Account) (*QuotaInfo, error) { return fixture.quotaInfoSnapshot(), nil }
			if pool.RefreshAccountQuota(acc, 0) {
				t.Fatal("newly exhausted allowance passed live precheck")
			}
			if pool.RefreshAccountQuota(acc, time.Minute) {
				t.Fatal("cached exhausted allowance passed precheck")
			}
			quotaFetcher = func(*Account) (*QuotaInfo, error) { return nil, errors.New("fixture failure") }
			if pool.RefreshAccountQuota(acc, 0) {
				t.Fatal("quota fetch error bypassed cached exhaustion")
			}
		})
	}
}

func TestRoutingRoundRobinUsesEntireDynamicPool(t *testing.T) {
	for _, size := range []int{1, 2, 5, 8} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			pool := routingPool(size)
			if err := pool.UpdateRouting(RoutingConfig{Mode: "round_robin", RetentionDays: 30}); err != nil {
				t.Fatal(err)
			}
			seen := make(map[string]int)
			for i := 0; i < size*3; i++ {
				acc, release, err := pool.SelectForRequest("", false, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				seen[acc.AccountID]++
				release()
			}
			if len(seen) != size {
				t.Fatalf("visited %d of %d members", len(seen), size)
			}
			for id, count := range seen {
				if count != 3 {
					t.Fatalf("member %s received %d requests, want 3", id, count)
				}
			}
		})
	}
}
