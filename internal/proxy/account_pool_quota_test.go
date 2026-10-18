package proxy

import (
	"testing"
	"time"
)

// newPool builds an AccountPool with deduplicated init for tests.
func newPool(accs ...*Account) *AccountPool {
	p := NewAccountPool()
	p.accounts = accs
	return p
}

func TestNextBestPicksHighestRemainingQuota(t *testing.T) {
	low := &Account{
		UserEmail: "low@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 180, UserLimit: 200, UserUsage: 180},
	}
	mid := &Account{
		UserEmail: "mid@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 100, UserLimit: 200, UserUsage: 100},
	}
	high := &Account{
		UserEmail: "high@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 20, UserLimit: 200, UserUsage: 20},
	}
	pool := newPool(low, mid, high)

	got := pool.NextBest()
	if got == nil || got.UserEmail != "high@example.com" {
		t.Fatalf("expected highest-remaining account, got %#v", got)
	}
}

func TestNextBestExcludingSkipsTriedAndPicksNextHighest(t *testing.T) {
	a := &Account{
		UserEmail: "a@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 20, UserLimit: 200, UserUsage: 20}, // remaining 180
	}
	b := &Account{
		UserEmail: "b@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 100, UserLimit: 200, UserUsage: 100}, // remaining 100
	}
	c := &Account{
		UserEmail: "c@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 160, UserLimit: 200, UserUsage: 160}, // remaining 40
	}
	pool := newPool(a, b, c)

	// First pick should be 'a' (highest remaining).
	first := pool.NextBest()
	if first == nil || first.UserEmail != "a@example.com" {
		t.Fatalf("expected first NextBest to be 'a', got %#v", first)
	}

	// After excluding 'a', the next best should be 'b'.
	tried := map[*Account]bool{a: true}
	second := pool.NextBestExcluding(tried)
	if second == nil || second.UserEmail != "b@example.com" {
		t.Fatalf("expected second NextBestExcluding to be 'b', got %#v", second)
	}

	// Excluding 'a' and 'b' leaves 'c'.
	tried[b] = true
	third := pool.NextBestExcluding(tried)
	if third == nil || third.UserEmail != "c@example.com" {
		t.Fatalf("expected third NextBestExcluding to be 'c', got %#v", third)
	}

	// All excluded → nil
	tried[c] = true
	if got := pool.NextBestExcluding(tried); got != nil {
		t.Fatalf("expected nil when all accounts excluded, got %#v", got)
	}
}

func TestNextBestFallsBackToUnknownQuota(t *testing.T) {
	// When no account has measurable quota (QuotaInfo == nil), the pool
	// should still return the first usable one rather than refusing to
	// serve.
	a := &Account{UserEmail: "a@example.com"}
	b := &Account{UserEmail: "b@example.com"}
	pool := newPool(a, b)

	got := pool.NextBest()
	if got == nil {
		t.Fatal("expected fallback when no quota info present, got nil")
	}
	if got != a && got != b {
		t.Fatalf("expected one of the seeded accounts, got %#v", got)
	}
}

func TestNextBestPrefersScoredOverUnknownQuota(t *testing.T) {
	known := &Account{
		UserEmail: "known@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 50, UserLimit: 200, UserUsage: 50}, // remaining 150
	}
	unknown := &Account{UserEmail: "unknown@example.com"}
	pool := newPool(unknown, known) // unknown listed first to defeat ordering bias

	got := pool.NextBest()
	if got == nil || got.UserEmail != "known@example.com" {
		t.Fatalf("expected known-quota account to be preferred, got %#v", got)
	}
}

func TestNextBestSkipsExhaustedAccounts(t *testing.T) {
	exhausted := &Account{
		UserEmail: "exhausted@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: false, SpaceLimit: 200, SpaceUsage: 200, UserLimit: 200, UserUsage: 200},
	}
	healthy := &Account{
		UserEmail: "healthy@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 50, UserLimit: 200, UserUsage: 50},
	}
	pool := newPool(exhausted, healthy)

	got := pool.NextBest()
	if got == nil || got.UserEmail != "healthy@example.com" {
		t.Fatalf("expected exhausted account to be skipped, got %#v", got)
	}
}

func TestAccountQuotaPriorityFoldsPremium(t *testing.T) {
	basicOnly := &Account{
		UserEmail: "basic@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 50, UserLimit: 200, UserUsage: 50}, // 150
	}
	premiumLowBasic := &Account{
		UserEmail: "premium@example.com",
		QuotaInfo: &QuotaInfo{
			IsEligible:          true,
			HasPremium:          true,
			PremiumBalance:      10000,
			PremiumBalanceKnown: true,
			SpaceLimit:          200,
			SpaceUsage:          190,
			UserLimit:           200,
			UserUsage:           190, // basic remaining only 10
		},
	}

	if accountQuotaPriority(basicOnly) != 150 {
		t.Fatalf("basic-only score: want 150, got %d", accountQuotaPriority(basicOnly))
	}
	if got := accountQuotaPriority(premiumLowBasic); got <= 150 {
		t.Fatalf("premium with low basic should score higher than basic-only 150, got %d", got)
	}

	pool := newPool(basicOnly, premiumLowBasic)
	got := pool.NextBest()
	if got == nil || got.UserEmail != "premium@example.com" {
		t.Fatalf("expected premium account to win, got %#v", got)
	}
}

func TestRefreshAccountQuotaCacheHitSkipsHTTP(t *testing.T) {
	// QuotaCheckedAt was set very recently, so RefreshAccountQuota must
	// take the cached fast path and never reach CheckQuota (which would
	// hit the network and panic since this account has no real token).
	now := time.Now()
	acc := &Account{
		UserEmail:      "cached@example.com",
		TokenV2:        "fake-token-do-not-use",
		QuotaCheckedAt: &now,
		QuotaInfo:      &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 0, UserLimit: 200, UserUsage: 0},
	}
	pool := newPool(acc)

	// 60s minInterval ensures the freshly recorded check counts as fresh.
	if !pool.RefreshAccountQuota(acc, 60*time.Second) {
		t.Fatal("expected cached eligible result to return true")
	}

	acc.QuotaInfo.IsEligible = false
	if pool.RefreshAccountQuota(acc, 60*time.Second) {
		t.Fatal("expected cached non-eligible result to return false")
	}
}

func TestRefreshAccountQuotaNilAccount(t *testing.T) {
	pool := newPool()
	if pool.RefreshAccountQuota(nil, 5*time.Second) {
		t.Fatal("nil account must not be considered eligible")
	}
}

func TestApplyQuotaInfoMarksAndClearsExhaustion(t *testing.T) {
	pool := NewAccountPool()
	now := time.Now()
	acc := &Account{
		UserEmail:        "victim@example.com",
		PlanType:         "personal",
		QuotaExhaustedAt: &now,
	}

	// Eligible result clears the exhaustion mark.
	pool.applyQuotaInfo(acc, &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 10, UserLimit: 200, UserUsage: 10})
	if acc.QuotaExhaustedAt != nil {
		t.Fatalf("expected QuotaExhaustedAt cleared after recovery, got %v", acc.QuotaExhaustedAt)
	}
	if acc.PermanentlyExhausted {
		t.Fatal("recovered free account should no longer be permanent")
	}
	if acc.QuotaInfo == nil || !acc.QuotaInfo.IsEligible {
		t.Fatal("expected QuotaInfo updated to eligible state")
	}

	// Non-eligible result on a free plan flips the permanent flag.
	pool.applyQuotaInfo(acc, &QuotaInfo{IsEligible: false, SpaceLimit: 200, SpaceUsage: 200, UserLimit: 200, UserUsage: 200})
	if acc.QuotaExhaustedAt == nil {
		t.Fatal("expected QuotaExhaustedAt set after exhaustion")
	}
	if !acc.PermanentlyExhausted {
		t.Fatal("free plan exhaustion should be permanent")
	}

	// Paid plan (premium-bearing) exhaustion only marks timestamp.
	// isFreePlan treats team accounts without premium as free, so the
	// new QuotaInfo must keep the premium signal alive to be recognised
	// as paid even when basic credits hit zero.
	paid := &Account{UserEmail: "biz@example.com", PlanType: "business"}
	pool.applyQuotaInfo(paid, &QuotaInfo{
		IsEligible:     false,
		HasPremium:     true,
		PremiumBalance: 0,
		PremiumLimit:   1000,
		SpaceLimit:     200, SpaceUsage: 200,
		UserLimit: 200, UserUsage: 200,
	})
	if paid.PermanentlyExhausted {
		t.Fatal("business plan with premium credits must not be marked permanent on exhaustion")
	}
	if paid.QuotaExhaustedAt == nil {
		t.Fatal("paid plan should still have QuotaExhaustedAt set")
	}
}

func TestCloneChatMessagesIsDeepCopy(t *testing.T) {
	src := []ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hi"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "lookup", Arguments: `{"q":"x"}`}},
				{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "noop", Arguments: `{}`}},
			},
		},
	}

	clone := cloneChatMessages(src)
	if len(clone) != len(src) {
		t.Fatalf("expected length %d, got %d", len(src), len(clone))
	}

	// Mutating the clone must not affect the source.
	clone[0].Content = "tampered"
	if src[0].Content != "you are helpful" {
		t.Fatalf("source content was mutated through clone: %q", src[0].Content)
	}

	// ToolCalls slice should be a fresh allocation.
	clone[2].ToolCalls[0].ID = "tampered_id"
	if src[2].ToolCalls[0].ID != "call_1" {
		t.Fatalf("source ToolCalls was mutated through clone: %q", src[2].ToolCalls[0].ID)
	}
	clone[2].ToolCalls = append(clone[2].ToolCalls, ToolCall{ID: "extra"})
	if len(src[2].ToolCalls) != 2 {
		t.Fatalf("source ToolCalls slice was extended through clone: len=%d", len(src[2].ToolCalls))
	}
}

func TestCloneChatMessagesNilInput(t *testing.T) {
	if got := cloneChatMessages(nil); got != nil {
		t.Fatalf("expected nil clone for nil input, got %v", got)
	}
}

// applyQuotaInfo's return value drives the refresh-loop log lines that tell
// operators which accounts were just disabled. Lock the contract here so the
// startup "kick out exhausted accounts" behaviour stays observable.
func TestApplyQuotaInfoReturnsTransitionForExhaustedFreePlan(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserEmail: "free@example.com", PlanType: "personal"}

	res := pool.applyQuotaInfo(acc, &QuotaInfo{
		IsEligible: false,
		SpaceLimit: 200, SpaceUsage: 200,
		UserLimit: 200, UserUsage: 200,
	})

	if !res.NowExhausted {
		t.Fatal("expected NowExhausted=true")
	}
	if !res.NowPermanent {
		t.Fatal("expected NowPermanent=true for free plan exhaustion")
	}
	if res.Recovered {
		t.Fatal("expected Recovered=false on first exhaustion")
	}
	if !pool.isQuotaExhausted(acc) {
		t.Fatal("expected pool to consider account exhausted after applyQuotaInfo")
	}
}

func TestApplyQuotaInfoReturnsTransitionForRecovery(t *testing.T) {
	pool := NewAccountPool()
	now := time.Now()
	acc := &Account{
		UserEmail:        "back@example.com",
		PlanType:         "personal",
		QuotaExhaustedAt: &now,
	}

	res := pool.applyQuotaInfo(acc, &QuotaInfo{
		IsEligible: true,
		SpaceLimit: 200, SpaceUsage: 50,
		UserLimit: 200, UserUsage: 50,
	})

	if !res.Recovered {
		t.Fatal("expected Recovered=true when previously-exhausted account becomes eligible")
	}
	if res.NowExhausted || res.NowPermanent {
		t.Fatal("recovery transition must clear exhaustion flags")
	}
	if pool.isQuotaExhausted(acc) {
		t.Fatal("recovered account must not be reported exhausted")
	}
}

func TestApplyQuotaInfoNoTransitionWhenStillEligible(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserEmail: "stable@example.com", PlanType: "business"}

	res := pool.applyQuotaInfo(acc, &QuotaInfo{
		IsEligible: true,
		HasPremium: true,
		SpaceLimit: 200, SpaceUsage: 10,
		UserLimit: 200, UserUsage: 10,
	})

	if res.Recovered || res.NowExhausted || res.NowPermanent {
		t.Fatalf("expected no transitions, got %+v", res)
	}
	if res.BasicLeft != 190 {
		t.Fatalf("expected BasicLeft=190, got %d", res.BasicLeft)
	}
	if !res.HasPremium {
		t.Fatal("expected HasPremium=true echoed back")
	}
}

// Once an account is disabled by applyQuotaInfo, all selectors must skip it.
// This guards the startup "disable exhausted on refresh" promise: if a worker
// just marked an account exhausted, no concurrent NextBest/Next/etc. call may
// still hand it back to a request.
func TestSelectorsSkipAccountAfterApplyQuotaInfo(t *testing.T) {
	pool := NewAccountPool()
	exhausted := &Account{UserEmail: "drained@example.com", PlanType: "personal"}
	healthy := &Account{
		UserEmail: "healthy@example.com",
		QuotaInfo: &QuotaInfo{IsEligible: true, SpaceLimit: 200, SpaceUsage: 10, UserLimit: 200, UserUsage: 10},
	}
	pool.accounts = []*Account{exhausted, healthy}

	pool.applyQuotaInfo(exhausted, &QuotaInfo{
		IsEligible: false,
		SpaceLimit: 200, SpaceUsage: 200,
		UserLimit: 200, UserUsage: 200,
	})

	if got := pool.NextBest(); got == nil || got.UserEmail != "healthy@example.com" {
		t.Fatalf("NextBest should skip newly-disabled account, got %#v", got)
	}
	if got := pool.Next(); got == nil || got.UserEmail != "healthy@example.com" {
		t.Fatalf("Next should skip newly-disabled account, got %#v", got)
	}
	if got := pool.GetByEmail("drained@example.com"); got != nil {
		t.Fatalf("GetByEmail must not return disabled account, got %#v", got)
	}
	if got := pool.AvailableCount(); got != 1 {
		t.Fatalf("expected AvailableCount=1, got %d", got)
	}
}
