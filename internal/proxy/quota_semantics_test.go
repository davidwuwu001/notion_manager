package proxy

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPremiumBalancePreservesExplicitZeroAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, balance string
		known         bool
		value         int
	}{
		{"zero", `"totalCreditBalance":0,`, true, 0},
		{"positive", `"totalCreditBalance":27,`, true, 27},
		{"missing", ``, false, 0},
		{"null", `"totalCreditBalance":null,`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var v2 quotaV2Response
			body := `{"premiumCredits":{` + tc.balance + `"perSource":{"monthlyAllocated":{"usageTotal":0,"limit":300}}}}`
			if err := json.Unmarshal([]byte(body), &v2); err != nil {
				t.Fatal(err)
			}
			info := mergeQuotaResponses(quotaV1Response{IsEligible: true}, v2)
			if info.PremiumBalanceKnown != tc.known || info.PremiumBalance != tc.value {
				t.Fatalf("incorrect reported balance: %+v", info)
			}
			if info.PremiumLimit != 300 || info.PremiumUsage != 0 {
				t.Fatal("monthly allocation lost")
			}
			if tc.known && tc.value == 0 && info.QuotaStatusReason != "premium_balance_conflict" {
				t.Fatal("zero/allocation conflict was hidden")
			}
		})
	}
}

func TestPremiumKnownFlagSurvivesAccountSaveAndReload(t *testing.T) {
	for _, known := range []bool{false, true} {
		acc := &Account{TokenV2: "fixture", UserID: "u", SpaceID: "s", PlanType: "business", QuotaInfo: &QuotaInfo{IsEligible: true, HasPremium: true, PremiumBalanceKnown: known, PremiumBalance: 0, PremiumLimit: 300}}
		dir := t.TempDir()
		if _, err := SaveAccountToFile(acc, dir); err != nil {
			t.Fatal(err)
		}
		pool := NewAccountPool()
		if err := pool.LoadFromDir(dir); err != nil {
			t.Fatal(err)
		}
		restored := pool.AccountsSnapshot()[0].quotaInfoSnapshot()
		if restored.PremiumBalanceKnown != known || restored.PremiumBalance != 0 {
			t.Fatalf("lost explicit balance state: %+v", restored)
		}
	}
}

func TestUnknownLegacyPremiumDoesNotBecomeAvailableBalance(t *testing.T) {
	info := loadPersistedQuotaInfo([]byte(`{"quota_info":{"is_eligible":true,"has_premium":true,"premium_balance":300,"premium_limit":300}}`))
	if info.PremiumBalanceKnown {
		t.Fatal("historical estimated balance became verified")
	}
	pool := NewAccountPool()
	pool.AddAccount(&Account{UserID: "fixture-u", SpaceID: "fixture-s", PlanType: "business", QuotaInfo: info})
	entry := pool.GetAccountDetails()[0]
	if entry["premium_balance"] != nil || entry["premium_balance_known"] != false {
		t.Fatalf("unknown balance exposed as known: %v", entry)
	}
	if score, source := routingQuotaScore(pool.AccountsSnapshot()[0]); score >= 0 || source != "legacy_premium_unknown" {
		t.Fatalf("unknown legacy balance scored %v via %s", score, source)
	}
	summary := summarizeAccounts(pool.GetAccountDetails())
	if summary.PremiumUnknownAccounts != 1 || summary.TotalPremiumBalance != 0 {
		t.Fatalf("unknown aggregate: %+v", summary)
	}
	known := loadPersistedQuotaInfo([]byte(`{"quota_info":{"is_eligible":true,"has_premium":true,"premium_balance":0,"premium_balance_known":true,"premium_limit":300}}`))
	if !known.PremiumBalanceKnown || known.PremiumBalance != 0 {
		t.Fatal("explicit zero did not survive persistence")
	}
	for _, body := range []string{`{"quota_info":{"premium_balance_known":true}}`, `{"quota_info":{"premium_balance_known":true,"premium_balance":null}}`} {
		if loadPersistedQuotaInfo([]byte(body)).PremiumBalanceKnown {
			t.Fatal("missing persisted balance became known zero")
		}
	}
}

func TestQuotaReadFailureKeepsKnownBalanceAndCheckTime(t *testing.T) {
	old := quotaFetcher
	t.Cleanup(func() { quotaFetcher = old })
	quotaFetcher = func(*Account) (*QuotaInfo, error) { return nil, errors.New("fixture read failed") }
	pool := NewAccountPool()
	checked := time.Now().Add(-time.Minute)
	acc := &Account{UserID: "u", SpaceID: "s", PlanType: "business"}
	acc.setQuotaInfo(&QuotaInfo{IsEligible: true, HasPremium: true, PremiumBalanceKnown: true, PremiumBalance: 42, PremiumLimit: 300}, &checked)
	pool.AddAccount(acc)
	if !pool.RefreshAccountQuota(acc, 0) {
		t.Fatal("failed refresh discarded cached eligibility")
	}
	got := acc.quotaSnapshot()
	if !got.Info.PremiumBalanceKnown || got.Info.PremiumBalance != 42 || !got.CheckedAt.Equal(checked) {
		t.Fatal("failed refresh changed cached balance or timestamp")
	}
}

func TestModernWindowDoesNotRewriteLegacyEligibility(t *testing.T) {
	for _, eligible := range []bool{true, false} {
		info := mergeQuotaResponses(quotaV1Response{IsEligible: eligible, SpaceLimit: 200, SpaceUsage: 600}, quotaV2Response{})
		info.RateLimit = &CreditRateLimit{}
		info.RateLimit.Window.Limit = 100
		info.RateLimit.Window.Used = 10
		info.QuotaStatusReason = quotaStatusReason(info)
		pool := NewAccountPool()
		acc := &Account{UserID: "u", SpaceID: "s", PlanType: "business"}
		pool.AddAccount(acc)
		pool.applyQuotaInfo(acc, info)
		if acc.quotaSnapshot().PermanentlyExhausted {
			t.Fatal("Business permanently disabled by old quota data")
		}
		if info.IsEligible != eligible {
			t.Fatal("window silently rewrote V1 eligibility")
		}
		if !eligible && info.QuotaStatusReason != "eligibility_conflict" {
			t.Fatal("eligibility conflict hidden")
		}
		if eligible && !accountHasInferenceQuota(acc) {
			t.Fatal("old Basic usage overruled eligible modern window")
		}
	}
}
