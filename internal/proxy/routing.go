package proxy

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoRoutingAccount       = errors.New("no eligible account in the pool")
	ErrRoutingBusy            = errors.New("all eligible accounts are busy")
	ErrRoutingBudgetExhausted = errors.New("account attempt budget exhausted")
)

type RoutingSelectionError struct {
	Cause     error
	RetryAtMs int64
	Reasons   map[string]int
}

func (err *RoutingSelectionError) Error() string {
	message := fmt.Sprintf("%v (%v)", err.Cause, err.Reasons)
	if err.RetryAtMs > 0 {
		message += "; next check " + time.UnixMilli(err.RetryAtMs).UTC().Format(time.RFC3339)
	}
	return message
}

func (err *RoutingSelectionError) Unwrap() error { return err.Cause }

type RoutingConfig struct {
	Mode                    string   `json:"mode"`
	MaxAttempts             int      `json:"max_attempts"`
	MaxConcurrentPerAccount int      `json:"max_concurrent_per_account"`
	DisabledAccountIDs      []string `json:"disabled_account_ids"`
	RetentionDays           int      `json:"retention_days"`
}

type RoutingAccountStatus struct {
	AccountID        string `json:"account_id"`
	Enabled          bool   `json:"enabled"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	ScoreSource      string `json:"score_source"`
	QuotaCheckedAtMs int64  `json:"quota_checked_at_ms,omitempty"`
	CooldownUntilMs  int64  `json:"cooldown_until_ms,omitempty"`
	InFlight         int    `json:"in_flight"`
}

type RoutingStatus struct {
	RoutingConfig
	Total     int                    `json:"total"`
	Available int                    `json:"available"`
	Paused    int                    `json:"paused"`
	Exhausted int                    `json:"exhausted"`
	Cooldown  int                    `json:"cooldown"`
	Invalid   int                    `json:"invalid"`
	Busy      int                    `json:"busy"`
	Accounts  []RoutingAccountStatus `json:"accounts"`
}

type routingRuntime struct {
	inFlight          int
	account           *Account
	reason            string
	until             time.Time
	recheckAt         time.Time
	rechecking        bool
	models            map[string]bool
	researchExhausted bool
}

func normalizedRoutingConfig(config RoutingConfig) (RoutingConfig, error) {
	if config.Mode == "" {
		config.Mode = "quota"
	}
	if config.Mode != "quota" && config.Mode != "round_robin" {
		return config, fmt.Errorf("unsupported routing mode")
	}
	if config.MaxAttempts < 0 || config.MaxConcurrentPerAccount < 0 || config.RetentionDays < 0 || config.RetentionDays > 3650 {
		return config, fmt.Errorf("invalid routing limits")
	}
	if config.RetentionDays == 0 {
		config.RetentionDays = 30
	}
	config.DisabledAccountIDs = append([]string{}, config.DisabledAccountIDs...)
	seen := make(map[string]bool)
	for _, id := range config.DisabledAccountIDs {
		if !isAccountID(id) || seen[id] {
			return config, fmt.Errorf("invalid or duplicate disabled account ID")
		}
		seen[id] = true
	}
	return config, nil
}

func (p *AccountPool) routingConfigLocked() RoutingConfig {
	config, _ := normalizedRoutingConfig(p.routingConfig)
	return config
}

func (p *AccountPool) UpdateRouting(config RoutingConfig) error {
	config, err := normalizedRoutingConfig(config)
	if err != nil {
		return err
	}
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	if p.routingPath != "" {
		if err := saveRoutingConfig(p.routingPath, config); err != nil {
			return fmt.Errorf("save routing config: %w", err)
		}
	}
	p.routingConfig = config
	return nil
}

func (p *AccountPool) SelectForRequest(model string, research bool, excluded map[string]bool, boundID string) (*Account, func(), error) {
	accounts := p.AccountsSnapshot()
	p.recheckRoutingAccounts(accounts)
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	config := p.routingConfigLocked()
	if len(accounts) == 0 {
		return nil, nil, ErrNoRoutingAccount
	}
	budget := config.MaxAttempts
	tried := 0
	for _, value := range excluded {
		if value {
			tried++
		}
	}
	if budget == 0 {
		budget = len(accounts)
		tried = 0
		for _, acc := range accounts {
			if excluded[acc.AccountID] {
				tried++
			}
		}
	}
	if tried >= budget {
		return nil, nil, ErrRoutingBudgetExhausted
	}
	if p.routingStates == nil {
		p.routingStates = make(map[string]*routingRuntime)
	}
	var candidates []*Account
	bestScore := -2.0
	busy := false
	reasons := make(map[string]int)
	var nextCheck int64
	for _, acc := range accounts {
		acc.EnsureAccountID()
		if excluded[acc.AccountID] {
			continue
		}
		state := p.routingStateLocked(acc)
		status := p.routingAccountStatusLocked(acc, config, time.Now())
		if !routingSupportsModel(acc, model, research, state) {
			reasons["model_or_research_unavailable"]++
			continue
		}
		if status.CooldownUntilMs > 0 && (nextCheck == 0 || status.CooldownUntilMs < nextCheck) {
			nextCheck = status.CooldownUntilMs
		}
		if status.Status != "available" {
			reasons[status.Reason]++
		}
		if status.Status == "busy" {
			busy = true
			continue
		}
		if status.Status != "available" {
			continue
		}
		if acc.AccountID == boundID {
			candidates = []*Account{acc}
			break
		}
		score, _ := routingQuotaScore(acc)
		if config.Mode == "round_robin" {
			score = 0
		}
		if len(candidates) == 0 || score > bestScore {
			candidates = []*Account{acc}
			bestScore = score
		} else if score == bestScore {
			candidates = append(candidates, acc)
		}
	}
	if len(candidates) == 0 {
		cause := ErrNoRoutingAccount
		if busy {
			cause = ErrRoutingBusy
		}
		return nil, nil, &RoutingSelectionError{Cause: cause, RetryAtMs: nextCheck, Reasons: reasons}
	}
	best := candidates[(p.index.Add(1)-1)%uint64(len(candidates))]
	state := p.routingStates[best.AccountID]
	state.inFlight++
	var once sync.Once
	release := func() { once.Do(func() { p.routingMu.Lock(); state.inFlight--; p.routingMu.Unlock() }) }
	return best, release, nil
}

func (p *AccountPool) routingStateLocked(acc *Account) *routingRuntime {
	if p.routingStates == nil {
		p.routingStates = make(map[string]*routingRuntime)
	}
	state := p.routingStates[acc.AccountID]
	if state == nil {
		state = &routingRuntime{}
		p.routingStates[acc.AccountID] = state
	}
	if state.account != acc {
		*state = routingRuntime{account: acc, inFlight: state.inFlight}
	}
	return state
}

// SetRoutingFailure accepts classified codes, never raw upstream error text.
func (p *AccountPool) SetRoutingFailure(accountID, model, reason string, until time.Time) {
	acc := p.FindByAccountID(accountID)
	if acc == nil {
		return
	}
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	state := p.routingStateLocked(acc)
	switch reason {
	case "model_unavailable":
		if state.models == nil {
			state.models = make(map[string]bool)
		}
		state.models[ResolveModel(model)] = true
	case "research_exhausted":
		state.researchExhausted = true
	case "invalid_credentials":
		state.reason = reason
		state.until = time.Time{}
	case "rate_limited", "transient":
		state.reason = reason
		if until.IsZero() {
			until = time.Now().Add(5 * time.Second)
		}
		state.until = until
		state.recheckAt = until
	}
}

func (p *AccountPool) routingQuotaUpdated(acc *Account, info *QuotaInfo) {
	if info == nil {
		return
	}
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	state := p.routingStates[acc.AccountID]
	if state == nil || state.account != acc {
		return
	}
	if info.HasPremium || info.ResearchModeUsage < 3 {
		state.researchExhausted = false
	}
	blocked, _ := routingAllowanceBlocked(info)
	if info.IsEligible && !blocked && !state.until.After(time.Now()) && (state.reason == "rate_limited" || state.reason == "transient") {
		state.reason = ""
		state.until = time.Time{}
		state.recheckAt = time.Time{}
	}
}

func (p *AccountPool) routingModelsUpdated(acc *Account) {
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	if state := p.routingStates[acc.AccountID]; state != nil && state.account == acc {
		state.models = nil
	}
}

func routingQuotaScore(acc *Account) (float64, string) {
	info := acc.quotaInfoSnapshot()
	if info == nil {
		return -1, "unknown"
	}
	if limit := info.RateLimit; limit != nil {
		score := 1.0
		known := false
		for _, window := range [][2]float64{{limit.Window.Used, limit.Window.Limit}, {limit.BillingPeriodWindow.Used, limit.BillingPeriodWindow.Limit}} {
			if window[1] > 0 && !math.IsNaN(window[0]) && !math.IsInf(window[0], 0) && !math.IsInf(window[1], 0) {
				score = math.Min(score, math.Max(0, 1-window[0]/window[1]))
				known = true
			}
		}
		if known {
			return score, "credit_rate_limit"
		}
	}
	if info.HasPremium && !info.PremiumBalanceKnown {
		return -1, "legacy_premium_unknown"
	}
	if info.HasPremium && info.PremiumLimit > 0 {
		return math.Min(1, math.Max(0, float64(info.PremiumBalance)/float64(info.PremiumLimit))), "legacy_premium"
	}
	score := 1.0
	known := false
	for _, window := range [][2]int{{info.SpaceUsage, info.SpaceLimit}, {info.UserUsage, info.UserLimit}} {
		if window[1] > 0 {
			score = math.Min(score, math.Max(0, 1-float64(window[0])/float64(window[1])))
			known = true
		}
	}
	if known {
		return score, "legacy_basic"
	}
	return -1, "unknown"
}

func routingAllowanceBlocked(info *QuotaInfo) (bool, time.Time) {
	if info == nil || info.RateLimit == nil {
		return false, time.Time{}
	}
	limit := info.RateLimit
	blocked := false
	var reset time.Time
	for _, window := range []struct {
		used, limit float64
		reset       int64
	}{
		{limit.Window.Used, limit.Window.Limit, limit.RollingResetAtMs},
		{limit.BillingPeriodWindow.Used, limit.BillingPeriodWindow.Limit, limit.BillingPeriodWindow.PeriodEndMs},
	} {
		if window.limit > 0 && window.used >= window.limit {
			blocked = true
			if window.reset > 0 && (reset.IsZero() || time.UnixMilli(window.reset).After(reset)) {
				reset = time.UnixMilli(window.reset)
			}
		}
	}
	return blocked, reset
}

func routingSupportsModel(acc *Account, model string, research bool, state *routingRuntime) bool {
	if research {
		quota := acc.quotaInfoSnapshot()
		return !state.researchExhausted && (quota == nil || quota.HasPremium || quota.ResearchModeUsage < 3)
	}
	model, _ = StripAskModeSuffix(strings.TrimSpace(model))
	if model == "" {
		return true
	}
	resolved := ResolveModel(model)
	if state.models[resolved] {
		return false
	}
	models := acc.modelsSnapshot()
	if len(models) == 0 {
		return true
	}
	for _, entry := range models {
		if entry.ID == resolved || entry.ID == model || normalizeModelName(entry.Name) == normalizeModelName(model) {
			return true
		}
	}
	return false
}

func (p *AccountPool) routingAccountStatusLocked(acc *Account, config RoutingConfig, now time.Time) RoutingAccountStatus {
	state := p.routingStateLocked(acc)
	_, source := routingQuotaScore(acc)
	status := RoutingAccountStatus{AccountID: acc.AccountID, Enabled: true, Status: "available", Reason: "", ScoreSource: source, InFlight: state.inFlight}
	quota := acc.quotaSnapshot()
	if quota.CheckedAt != nil {
		status.QuotaCheckedAtMs = quota.CheckedAt.UnixMilli()
	}
	for _, id := range config.DisabledAccountIDs {
		if id == acc.AccountID {
			status.Enabled = false
			status.Status = "paused"
			status.Reason = "manually_paused"
			return status
		}
	}
	if p.HasNoWorkspace(acc) {
		status.Status = "no_workspace"
		status.Reason = "no_accessible_workspace"
		return status
	}
	if state.reason == "invalid_credentials" {
		status.Status = "invalid"
		status.Reason = state.reason
		return status
	}
	if blocked, reset := routingAllowanceBlocked(quota.Info); blocked {
		status.Status = "exhausted"
		status.Reason = "allowance_exhausted"
		if !reset.IsZero() {
			status.CooldownUntilMs = reset.UnixMilli()
		}
		if reset.Before(now) && state.recheckAt.After(now) {
			status.CooldownUntilMs = state.recheckAt.UnixMilli()
		}
		return status
	}
	if p.isQuotaExhausted(acc) {
		status.Status = "exhausted"
		status.Reason = "quota_exhausted"
		return status
	}
	if state.reason != "" || state.rechecking {
		status.Status = "cooldown"
		status.Reason = state.reason
		if !state.recheckAt.IsZero() {
			status.CooldownUntilMs = state.recheckAt.UnixMilli()
		}
		return status
	}
	if config.MaxConcurrentPerAccount > 0 && state.inFlight >= config.MaxConcurrentPerAccount {
		status.Status = "busy"
		status.Reason = "concurrency_limit"
	}
	return status
}

func (p *AccountPool) RoutingSnapshot() RoutingStatus {
	accounts := p.AccountsSnapshot()
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	status := RoutingStatus{RoutingConfig: p.routingConfigLocked(), Total: len(accounts), Accounts: []RoutingAccountStatus{}}
	for _, acc := range accounts {
		acc.EnsureAccountID()
		entry := p.routingAccountStatusLocked(acc, status.RoutingConfig, time.Now())
		status.Accounts = append(status.Accounts, entry)
		switch entry.Status {
		case "available":
			status.Available++
		case "paused":
			status.Paused++
		case "exhausted":
			status.Exhausted++
		case "cooldown":
			status.Cooldown++
		case "invalid", "no_workspace":
			status.Invalid++
		case "busy":
			status.Busy++
		}
	}
	return status
}

func (p *AccountPool) recheckRoutingAccounts(accounts []*Account) {
	for _, acc := range accounts {
		acc.EnsureAccountID()
		p.routingMu.Lock()
		state := p.routingStateLocked(acc)
		paused := false
		for _, id := range p.routingConfig.DisabledAccountIDs {
			if id == acc.AccountID {
				paused = true
				break
			}
		}
		blocked, reset := routingAllowanceBlocked(acc.quotaInfoSnapshot())
		due := (state.reason == "rate_limited" || state.reason == "transient") && !state.recheckAt.After(time.Now())
		if blocked && !reset.IsZero() && !reset.After(time.Now()) && !state.recheckAt.After(time.Now()) {
			due = true
		}
		if paused || state.reason == "invalid_credentials" || state.rechecking || !due {
			p.routingMu.Unlock()
			continue
		}
		state.rechecking = true
		p.routingMu.Unlock()
		info, err := quotaFetcher(acc)
		if err == nil && info != nil {
			p.applyQuotaInfo(acc, info)
		}
		p.routingMu.Lock()
		state.rechecking = false
		if state.account == acc {
			stillBlocked, _ := routingAllowanceBlocked(info)
			if err == nil && info != nil && info.IsEligible && !stillBlocked {
				state.reason = ""
				state.until = time.Time{}
				state.recheckAt = time.Time{}
			} else {
				state.recheckAt = time.Now().Add(5 * time.Second)
			}
		}
		p.routingMu.Unlock()
	}
}
