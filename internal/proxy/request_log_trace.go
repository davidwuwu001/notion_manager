package proxy

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

type requestTraceContextKey struct{}
type requestAttemptContextKey struct{}

type RequestTrace struct {
	mu        sync.Mutex
	start     time.Time
	record    RequestLog
	attempts  []*RequestAttemptTrace
	finalized bool
}

type RequestAttemptTrace struct {
	mu       sync.Mutex
	start    time.Time
	record   RequestAttempt
	finished bool
}

type RequestAttemptResult struct {
	Outcome           string
	ErrorCode         string
	RetryReason       string
	CooldownUntilMs   *int64
	ResponseCommitted bool
	ContentEmitted    bool
	ToolCallEmitted   bool
}

func RequestIDFromContext(ctx context.Context) string {
	trace := TraceRequest(ctx)
	if trace == nil {
		return ""
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.record.RequestID
}

// Stage times accumulate across retries and may overlap; they are not a breakdown of total time.
func (t *RequestTrace) RecordStage(stage string, duration time.Duration) {
	if t == nil || !validRequestStage(stage) || duration < 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finalized {
		return
	}
	if t.record.StageDurationsMs == nil {
		t.record.StageDurationsMs = make(map[string]int64)
	}
	t.record.StageDurationsMs[stage] += duration.Milliseconds()
}

func TraceRequest(ctx context.Context) *RequestTrace {
	if ctx == nil {
		return nil
	}
	trace, _ := ctx.Value(requestTraceContextKey{}).(*RequestTrace)
	return trace
}

func WithRequestAttempt(ctx context.Context, attempt *RequestAttemptTrace) context.Context {
	return context.WithValue(ctx, requestAttemptContextKey{}, attempt)
}

func RequestAttemptFromContext(ctx context.Context) *RequestAttemptTrace {
	if ctx == nil {
		return nil
	}
	attempt, _ := ctx.Value(requestAttemptContextKey{}).(*RequestAttemptTrace)
	return attempt
}

func (t *RequestTrace) SetRequest(requested, resolved string, stream bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finalized {
		return
	}
	t.record.RequestedModel = safeRequestMetadata(requested)
	t.record.ResolvedModel = safeRequestMetadata(resolved)
	t.record.Stream = stream
}

func (t *RequestTrace) BeginAttempt(accountID string) *RequestAttemptTrace {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finalized {
		return nil
	}
	now := time.Now()
	attempt := &RequestAttemptTrace{start: now, record: RequestAttempt{RequestID: t.record.RequestID, AttemptNo: len(t.attempts) + 1, AccountID: safeAccountID(accountID), StartedAtMs: now.UnixMilli(), Outcome: "in_progress"}}
	t.attempts = append(t.attempts, attempt)
	return attempt
}

func (t *RequestTrace) MarkFirstToken() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finalized || t.record.FirstTokenMs != nil {
		return
	}
	value := time.Since(t.start).Milliseconds()
	t.record.FirstTokenMs = &value
}

func (t *RequestTrace) FinishSuccess() { t.finish("success", "") }
func (t *RequestTrace) FinishFailure(status, errorCode string) {
	if status != "rejected" && status != "failed" {
		status = "failed"
	}
	t.finish(status, safeRequestCode(errorCode))
}
func (t *RequestTrace) FinishCancelled() { t.finish("cancelled", "client_cancelled") }

func (t *RequestTrace) finish(status, code string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finalized || t.record.Status != "in_progress" {
		return
	}
	t.record.Status = status
	t.record.ErrorCode = code
	t.record.CompletionEmitted = status == "success"
}

func (t *RequestAttemptTrace) RecordUsage(usage RequestUsage) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	t.record.RequestUsageTotals.add(usage)
}

func (t *RequestAttemptTrace) Finish(result RequestAttemptResult) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	t.finished = true
	now := time.Now().UnixMilli()
	duration := time.Since(t.start).Milliseconds()
	t.record.FinishedAtMs = &now
	t.record.DurationMs = &duration
	t.record.Outcome = result.Outcome
	t.record.ErrorCode = safeRequestCode(result.ErrorCode)
	t.record.RetryReason = safeRequestCode(result.RetryReason)
	t.record.CooldownUntilMs = result.CooldownUntilMs
	t.record.ResponseCommitted = result.ResponseCommitted
	t.record.ContentEmitted = result.ContentEmitted
	t.record.ToolCallEmitted = result.ToolCallEmitted
}

func (t *RequestTrace) snapshot(status int, writeFailed, partialBytes, cancelled bool) (RequestLog, []RequestAttempt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finalized = true
	now := time.Now().UnixMilli()
	duration := time.Since(t.start).Milliseconds()
	t.record.FinishedAtMs = &now
	t.record.DurationMs = &duration
	t.record.HTTPStatus = status
	switch {
	case writeFailed:
		t.record.Status = "cancelled"
		t.record.ErrorCode = "write_failed"
		t.record.CompletionEmitted = false
	case cancelled:
		t.record.Status = "cancelled"
		t.record.ErrorCode = "client_cancelled"
		t.record.CompletionEmitted = false
	case t.record.Status == "in_progress" && status >= 400 && status < 500:
		t.record.Status = "rejected"
		t.record.ErrorCode = "invalid_request"
		if status == http.StatusUnauthorized {
			t.record.ErrorCode = "unauthorized"
		}
		if status == http.StatusForbidden {
			t.record.ErrorCode = "forbidden"
		}
	case t.record.Status == "in_progress":
		t.record.Status = "failed"
		t.record.ErrorCode = "unfinished_request"
	}
	partial := t.record.FirstTokenMs != nil
	attempts := make([]RequestAttempt, 0, len(t.attempts))
	for _, attempt := range t.attempts {
		attempt.mu.Lock()
		if !attempt.finished {
			attempt.finished = true
			attempt.record.Outcome = "interrupted"
			if cancelled || writeFailed {
				attempt.record.Outcome = "cancelled"
			}
			attempt.record.FinishedAtMs = &now
			d := time.Since(attempt.start).Milliseconds()
			attempt.record.DurationMs = &d
		}
		attempt.record.RequestUsageTotals.normalize()
		if attempt.record.ContentEmitted || attempt.record.ToolCallEmitted || partialBytes && writeFailed && status == http.StatusOK && attempt.record.Outcome == "success" {
			partial = true
		}
		attempts = append(attempts, attempt.record)
		if t.record.Status == "success" && attempt.record.Outcome == "success" && attempt.record.AccountID != "" {
			id := attempt.record.AccountID
			t.record.FinalAccountID = &id
		}
		attempt.mu.Unlock()
	}
	t.record.PartialResponse = partial && t.record.Status != "success"
	return t.record, attempts
}

type requestLogResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	writeFailed bool
}

func (w *requestLogResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *requestLogResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *requestLogResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	if err != nil || n != len(p) {
		w.writeFailed = true
	}
	return n, err
}

type flushingRequestLogWriter struct{ *requestLogResponseWriter }

func (w *flushingRequestLogWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.writeFailed = true
	}
}

// RequestLogMiddleware observes only the external generation request. Internal
// protocol bridges inherit its context and never create another database row.
func RequestLogMiddleware(store *RequestLogStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol := ""
		if r.Method == http.MethodPost {
			switch r.URL.Path {
			case "/v1/messages":
				protocol = "anthropic_messages"
			case "/v1/chat/completions":
				protocol = "openai_chat"
			case "/v1/responses":
				protocol = "openai_responses"
			}
		}
		if protocol == "" || TraceRequest(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}
		now := time.Now()
		trace := &RequestTrace{start: now, record: RequestLog{SchemaVersion: 1, RequestID: "req_" + generateUUIDv4(), StartedAtMs: now.UnixMilli(), Protocol: protocol, Status: "in_progress"}}
		trace.record.RequestUsageTotals.normalize()
		w.Header().Set("X-Request-ID", trace.record.RequestID)
		log.Printf("[request] %s protocol=%s status=in_progress", trace.record.RequestID, protocol)
		_ = store.BeginRequest(trace.record)
		writer := &requestLogResponseWriter{ResponseWriter: w}
		var responseWriter http.ResponseWriter = writer
		if _, ok := w.(http.Flusher); ok {
			responseWriter = &flushingRequestLogWriter{writer}
		}
		r = r.WithContext(context.WithValue(r.Context(), requestTraceContextKey{}, trace))
		defer func() {
			if recovered := recover(); recovered != nil {
				trace.mu.Lock()
				trace.record.Status = "failed"
				trace.record.ErrorCode = "internal_error"
				trace.record.CompletionEmitted = false
				trace.mu.Unlock()
				record, attempts := trace.snapshot(writer.status, writer.writeFailed, writer.bytes > 0, false)
				_ = store.FinalizeRequest(record, attempts)
				panic(recovered)
			}
			record, attempts := trace.snapshot(writer.status, writer.writeFailed, writer.bytes > 0, r.Context().Err() != nil)
			_ = store.FinalizeRequest(record, attempts)
			log.Printf("[request] %s protocol=%s status=%s http_status=%d attempts=%d error_code=%s", record.RequestID, record.Protocol, record.Status, record.HTTPStatus, record.AttemptCount, record.ErrorCode)
		}()
		next.ServeHTTP(responseWriter, r)
	})
}
