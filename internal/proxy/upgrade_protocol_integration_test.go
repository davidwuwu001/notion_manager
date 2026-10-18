package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type upgradeProtocolFixture struct {
	pool  *AccountPool
	store *RequestLogStore
}

func newUpgradeProtocolFixture(t *testing.T, accounts int) upgradeProtocolFixture {
	t.Helper()
	originalConfig, originalCaller, originalQuota, originalSessions := AppConfig, inferenceCaller, quotaFetcher, globalSessionManager
	AppConfig = DefaultConfig()
	no, yes := false, true
	AppConfig.Proxy.EnableWebSearch = &no
	AppConfig.Proxy.EnableWorkspaceSearch = &no
	AppConfig.Proxy.AskModeDefault = &yes
	globalSessionManager = &SessionManager{sessions: make(map[string]*Session), ttl: time.Hour}
	inferenceCaller = func(*Account, []ChatMessage, string, bool, StreamCallback, ...CallOptions) error {
		return errors.New("unexpected inference in offline integration fixture")
	}
	quotaFetcher = func(*Account) (*QuotaInfo, error) { return nil, errors.New("offline quota check") }
	pool := routingPool(accounts)
	for _, acc := range pool.AccountsSnapshot() {
		now := time.Now()
		acc.setQuotaInfo(&QuotaInfo{IsEligible: true, SpaceLimit: 100, UserLimit: 100}, &now)
	}
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "requests.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := upgradeProtocolFixture{pool: pool, store: store}
	t.Cleanup(func() {
		defer func() {
			_ = store.Close()
			AppConfig, inferenceCaller, quotaFetcher, globalSessionManager = originalConfig, originalCaller, originalQuota, originalSessions
		}()
		fixture.waitForIdle(t)
	})
	return fixture
}

func (f upgradeProtocolFixture) waitForIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.pool.liveQuotaMu.Lock()
		checks := len(f.pool.liveQuotaInflight)
		f.pool.liveQuotaMu.Unlock()
		inFlight := 0
		for _, account := range f.pool.RoutingSnapshot().Accounts {
			inFlight += account.InFlight
		}
		globalSessionManager.mu.RLock()
		turns := len(globalSessionManager.active)
		globalSessionManager.mu.RUnlock()
		if checks == 0 && inFlight == 0 && turns == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("integration request left an active quota check, turn, or account slot")
}

var upgradeProtocolCases = []struct{ path, protocol string }{
	{"/v1/messages", "anthropic_messages"},
	{"/v1/chat/completions", "openai_chat"},
	{"/v1/responses", "openai_responses"},
}

func (f upgradeProtocolFixture) serve(t *testing.T, path string, stream bool, messages []map[string]string, ctx context.Context, headers ...http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := map[string]any{"model": "integration-model", "stream": stream, "max_tokens": 32}
	request["metadata"] = map[string]string{"session_id": t.Name()}
	if strings.HasSuffix(path, "/responses") {
		request["input"] = messages
	} else {
		request["messages"] = messages
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var handler http.HandlerFunc
	switch path {
	case "/v1/messages":
		handler = HandleAnthropicMessages(f.pool)
	case "/v1/chat/completions":
		handler = HandleOpenAIChatCompletions(f.pool)
	case "/v1/responses":
		handler = HandleOpenAIResponses(f.pool)
	default:
		t.Fatalf("unknown test protocol path %s", path)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body))).WithContext(ctx)
	if len(headers) > 0 {
		r.Header = headers[0].Clone()
	}
	RequestLogMiddleware(f.store, handler).ServeHTTP(w, r)
	f.waitForIdle(t)
	return w
}

func (f upgradeProtocolFixture) oneLog(t *testing.T) RequestLogDetail {
	t.Helper()
	list, err := f.store.List(RequestLogFilter{From: 0, To: time.Now().Add(time.Second).UnixMilli(), Limit: 50})
	if err != nil || list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("external request not logged exactly once: total=%d items=%d err=%v", list.Total, len(list.Items), err)
	}
	detail, err := f.store.Detail(list.Items[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func upgradeProtocolSuccess(cb StreamCallback, opts []CallOptions) error {
	if len(opts) != 1 || RequestAttemptFromContext(opts[0].Context) == nil {
		return errors.New("protocol bridge dropped the external request attempt context")
	}
	zero := int64(0)
	RequestAttemptFromContext(opts[0].Context).RecordUsage(RequestUsage{Source: "actual", InputTokens: &zero, OutputTokens: &zero, Completeness: "complete"})
	cb("offline answer", true, &UsageInfo{InputKnown: true, OutputKnown: true})
	return nil
}

func TestUpgradeProtocolSingleExternalRecord(t *testing.T) {
	for _, protocol := range upgradeProtocolCases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol.protocol, stream), func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 1)
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					return upgradeProtocolSuccess(cb, opts)
				}
				w := fixture.serve(t, protocol.path, stream, []map[string]string{{"role": "user", "content": t.Name()}}, context.Background())
				log := fixture.oneLog(t)
				if w.Code != http.StatusOK || log.Request.Status != "success" || log.Request.Protocol != protocol.protocol || log.Request.AttemptCount != 1 || len(log.Attempts) != 1 {
					t.Fatalf("protocol completion mismatch: http=%d log=%+v body=%s", w.Code, log, w.Body)
				}
				if log.Request.ReportedInputTokens == nil || *log.Request.ReportedInputTokens != 0 || log.Request.ReportedOutputTokens == nil || *log.Request.ReportedOutputTokens != 0 || log.Request.UsageSource != "actual" {
					t.Fatalf("reported zero tokens became missing: %+v", log.Request.RequestUsageTotals)
				}
				if globalSessionManager.Count() != 1 || log.Request.FinalAccountID == nil {
					t.Fatal("successful request did not persist its workspace-bound session")
				}
			})
		}
	}
}

func TestUpgradeProtocolErrorsAndCancellationNeverSaveSuccessfulSession(t *testing.T) {
	for _, protocol := range upgradeProtocolCases {
		for _, stream := range []bool{false, true} {
			for _, cancelled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/cancel=%t", protocol.protocol, stream, cancelled), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, _ StreamCallback, _ ...CallOptions) error {
						if cancelled {
							cancel()
							return context.Canceled
						}
						return &notionHTTPError{Status: http.StatusBadRequest}
					}
					fixture.serve(t, protocol.path, stream, []map[string]string{{"role": "user", "content": t.Name()}}, ctx)
					log := fixture.oneLog(t)
					want := "failed"
					if cancelled {
						want = "cancelled"
					}
					if log.Request.Status != want || log.Request.CompletionEmitted || globalSessionManager.Count() != 0 || log.Request.AttemptCount != 1 {
						t.Fatalf("failed request produced success state: %+v sessions=%d", log, globalSessionManager.Count())
					}
				})
			}
		}
	}
}

func TestUpgradeProtocolReadOnlyTransientFailsOverWithoutDoubleCounting(t *testing.T) {
	for _, protocol := range upgradeProtocolCases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol.protocol, stream), func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 2)
				var selected []string
				inferenceCaller = func(acc *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					selected = append(selected, acc.AccountID)
					if len(selected) == 1 {
						return &notionHTTPError{Status: http.StatusInternalServerError}
					}
					return upgradeProtocolSuccess(cb, opts)
				}
				w := fixture.serve(t, protocol.path, stream, []map[string]string{{"role": "user", "content": t.Name()}}, context.Background())
				log := fixture.oneLog(t)
				if w.Code != http.StatusOK || log.Request.Status != "success" || len(selected) != 2 || selected[0] == selected[1] || len(log.Attempts) != 2 {
					t.Fatalf("safe pool failover failed: status=%d selected=%v log=%+v", w.Code, selected, log)
				}
				if log.Attempts[0].Outcome != "failed" || log.Attempts[0].RetryReason == "" || log.Attempts[1].Outcome != "success" {
					t.Fatalf("retry trajectory was lost: %+v", log.Attempts)
				}
			})
		}
	}
}

func TestUpgradeProtocolWorkspaceHeaderControlsReplayBoundary(t *testing.T) {
	for _, protocol := range upgradeProtocolCases {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name          string
				global        bool
				header        string
				effective     bool
				allowFailover bool
			}{
				{"global_enabled_request_disabled", true, "false", false, true},
				{"global_enabled_no_override", true, "", true, false},
				{"global_disabled_request_enabled", false, "true", true, false},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocol.protocol, stream, tc.name), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 2)
					AppConfig.Proxy.EnableWorkspaceSearch = &tc.global
					var selected []string
					inferenceCaller = func(acc *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
						selected = append(selected, acc.AccountID)
						if len(opts) != 1 || opts[0].EnableWorkspaceSearch == nil || *opts[0].EnableWorkspaceSearch != tc.effective {
							return errors.New("effective workspace-search setting was not frozen into inference options")
						}
						if len(selected) == 1 {
							return &notionHTTPError{Status: http.StatusBadGateway}
						}
						return upgradeProtocolSuccess(cb, opts)
					}
					header := make(http.Header)
					if tc.header != "" {
						header.Set("X-Workspace-Search", tc.header)
					}
					fixture.serve(t, protocol.path, stream, []map[string]string{{"role": "user", "content": t.Name()}}, context.Background(), header)
					log := fixture.oneLog(t)
					if tc.allowFailover {
						if len(selected) != 2 || selected[0] == selected[1] || len(log.Attempts) != 2 || log.Request.Status != "success" {
							t.Fatalf("explicitly disabled workspace search still prevented safe failover: selected=%v log=%+v", selected, log)
						}
					} else if len(selected) != 1 || len(log.Attempts) != 1 || log.Request.Status != "failed" || log.Attempts[0].RetryReason != "" {
						t.Fatalf("workspace search crossed the replay boundary: selected=%v log=%+v", selected, log)
					}
					if AppConfig.WorkspaceSearchEnabled() != tc.global {
						t.Fatal("request override changed the global workspace-search setting")
					}
				})
			}
		}
	}
}

func TestUpgradeProtocolPartialStreamNeverReplaysAcrossWorkspaces(t *testing.T) {
	for _, protocol := range upgradeProtocolCases {
		t.Run(protocol.protocol, func(t *testing.T) {
			fixture := newUpgradeProtocolFixture(t, 2)
			calls := 0
			inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
				calls++
				cb("partial offline answer", false, nil)
				return ErrQuotaExhausted
			}
			w := fixture.serve(t, protocol.path, true, []map[string]string{{"role": "user", "content": t.Name()}}, context.Background())
			log := fixture.oneLog(t)
			if calls != 1 || log.Request.Status != "failed" || !log.Request.PartialResponse || log.Request.CompletionEmitted || globalSessionManager.Count() != 0 || len(log.Attempts) != 1 {
				t.Fatalf("partial stream replayed or became success: calls=%d log=%+v", calls, log)
			}
			if strings.Contains(w.Body.String(), "message_stop") || strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "response.completed") {
				t.Fatal("interrupted stream emitted normal completion")
			}
			if fixture.pool.RoutingSnapshot().Exhausted != 1 {
				t.Fatal("unsafe-to-replay quota failure did not exclude the exhausted member")
			}
		})
	}
}

func TestUpgradeProtocolMultiTurnKeepsAccountIDAndPersistsUpdates(t *testing.T) {
	fixture := newUpgradeProtocolFixture(t, 2)
	accounts := fixture.pool.AccountsSnapshot()
	var selected []string
	var threadIDs []string
	var priorTurns []int
	inferenceCaller = func(acc *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
		selected = append(selected, acc.AccountID)
		threadIDs = append(threadIDs, opts[0].Session.ThreadID)
		priorTurns = append(priorTurns, opts[0].Session.TurnCount)
		return upgradeProtocolSuccess(cb, opts)
	}
	messages := []map[string]string{{"role": "user", "content": t.Name()}}
	fixture.serve(t, "/v1/messages", false, messages, context.Background())
	setRoutingAllowance(accounts[0], 80, 80, time.Now().Add(time.Hour))
	setRoutingAllowance(accounts[1], 0, 0, time.Now().Add(time.Hour))
	messages = append(messages, map[string]string{"role": "assistant", "content": "offline answer"}, map[string]string{"role": "user", "content": "continue"})
	fixture.serve(t, "/v1/messages", false, messages, context.Background())
	fixture.serve(t, "/v1/messages", false, messages, context.Background())
	if len(selected) != 3 || selected[0] != accounts[0].AccountID || selected[1] != selected[0] || selected[2] != selected[0] || threadIDs[0] != threadIDs[1] || threadIDs[1] != threadIDs[2] {
		t.Fatalf("same-email workspace binding changed: accounts=%v threads=%v", selected, threadIDs)
	}
	if priorTurns[0] != 0 || priorTurns[1] != 1 || priorTurns[2] != 2 {
		t.Fatalf("multi-turn session updates were not persisted: %v", priorTurns)
	}
	globalSessionManager.mu.RLock()
	defer globalSessionManager.mu.RUnlock()
	for _, session := range globalSessionManager.sessions {
		if session.TurnCount != 2 || session.RawMessageCount != 3 || len(session.UpdatedConfigIDs) != 2 || session.AccountID != accounts[0].AccountID {
			t.Fatalf("stored repeated-turn session drifted: %+v", session)
		}
	}
}
