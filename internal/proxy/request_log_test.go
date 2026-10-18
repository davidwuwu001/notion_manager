package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func requestLogSession() (*DashboardAuth, *http.Cookie) {
	auth := NewDashboardAuth("", "")
	w := httptest.NewRecorder()
	auth.CreateSession(w)
	return auth, w.Result().Cookies()[0]
}

func TestRequestLogOneRequestKeepsBothAccountAttempts(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), ".request_logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	zero := int64(0)
	handler := RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := TraceRequest(r.Context())
		trace.SetRequest("sonnet", "anthropic-sonnet", false)
		a := trace.BeginAttempt(ComputeAccountID("user", "space-a"))
		a.RecordUsage(RequestUsage{Source: "unknown"})
		a.Finish(RequestAttemptResult{Outcome: "failed", ErrorCode: "quota_exhausted"})
		b := trace.BeginAttempt(ComputeAccountID("user", "space-b"))
		b.RecordUsage(RequestUsage{Source: "actual", InputTokens: &zero, OutputTokens: &zero, Completeness: "complete"})
		b.Finish(RequestAttemptResult{Outcome: "success"})
		_, _ = w.Write([]byte("{}"))
		trace.FinishSuccess()
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	w := httptest.NewRecorder()
	auth, cookie := requestLogSession()
	request := httptest.NewRequest(http.MethodGet, "/admin/request-logs", nil)
	request.AddCookie(cookie)
	HandleRequestLogs(store, auth)(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("logs: %d %s", w.Code, w.Body)
	}
	var response RequestLogsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Items) != 1 {
		t.Fatalf("one external request expected: %+v", response)
	}
	item := response.Items[0]
	if item.Status != "success" || item.AttemptCount != 2 || item.UnknownCalls != 1 || item.ReportedCalls != 1 {
		t.Fatalf("attempt accounting: %+v", item)
	}
	if item.ReportedInputTokens == nil || *item.ReportedInputTokens != 0 {
		t.Fatalf("reported zero must remain known: %+v", item)
	}
	detail, err := store.Detail(item.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Attempts) != 2 || detail.Attempts[0].AccountID == detail.Attempts[1].AccountID {
		t.Fatalf("workspace attempts: %+v", detail.Attempts)
	}
}

func storeRequestFixture(t *testing.T, store *RequestLogStore, id string, started, duration int64, status, model string, account string) {
	t.Helper()
	finished := started + duration
	record := RequestLog{RequestID: id, StartedAtMs: started, FinishedAtMs: &finished, DurationMs: &duration, Protocol: "anthropic_messages", RequestedModel: model, ResolvedModel: model, Status: status}
	if err := store.BeginRequest(record); err != nil {
		t.Fatal(err)
	}
	attempts := []RequestAttempt{}
	if account != "" {
		attempts = append(attempts, RequestAttempt{AccountID: account, StartedAtMs: started, Outcome: status, FinishedAtMs: &finished, DurationMs: &duration})
	}
	if err := store.FinalizeRequest(record, attempts); err != nil {
		t.Fatal(err)
	}
}

func TestRequestLogCancelWriteFailureAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".request_logs.sqlite")
	store, err := NewRequestLogStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"cancel", "write", "unfinished"} {
		ctx, cancel := context.WithCancel(context.Background())
		h := RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace := TraceRequest(r.Context())
			trace.SetRequest(mode, mode, true)
			if mode == "cancel" {
				cancel()
			}
			if mode == "write" {
				_, _ = w.Write([]byte("sensitive output"))
				trace.FinishSuccess()
			}
		}))
		var w http.ResponseWriter = httptest.NewRecorder()
		if mode == "write" {
			w = failingRequestLogWriter{HeaderMap: http.Header{}}
		}
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx))
		cancel()
	}
	if err = store.BeginRequest(RequestLog{RequestID: "pending", StartedAtMs: time.Now().UnixMilli(), Protocol: "openai_responses"}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewRequestLogStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	list, err := store.List(RequestLogFilter{From: 0, To: time.Now().UnixMilli() + 1000, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range list.Items {
		statuses[item.ResolvedModel] = item.Status
	}
	if statuses["cancel"] != "cancelled" || statuses["write"] != "cancelled" || statuses["unfinished"] != "failed" {
		t.Fatalf("terminal states: %+v", statuses)
	}
	pending, err := store.Detail("pending")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Request.Status != "interrupted" || pending.Request.DurationMs != nil || pending.Request.FinishedAtMs != nil {
		t.Fatalf("recovered pending: %+v", pending.Request)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private database: %v %v", info, err)
	}
}

type failingRequestLogWriter struct{ HeaderMap http.Header }

func (w failingRequestLogWriter) Header() http.Header       { return w.HeaderMap }
func (w failingRequestLogWriter) WriteHeader(int)           {}
func (w failingRequestLogWriter) Write([]byte) (int, error) { return 0, errors.New("token_v2=private") }

func TestRequestLogFiltersCursorDetailAndSafeErrors(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UnixMilli()
	account := ComputeAccountID("same-user", "space-a")
	for i := 0; i < 6; i++ {
		storeRequestFixture(t, store, fmt.Sprintf("row-%d", i), now, 10, "success", "model-a", account)
	}
	filter := RequestLogFilter{From: now, To: now + 1, Model: "model-a", Protocol: "anthropic_messages", AccountID: account, Query: "row-", Limit: 2}
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		list, err := store.List(filter)
		if err != nil {
			t.Fatal(err)
		}
		if list.Total != 6 || len(list.Items) != 2 {
			t.Fatalf("page %d: %+v", page, list)
		}
		for _, item := range list.Items {
			if seen[item.RequestID] {
				t.Fatal("duplicate cursor row")
			}
			seen[item.RequestID] = true
		}
		if page < 2 {
			if list.NextCursor == nil {
				t.Fatal("missing cursor")
			}
			filter.Cursor = *list.NextCursor
		}
	}
	if len(seen) != 6 {
		t.Fatalf("missing rows: %+v", seen)
	}
	detail, err := store.Detail("row-0")
	if err != nil || detail.Request.RequestID != "row-0" {
		t.Fatalf("older detail: %+v %v", detail, err)
	}
	filter.Query = "' OR 1=1 --"
	list, err := store.List(filter)
	if err != nil || list.Total != 0 {
		t.Fatalf("bound filter: %+v %v", list, err)
	}
	record := RequestLog{RequestID: "unsafe", StartedAtMs: now, Protocol: "openai_chat", ErrorCode: "upstream token_v2=private cookie=private", Status: "failed", RequestedModel: "prompt contains spaces"}
	if err = store.BeginRequest(record); err != nil {
		t.Fatal(err)
	}
	if err = store.FinalizeRequest(record, []RequestAttempt{{AccountID: account, ErrorCode: "secret upstream response", RetryReason: "Cookie: token_v2=private", Outcome: "failed"}}); err != nil {
		t.Fatal(err)
	}
	detail, err = store.Detail("unsafe")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(detail)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "prompt contains") || strings.Contains(string(raw), "secret upstream") {
		t.Fatalf("sensitive error persisted: %s", raw)
	}
	auth, cookie := requestLogSession()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/request-logs?status=bad", nil)
	r.AddCookie(cookie)
	HandleRequestLogs(store, auth)(w, r)
	if w.Code != 400 {
		t.Fatalf("bad filter status %d", w.Code)
	}
	w = httptest.NewRecorder()
	HandleRequestLogs(store, auth)(w, httptest.NewRequest("GET", "/admin/request-logs", nil))
	if w.Code != 401 {
		t.Fatalf("unauthorized status %d", w.Code)
	}
}

func TestRequestAnalyticsFullWindowPercentilesAndBuckets(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().Truncate(time.Hour).UnixMilli()
	account := ComputeAccountID("user", "space")
	for i := 1; i <= 100; i++ {
		storeRequestFixture(t, store, fmt.Sprintf("latency-%03d", i), now, int64(i), "success", "model-a", account)
	}
	storeRequestFixture(t, store, "failed", now, 10000, "failed", "model-b", account)
	result, err := store.Analytics(RequestLogFilter{From: now, To: now + 2*int64(time.Hour/time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Overview.TotalRequests != 101 || result.Overview.P50DurationMs == nil || *result.Overview.P50DurationMs != 50 || result.Overview.P95DurationMs == nil || *result.Overview.P95DurationMs != 95 {
		t.Fatalf("global percentile: %+v", result.Overview)
	}
	if result.Overview.AvgDurationMs == nil || *result.Overview.AvgDurationMs != 50.5 {
		t.Fatalf("success-only average: %+v", result.Overview)
	}
	if len(result.Timeline) != 2 || result.Timeline[0].TotalRequests != 101 || result.Timeline[1].TotalRequests != 0 {
		t.Fatalf("zero-filled hour bins: %+v", result.Timeline)
	}
	if len(result.Accounts) != 1 || result.Accounts[0].Requests != 101 || result.Accounts[0].Attempts != 101 {
		t.Fatalf("account distribution: %+v", result.Accounts)
	}
}

func TestRequestLogIdempotencyMixedUsageAndAccountRequestDedup(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UnixMilli()
	account := ComputeAccountID("u", "s")
	reported, estimated := int64(10), int64(50)
	record := RequestLog{RequestID: "idempotent", StartedAtMs: now, Status: "success", Protocol: "openai_responses"}
	if err = store.BeginRequest(record); err != nil {
		t.Fatal(err)
	}
	var actual, estimate RequestUsageTotals
	actual.add(RequestUsage{Source: "actual", InputTokens: &reported, OutputTokens: &reported, Completeness: "complete"})
	estimate.add(RequestUsage{Source: "estimated", InputTokens: &estimated, OutputTokens: &estimated, Completeness: "complete"})
	attempts := []RequestAttempt{{AccountID: account, Outcome: "failed", RequestUsageTotals: actual}, {AccountID: account, Outcome: "success", RequestUsageTotals: estimate}}
	if err = store.FinalizeRequest(record, attempts); err != nil {
		t.Fatal(err)
	}
	record.Status = "failed"
	if err = store.FinalizeRequest(record, nil); err != nil {
		t.Fatal(err)
	}
	detail, err := store.Detail(record.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Request.Status != "success" || detail.Request.UsageSource != "mixed" || detail.Request.InputTokens != nil || *detail.Request.ReportedInputTokens != 10 || *detail.Request.EstimatedInputTokens != 50 || len(detail.Attempts) != 2 {
		t.Fatalf("first terminal transaction must win: %+v", detail)
	}
	analytics, err := store.Analytics(RequestLogFilter{From: now, To: now + 1, AccountID: account})
	if err != nil {
		t.Fatal(err)
	}
	if analytics.Overview.TotalRequests != 1 || analytics.Accounts[0].Requests != 1 || analytics.Accounts[0].Attempts != 2 {
		t.Fatalf("request/attempt dedup: %+v", analytics)
	}
}

func TestRequestLogRetentionSchemaProtectionAndUnavailableStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.sqlite")
	store, err := NewRequestLogStore(path, 90)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.AddDate(0, 0, -60).UnixMilli()
	storeRequestFixture(t, store, "old", old, 1, "success", "model", "")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewRequestLogStore(path, 90)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Detail("old"); err != nil {
		t.Fatalf("configured startup retention: %v", err)
	}
	if err = store.SetRetentionDays(30); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Detail("old"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired request retained: %v", err)
	}
	if err = store.SetRetentionDays(0); err == nil {
		t.Fatal("invalid retention accepted")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("still works"))
		TraceRequest(r.Context()).FinishSuccess()
	})).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", nil))
	if w.Code != 200 || w.Body.String() != "still works" || store.Coverage().DataComplete {
		t.Fatalf("storage must not break inference: %d %+v", w.Code, store.Coverage())
	}
	auth, cookie := requestLogSession()
	w = httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/admin/request-logs", nil)
	request.AddCookie(cookie)
	HandleRequestLogs(store, auth)(w, request)
	if w.Code != 503 || strings.Contains(w.Body.String(), path) || !strings.Contains(w.Body.String(), "storage_unavailable") {
		t.Fatalf("safe visible error: %d %s", w.Code, w.Body)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if unexpected, err := NewRequestLogStore(path); err == nil {
		_ = unexpected.Close()
		t.Fatal("future schema silently opened")
	}
}

func TestRequestLogBridgeOnlyCountsExternalRequestAndFirstFailureWins(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inner := RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := TraceRequest(r.Context())
		trace.SetRequest("model", "model", false)
		trace.FinishFailure("failed", "upstream_timeout")
		trace.FinishFailure("failed", "api_error")
		trace.FinishSuccess()
		_, _ = w.Write([]byte("{}"))
	}))
	outer := RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := httptest.NewRequest("POST", "/v1/messages", nil).WithContext(r.Context())
		inner.ServeHTTP(w, request)
	}))
	outer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", nil))
	RequestLogMiddleware(store, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/models", nil))
	list, err := store.List(RequestLogFilter{From: 0, To: time.Now().UnixMilli() + 1})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || list.Items[0].Protocol != "openai_responses" || list.Items[0].ErrorCode != "upstream_timeout" || list.Items[0].Status != "failed" {
		t.Fatalf("shared trace: %+v", list)
	}
}

func TestRequestAnalyticsDoesNotTruncateAfterFiveThousandRows(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UnixMilli()
	for i := 0; i < 5001; i++ {
		storeRequestFixture(t, store, fmt.Sprintf("all-%05d", i), now, 1, "success", "model", "")
	}
	result, err := store.Analytics(RequestLogFilter{From: now, To: now + 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Overview.TotalRequests != 5001 || result.Models[0].TotalRequests != 5001 || result.Timeline[0].TotalRequests != 5001 {
		t.Fatalf("window truncated: %+v", result.Overview)
	}
	if _, err = store.Detail("all-00000"); err != nil {
		t.Fatalf("old ID inaccessible: %v", err)
	}
}

func TestRequestLogConcurrentRecording(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := TraceRequest(r.Context())
		attempt := trace.BeginAttempt(ComputeAccountID("u", "s"))
		tokens := int64(1)
		attempt.RecordUsage(RequestUsage{Source: "actual", InputTokens: &tokens, OutputTokens: &tokens, Completeness: "complete"})
		attempt.Finish(RequestAttemptResult{Outcome: "success"})
		trace.FinishSuccess()
		_, _ = w.Write([]byte("{}"))
	}))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/messages", nil))
		}()
	}
	wg.Wait()
	result, err := store.Analytics(RequestLogFilter{From: time.Now().Add(-time.Minute).UnixMilli(), To: time.Now().UnixMilli() + 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Overview.TotalRequests != 20 || *result.Overview.ReportedInputTokens != 20 {
		t.Fatalf("concurrent accounting: %+v", result.Overview)
	}
}

func TestRequestLogPanicCannotKeepSuccess(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		TraceRequest(r.Context()).FinishSuccess()
		panic("private panic detail")
	}))
	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/messages", nil))
	}()
	result, err := store.List(RequestLogFilter{From: time.Now().Add(-time.Minute).UnixMilli(), To: time.Now().UnixMilli() + 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Items[0].Status != "failed" || result.Items[0].ErrorCode != "internal_error" || result.Items[0].CompletionEmitted {
		t.Fatalf("panic state: %+v", result)
	}
}

func TestRequestLogRetentionWatermarkNeverMovesBackAfterDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.sqlite")
	store, err := NewRequestLogStore(path, 30)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	storeRequestFixture(t, store, "removed", now.AddDate(0, 0, -2).UnixMilli(), 1, "success", "model", "")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	// A persisted fixture represents a service that started thirty days ago.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE request_log_meta SET value=? WHERE key='captured_since'", now.AddDate(0, 0, -30).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewRequestLogStore(path, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetRetentionDays(1); err != nil {
		t.Fatal(err)
	}
	watermark := store.Coverage().RetainedFrom
	if _, err = store.Detail("removed"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("fixture was not deleted: %v", err)
	}
	if err = store.SetRetentionDays(30); err != nil {
		t.Fatal(err)
	}
	if got := store.Coverage().RetainedFrom; got < watermark {
		t.Fatalf("retention increased but deleted history was claimed again: %d < %d", got, watermark)
	}
	window := RequestLogFilter{From: now.AddDate(0, 0, -2).UnixMilli(), To: now.UnixMilli() + 1}
	result, err := store.Analytics(window)
	if err != nil {
		t.Fatal(err)
	}
	if result.DataComplete {
		t.Fatal("deleted interval was advertised as complete")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewRequestLogStore(path, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.Coverage().RetainedFrom; got < watermark {
		t.Fatalf("restart lost retained watermark: %d < %d", got, watermark)
	}
	result, err = store.Analytics(window)
	if err != nil {
		t.Fatal(err)
	}
	if result.DataComplete {
		t.Fatal("restart advertised deleted interval as complete")
	}
}

func TestRequestLogCaptureGapSurvivesRestartWithoutAnotherRequest(t *testing.T) {
	for _, mode := range []string{"lock-recovered", "database-still-readonly"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "logs.sqlite")
			store, err := NewRequestLogStore(path)
			if err != nil {
				t.Fatal(err)
			}
			var lock *sql.DB
			if mode == "lock-recovered" {
				lock, err = sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = lock.Exec("BEGIN IMMEDIATE"); err != nil {
					t.Fatal(err)
				}
			} else if _, err = store.db.Exec("PRAGMA query_only=ON"); err != nil {
				t.Fatal(err)
			}
			if err = store.BeginRequest(RequestLog{RequestID: "lost-request", StartedAtMs: time.Now().UnixMilli(), Protocol: "openai_chat"}); err == nil {
				t.Fatal("write failure fixture did not fail")
			}
			if store.Coverage().DataComplete {
				t.Fatal("live storage hides capture gap")
			}
			marker, err := os.ReadFile(path + ".gap")
			if err != nil || string(marker) != "incomplete\n" {
				t.Fatalf("private gap marker missing: %q %v", marker, err)
			}
			if info, err := os.Stat(path + ".gap"); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("gap marker permissions: %v %v", info, err)
			}
			if lock != nil {
				if _, err = lock.Exec("ROLLBACK"); err != nil {
					t.Fatal(err)
				}
				_ = lock.Close()
			}
			closeErr := store.Close()
			if mode == "lock-recovered" && closeErr != nil {
				t.Fatal(closeErr)
			}
			if mode == "database-still-readonly" {
				if _, err = os.Stat(path + ".gap"); err != nil {
					t.Fatalf("failed close lost marker: %v", err)
				}
			}
			store, err = NewRequestLogStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if store.Coverage().DataComplete {
				t.Fatal("restart advertised missing requests as complete")
			}
			if _, err = os.Stat(path + ".gap"); !os.IsNotExist(err) {
				t.Fatalf("persisted gap marker not retired: %v", err)
			}
		})
	}
}

func TestRequestLogErrorJSONIsNotPartialModelOutput(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, content := range []bool{false, true} {
		RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace := TraceRequest(r.Context())
			model := "error-only"
			if content {
				model = "model-output"
			}
			trace.SetRequest(model, model, true)
			if content {
				attempt := trace.BeginAttempt(ComputeAccountID("u", "s"))
				attempt.Finish(RequestAttemptResult{Outcome: "failed", ContentEmitted: true})
			}
			trace.FinishFailure("failed", "upstream_error")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/messages", nil))
	}
	result, err := store.List(RequestLogFilter{From: time.Now().Add(-time.Minute).UnixMilli(), To: time.Now().UnixMilli() + 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.PartialResponse != (item.RequestedModel == "model-output") {
			t.Fatalf("partial model output: %+v", item)
		}
	}
}
