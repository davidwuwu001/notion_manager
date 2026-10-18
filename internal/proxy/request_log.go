package proxy

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const requestLogSchemaVersion = 1

// RequestUsage describes one upstream call's final cumulative observation.
// Actual means reported by the upstream; it is not a verified billing amount.
type RequestUsage struct {
	Source       string
	InputTokens  *int64
	OutputTokens *int64
	Completeness string
}

type RequestUsageTotals struct {
	InputTokens           *int64 `json:"input_tokens"`
	OutputTokens          *int64 `json:"output_tokens"`
	UsageSource           string `json:"usage_source"`
	ReportedInputTokens   *int64 `json:"reported_input_tokens"`
	ReportedOutputTokens  *int64 `json:"reported_output_tokens"`
	EstimatedInputTokens  *int64 `json:"estimated_input_tokens"`
	EstimatedOutputTokens *int64 `json:"estimated_output_tokens"`
	UpstreamCallCount     int64  `json:"upstream_call_count"`
	ReportedCalls         int64  `json:"reported_calls"`
	EstimatedCalls        int64  `json:"estimated_calls"`
	UnknownCalls          int64  `json:"unknown_calls"`
	UsageCompleteness     string `json:"usage_completeness"`
}

type RequestLog struct {
	SchemaVersion     int     `json:"schema_version"`
	RequestID         string  `json:"request_id"`
	StartedAtMs       int64   `json:"started_at_ms"`
	FinishedAtMs      *int64  `json:"finished_at_ms"`
	Protocol          string  `json:"protocol"`
	RequestedModel    string  `json:"requested_model"`
	ResolvedModel     string  `json:"resolved_model"`
	Stream            bool    `json:"stream"`
	Status            string  `json:"status"`
	HTTPStatus        int     `json:"http_status"`
	DurationMs        *int64  `json:"duration_ms"`
	FirstTokenMs      *int64  `json:"first_token_ms"`
	FinalAccountID    *string `json:"final_account_id"`
	AttemptCount      int     `json:"attempt_count"`
	ErrorCode         string  `json:"error_code"`
	PartialResponse   bool    `json:"partial_response"`
	CompletionEmitted bool    `json:"completion_emitted"`
	RequestUsageTotals
}

type RequestAttempt struct {
	RequestID         string `json:"request_id"`
	AttemptNo         int    `json:"attempt_no"`
	AccountID         string `json:"account_id"`
	StartedAtMs       int64  `json:"started_at_ms"`
	FinishedAtMs      *int64 `json:"finished_at_ms"`
	DurationMs        *int64 `json:"duration_ms"`
	Outcome           string `json:"outcome"`
	ErrorCode         string `json:"error_code"`
	RetryReason       string `json:"retry_reason"`
	CooldownUntilMs   *int64 `json:"cooldown_until_ms"`
	ResponseCommitted bool   `json:"response_committed"`
	ContentEmitted    bool   `json:"content_emitted"`
	ToolCallEmitted   bool   `json:"tool_call_emitted"`
	RequestUsageTotals
}

type RequestLogCoverage struct {
	StorageStatus string `json:"storage_status"`
	CapturedSince int64  `json:"captured_since"`
	RetainedFrom  int64  `json:"retained_from"`
	DataComplete  bool   `json:"data_complete"`
	RetentionDays int    `json:"retention_days"`
}

type RequestLogsResponse struct {
	Items      []RequestLog `json:"items"`
	Total      int64        `json:"total"`
	NextCursor *string      `json:"next_cursor"`
	RequestLogCoverage
}

type RequestLogDetail struct {
	Request  RequestLog       `json:"request"`
	Attempts []RequestAttempt `json:"attempts"`
	RequestLogCoverage
}

type RequestLogFilter struct {
	From      int64
	To        int64
	Status    string
	Model     string
	Protocol  string
	AccountID string
	Query     string
	Limit     int
	Cursor    string
}

type RequestLogStore struct {
	db            *sql.DB
	mu            sync.RWMutex
	capturedSince int64
	retainedFrom  int64
	retentionDays int
	complete      bool
	degraded      bool
	closed        bool
	stop          chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
	gapPath       string
}

func NewRequestLogStore(path string, retentionDays ...int) (*RequestLogStore, error) {
	days := 30
	if len(retentionDays) > 0 {
		days = retentionDays[0]
	}
	if days < 1 || days > 3650 {
		return nil, errors.New("retention_days must be between 1 and 3650")
	}
	if path == "" {
		return nil, errors.New("request log path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &RequestLogStore{db: db, retentionDays: days, complete: true, stop: make(chan struct{}), done: make(chan struct{}), gapPath: path + ".gap"}
	if err = s.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err = os.Chmod(path+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			_ = db.Close()
			return nil, err
		}
	}
	if err = s.Prune(time.Now()); err != nil {
		_ = db.Close()
		return nil, err
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = s.Prune(time.Now())
			case <-s.stop:
				return
			}
		}
	}()
	return s, nil
}

func (s *RequestLogStore) initialize() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != requestLogSchemaVersion {
		return fmt.Errorf("unsupported request log schema version %d", version)
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=1500"} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if version == 0 {
		var tables int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return errors.New("unversioned request log database is not empty")
		}
		for _, statement := range []string{
			`CREATE TABLE request_log_meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL)`,
			`CREATE TABLE requests (request_id TEXT PRIMARY KEY, started_at_ms INTEGER NOT NULL, status TEXT NOT NULL, protocol TEXT NOT NULL, requested_model TEXT NOT NULL, resolved_model TEXT NOT NULL, record_json TEXT NOT NULL CHECK(json_valid(record_json)))`,
			`CREATE TABLE request_attempts (request_id TEXT NOT NULL REFERENCES requests(request_id) ON DELETE CASCADE, attempt_no INTEGER NOT NULL, account_id TEXT NOT NULL, record_json TEXT NOT NULL CHECK(json_valid(record_json)), PRIMARY KEY(request_id, attempt_no))`,
			`CREATE INDEX requests_time ON requests(started_at_ms DESC, request_id DESC)`,
			`CREATE INDEX requests_model ON requests(resolved_model, started_at_ms)`,
			`CREATE INDEX requests_status ON requests(status, started_at_ms)`,
			`CREATE INDEX attempts_account ON request_attempts(account_id, request_id)`,
			`PRAGMA user_version=1`,
		} {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO request_log_meta(key,value) VALUES ('captured_since',?),('data_complete',1)", time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE requests SET status='interrupted', record_json=json_set(record_json, '$.status','interrupted', '$.error_code','process_interrupted') WHERE status='in_progress'`); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, "SELECT value FROM request_log_meta WHERE key='captured_since'").Scan(&s.capturedSince); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO request_log_meta(key,value) VALUES ('retained_from',?)", s.capturedSince); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, "SELECT value FROM request_log_meta WHERE key='retained_from'").Scan(&s.retainedFrom); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, "SELECT value FROM request_log_meta WHERE key='data_complete'").Scan(&s.complete); err != nil {
		return err
	}
	if _, markerErr := os.Stat(s.gapPath); markerErr == nil {
		s.complete = false
	} else if !os.IsNotExist(markerErr) {
		return markerErr
	}
	if err = s.persistGap(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.clearGapMarker()
	return nil
}

func (s *RequestLogStore) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		err = s.flushGap()
		if closeErr := s.db.Close(); err == nil {
			err = closeErr
		}
	})
	return err
}

func (s *RequestLogStore) RetentionDays() int {
	if s == nil {
		return 30
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retentionDays
}

func (s *RequestLogStore) SetRetentionDays(days int) error {
	if days < 1 || days > 3650 {
		return errors.New("retention_days must be between 1 and 3650")
	}
	if s == nil {
		return errors.New("request log storage unavailable")
	}
	s.mu.Lock()
	if s.retentionDays == days {
		s.mu.Unlock()
		return nil
	}
	s.retentionDays = days
	s.mu.Unlock()
	return s.Prune(time.Now())
}

func (s *RequestLogStore) Coverage() RequestLogCoverage {
	if s == nil {
		return RequestLogCoverage{StorageStatus: "unavailable", RetentionDays: 30}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := "ok"
	if s.degraded {
		status = "degraded"
	}
	if s.closed {
		status = "unavailable"
	}
	return RequestLogCoverage{StorageStatus: status, CapturedSince: s.capturedSince, RetainedFrom: max(s.capturedSince, s.retainedFrom), DataComplete: s.complete && !s.closed, RetentionDays: s.retentionDays}
}

func (s *RequestLogStore) failure(err error) error {
	if err != nil && s != nil {
		s.mu.Lock()
		s.degraded = true
		s.complete = false
		s.mu.Unlock()
		// The marker contains no request data and survives a temporarily
		// unwritable database; failure of both media remains observable live.
		if marker, markerErr := os.OpenFile(s.gapPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); markerErr == nil {
			_, _ = marker.WriteString("incomplete\n")
			_ = marker.Sync()
			_ = marker.Close()
		}
	}
	return err
}

func (s *RequestLogStore) persistGap(ctx context.Context, tx *sql.Tx) error {
	s.mu.RLock()
	complete := s.complete
	s.mu.RUnlock()
	if !complete {
		_, err := tx.ExecContext(ctx, "UPDATE request_log_meta SET value=0 WHERE key='data_complete'")
		return err
	}
	return nil
}

func (s *RequestLogStore) clearGapMarker() {
	s.mu.RLock()
	incomplete := !s.complete
	s.mu.RUnlock()
	if incomplete {
		_ = os.Remove(s.gapPath)
	}
}

func (s *RequestLogStore) flushGap() error {
	s.mu.RLock()
	incomplete := !s.complete
	s.mu.RUnlock()
	if !incomplete {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, "UPDATE request_log_meta SET value=0 WHERE key='data_complete'"); err != nil {
		return s.failure(err)
	}
	s.clearGapMarker()
	return nil
}

func (s *RequestLogStore) Prune(now time.Time) error {
	if s == nil {
		return errors.New("request log storage unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cutoff := now.AddDate(0, 0, -s.RetentionDays()).UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return s.failure(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM requests WHERE started_at_ms < ? AND status <> 'in_progress'", cutoff); err != nil {
		return s.failure(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE request_log_meta SET value=MAX(value,?) WHERE key='retained_from'", cutoff); err != nil {
		return s.failure(err)
	}
	var watermark int64
	if err = tx.QueryRowContext(ctx, "SELECT value FROM request_log_meta WHERE key='retained_from'").Scan(&watermark); err != nil {
		return s.failure(err)
	}
	if err = s.persistGap(ctx, tx); err != nil {
		return s.failure(err)
	}
	if err = tx.Commit(); err != nil {
		return s.failure(err)
	}
	s.mu.Lock()
	s.retainedFrom = max(s.retainedFrom, watermark)
	s.mu.Unlock()
	return s.flushGap()
}

func (s *RequestLogStore) BeginRequest(record RequestLog) error {
	if s == nil {
		return errors.New("request log storage unavailable")
	}
	record = sanitizeRequestLog(record)
	record.Status = "in_progress"
	raw, err := json.Marshal(record)
	if err != nil {
		return s.failure(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = s.db.ExecContext(ctx, `INSERT INTO requests(request_id,started_at_ms,status,protocol,requested_model,resolved_model,record_json) VALUES (?,?,?,?,?,?,?) ON CONFLICT(request_id) DO NOTHING`, record.RequestID, record.StartedAtMs, record.Status, record.Protocol, record.RequestedModel, record.ResolvedModel, string(raw))
	if err != nil {
		return s.failure(err)
	}
	return s.flushGap()
}

// FinalizeRequest is the only terminal write, including all account attempts.
func (s *RequestLogStore) FinalizeRequest(record RequestLog, attempts []RequestAttempt) error {
	if s == nil {
		return errors.New("request log storage unavailable")
	}
	record = sanitizeRequestLog(record)
	if record.Status == "in_progress" {
		record.Status = "failed"
		record.ErrorCode = "unfinished_request"
	}
	record.AttemptCount = len(attempts)
	record.RequestUsageTotals = RequestUsageTotals{}
	cleanAttempts := make([]RequestAttempt, len(attempts))
	for i, attempt := range attempts {
		attempt.RequestID = record.RequestID
		attempt.AttemptNo = i + 1
		attempt.AccountID = safeAccountID(attempt.AccountID)
		attempt.ErrorCode = safeRequestCode(attempt.ErrorCode)
		attempt.RetryReason = safeRequestCode(attempt.RetryReason)
		if !validRequestStatus(attempt.Outcome) || attempt.Outcome == "in_progress" {
			attempt.Outcome = "interrupted"
		}
		attempt.RequestUsageTotals.normalize()
		cleanAttempts[i] = attempt
		record.RequestUsageTotals.merge(attempt.RequestUsageTotals)
	}
	record.RequestUsageTotals.normalize()
	raw, err := json.Marshal(record)
	if err != nil {
		return s.failure(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return s.failure(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE requests SET status=?, requested_model=?, resolved_model=?, record_json=? WHERE request_id=? AND status='in_progress'`, record.Status, record.RequestedModel, record.ResolvedModel, string(raw), record.RequestID)
	if err != nil {
		return s.failure(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return s.failure(err)
	}
	if n == 0 {
		_ = tx.Rollback()
		return s.flushGap()
	}
	for _, attempt := range cleanAttempts {
		raw, err = json.Marshal(attempt)
		if err != nil {
			return s.failure(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO request_attempts(request_id,attempt_no,account_id,record_json) VALUES (?,?,?,?)`, record.RequestID, attempt.AttemptNo, attempt.AccountID, string(raw)); err != nil {
			return s.failure(err)
		}
	}
	if err = s.persistGap(ctx, tx); err != nil {
		return s.failure(err)
	}
	if err = tx.Commit(); err != nil {
		return s.failure(err)
	}
	return s.flushGap()
}

func sanitizeRequestLog(record RequestLog) RequestLog {
	record.SchemaVersion = requestLogSchemaVersion
	record.RequestID = safeRequestMetadata(record.RequestID)
	record.RequestedModel = safeRequestMetadata(record.RequestedModel)
	record.ResolvedModel = safeRequestMetadata(record.ResolvedModel)
	if !validRequestProtocol(record.Protocol) {
		record.Protocol = "anthropic_messages"
	}
	if !validRequestStatus(record.Status) {
		record.Status = "failed"
	}
	record.ErrorCode = safeRequestCode(record.ErrorCode)
	if record.FinalAccountID != nil {
		value := safeAccountID(*record.FinalAccountID)
		if value == "" {
			record.FinalAccountID = nil
		} else {
			record.FinalAccountID = &value
		}
	}
	return record
}

func safeRequestMetadata(value string) string {
	if len(value) > 128 {
		return "unknown"
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:/-", c)) {
			return "unknown"
		}
	}
	return value
}

func safeAccountID(value string) string {
	if len(value) != 64 {
		return ""
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return ""
		}
	}
	return value
}

func safeRequestCode(value string) string {
	if value == "" {
		return ""
	}
	switch value {
	case "quota_exhausted", "research_quota_exhausted", "premium_feature_unavailable", "empty_response", "tool_bridge_no_tool", "all_accounts_exhausted", "pool_unavailable", "retry_budget_exhausted", "unauthorized", "invalid_request", "unsupported_model", "upload_failed", "upstream_error", "upstream_timeout", "transport_error", "transcode_error", "write_failed", "client_cancelled", "process_interrupted", "internal_error", "unfinished_request", "rate_limited", "auth_expired", "workspace_replay_blocked", "model_unavailable", "forbidden", "no_accounts", "method_not_allowed", "account_unavailable", "cancelled", "timeout", "unsafe_replay", "concurrency_limit", "invalid_credentials", "upstream_forbidden", "research_exhausted", "premium_unavailable", "tool_bridge_error", "api_error", "invalid_request_error", "overloaded_error":
		return value
	default:
		return "internal_error"
	}
}

func validRequestStatus(value string) bool {
	switch value {
	case "in_progress", "success", "rejected", "failed", "cancelled", "interrupted":
		return true
	}
	return false
}

func validRequestProtocol(value string) bool {
	return value == "anthropic_messages" || value == "openai_chat" || value == "openai_responses"
}

func addRequestTokens(a **int64, b *int64) {
	if b == nil || *b < 0 {
		return
	}
	if *a == nil {
		value := *b
		*a = &value
	} else {
		value := **a + *b
		*a = &value
	}
}

func (u *RequestUsageTotals) add(observation RequestUsage) {
	u.UpstreamCallCount++
	switch observation.Source {
	case "actual":
		u.ReportedCalls++
		addRequestTokens(&u.ReportedInputTokens, observation.InputTokens)
		addRequestTokens(&u.ReportedOutputTokens, observation.OutputTokens)
	case "estimated":
		u.EstimatedCalls++
		addRequestTokens(&u.EstimatedInputTokens, observation.InputTokens)
		addRequestTokens(&u.EstimatedOutputTokens, observation.OutputTokens)
	default:
		u.UnknownCalls++
	}
	if observation.Completeness != "complete" || observation.InputTokens == nil || observation.OutputTokens == nil {
		u.UsageCompleteness = "partial"
	}
	u.normalize()
}

func (u *RequestUsageTotals) merge(other RequestUsageTotals) {
	addRequestTokens(&u.ReportedInputTokens, other.ReportedInputTokens)
	addRequestTokens(&u.ReportedOutputTokens, other.ReportedOutputTokens)
	addRequestTokens(&u.EstimatedInputTokens, other.EstimatedInputTokens)
	addRequestTokens(&u.EstimatedOutputTokens, other.EstimatedOutputTokens)
	u.UpstreamCallCount += other.UpstreamCallCount
	u.ReportedCalls += other.ReportedCalls
	u.EstimatedCalls += other.EstimatedCalls
	u.UnknownCalls += other.UnknownCalls
	if other.UsageCompleteness == "partial" || other.UsageCompleteness == "unknown" && other.UpstreamCallCount > 0 {
		u.UsageCompleteness = "partial"
	}
	u.normalize()
}

func (u *RequestUsageTotals) normalize() {
	u.InputTokens = nil
	u.OutputTokens = nil
	switch {
	case u.ReportedCalls > 0 && u.EstimatedCalls == 0 && u.UnknownCalls == 0:
		u.UsageSource = "actual"
		u.InputTokens = u.ReportedInputTokens
		u.OutputTokens = u.ReportedOutputTokens
	case u.EstimatedCalls > 0 && u.ReportedCalls == 0 && u.UnknownCalls == 0:
		u.UsageSource = "estimated"
		u.InputTokens = u.EstimatedInputTokens
		u.OutputTokens = u.EstimatedOutputTokens
	case u.ReportedCalls > 0 || u.EstimatedCalls > 0:
		u.UsageSource = "mixed"
	default:
		u.UsageSource = "unknown"
	}
	if u.UpstreamCallCount == 0 || u.UsageSource == "unknown" {
		u.UsageCompleteness = "unknown"
	} else if u.UsageCompleteness == "partial" || u.UnknownCalls > 0 {
		u.UsageCompleteness = "partial"
	} else {
		u.UsageCompleteness = "complete"
	}
}

type requestLogCursor struct {
	StartedAtMs int64  `json:"t"`
	RequestID   string `json:"id"`
}

func (f RequestLogFilter) where() (string, []any) {
	clauses := []string{"r.started_at_ms >= ?", "r.started_at_ms < ?"}
	args := []any{f.From, f.To}
	for _, field := range []struct{ column, value string }{{"r.status", f.Status}, {"r.protocol", f.Protocol}} {
		if field.value != "" {
			clauses = append(clauses, field.column+" = ?")
			args = append(args, field.value)
		}
	}
	if f.Model != "" {
		clauses = append(clauses, "(r.requested_model = ? OR r.resolved_model = ?)")
		args = append(args, f.Model, f.Model)
	}
	if f.AccountID != "" {
		clauses = append(clauses, "EXISTS (SELECT 1 FROM request_attempts a WHERE a.request_id=r.request_id AND a.account_id=?)")
		args = append(args, f.AccountID)
	}
	if f.Query != "" {
		query := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(f.Query) + "%"
		clauses = append(clauses, `(r.request_id LIKE ? ESCAPE '\' OR r.requested_model LIKE ? ESCAPE '\' OR r.resolved_model LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM request_attempts a WHERE a.request_id=r.request_id AND a.account_id LIKE ? ESCAPE '\'))`)
		args = append(args, query, query, query, query)
	}
	return strings.Join(clauses, " AND "), args
}

func (s *RequestLogStore) List(filter RequestLogFilter) (RequestLogsResponse, error) {
	response := RequestLogsResponse{Items: []RequestLog{}, RequestLogCoverage: s.coverageFor(filter)}
	if s == nil {
		return response, errors.New("request log storage unavailable")
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	where, args := filter.where()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return response, s.failure(err)
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM requests r WHERE "+where, args...).Scan(&response.Total); err != nil {
		return response, s.failure(err)
	}
	if filter.Cursor != "" {
		var cursor requestLogCursor
		raw, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.RequestID == "" {
			return response, errors.New("invalid cursor")
		}
		where += " AND (r.started_at_ms < ? OR (r.started_at_ms = ? AND r.request_id < ?))"
		args = append(args, cursor.StartedAtMs, cursor.StartedAtMs, cursor.RequestID)
	}
	args = append(args, filter.Limit+1)
	rows, err := tx.QueryContext(ctx, "SELECT r.record_json FROM requests r WHERE "+where+" ORDER BY r.started_at_ms DESC,r.request_id DESC LIMIT ?", args...)
	if err != nil {
		return response, s.failure(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item RequestLog
		if err = rows.Scan(&raw); err != nil {
			return response, s.failure(err)
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return response, s.failure(err)
		}
		response.Items = append(response.Items, item)
	}
	if err = rows.Err(); err != nil {
		return response, s.failure(err)
	}
	if len(response.Items) > filter.Limit {
		response.Items = response.Items[:filter.Limit]
		last := response.Items[len(response.Items)-1]
		raw, _ := json.Marshal(requestLogCursor{last.StartedAtMs, last.RequestID})
		cursor := base64.RawURLEncoding.EncodeToString(raw)
		response.NextCursor = &cursor
	}
	response.RequestLogCoverage = s.coverageFor(filter)
	return response, nil
}

func (s *RequestLogStore) Detail(id string) (RequestLogDetail, error) {
	response := RequestLogDetail{Attempts: []RequestAttempt{}, RequestLogCoverage: s.Coverage()}
	if s == nil {
		return response, errors.New("request log storage unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return response, s.failure(err)
	}
	defer tx.Rollback()
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT record_json FROM requests WHERE request_id=?", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return response, err
		}
		return response, s.failure(err)
	}
	if err := json.Unmarshal(raw, &response.Request); err != nil {
		return response, s.failure(err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT record_json FROM request_attempts WHERE request_id=? ORDER BY attempt_no", id)
	if err != nil {
		return response, s.failure(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item RequestAttempt
		if err = rows.Scan(&raw); err != nil {
			return response, s.failure(err)
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return response, s.failure(err)
		}
		response.Attempts = append(response.Attempts, item)
	}
	return response, s.failure(rows.Err())
}

func parseRequestLogFilter(r *http.Request) (RequestLogFilter, error) {
	q := r.URL.Query()
	now := time.Now()
	filter := RequestLogFilter{From: now.Add(-24 * time.Hour).UnixMilli(), To: now.UnixMilli() + 1, Status: q.Get("status"), Model: q.Get("model"), Protocol: q.Get("protocol"), AccountID: q.Get("account_id"), Query: q.Get("q"), Limit: 50, Cursor: q.Get("cursor")}
	for _, field := range []struct {
		key    string
		target *int64
	}{{"from", &filter.From}, {"to", &filter.To}} {
		if value := q.Get(field.key); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				parsed, e := time.Parse(time.RFC3339Nano, value)
				if e != nil {
					return filter, errors.New("invalid time")
				}
				n = parsed.UnixMilli()
			}
			*field.target = n
		}
	}
	if filter.From < 0 || filter.To <= filter.From || filter.To-filter.From > 3660*24*int64(time.Hour/time.Millisecond) {
		return filter, errors.New("invalid time range")
	}
	if filter.Status != "" && !validRequestStatus(filter.Status) {
		return filter, errors.New("invalid status")
	}
	if filter.Protocol != "" && !validRequestProtocol(filter.Protocol) {
		return filter, errors.New("invalid protocol")
	}
	if filter.AccountID != "" && safeAccountID(filter.AccountID) == "" {
		return filter, errors.New("invalid account_id")
	}
	if len(filter.Model) > 128 || len(filter.Query) > 128 || len(filter.Cursor) > 512 {
		return filter, errors.New("filter too long")
	}
	if value := q.Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 200 {
			return filter, errors.New("invalid limit")
		}
		filter.Limit = n
	}
	return filter, nil
}

func requestLogAuthorized(w http.ResponseWriter, r *http.Request, auth *DashboardAuth) bool {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if auth == nil || !auth.ValidateSession(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "dashboard login required", "error_code": "unauthorized"})
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed", "error_code": "method_not_allowed"})
		return false
	}
	return true
}

func writeRequestLogError(w http.ResponseWriter, store *RequestLogStore, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error     string `json:"error"`
		ErrorCode string `json:"error_code"`
		RequestLogCoverage
	}{"request log query unavailable", code, store.Coverage()})
}

func HandleRequestLogs(store *RequestLogStore, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requestLogAuthorized(w, r, auth) {
			return
		}
		filter, err := parseRequestLogFilter(r)
		if err != nil {
			writeRequestLogError(w, store, 400, "invalid_filter")
			return
		}
		if filter.Cursor != "" {
			var cursor requestLogCursor
			raw, e := base64.RawURLEncoding.DecodeString(filter.Cursor)
			if e != nil || json.Unmarshal(raw, &cursor) != nil || cursor.RequestID == "" {
				writeRequestLogError(w, store, 400, "invalid_cursor")
				return
			}
		}
		result, err := store.List(filter)
		if err != nil {
			writeRequestLogError(w, store, 503, "storage_unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}
}

func HandleRequestLogDetail(store *RequestLogStore, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requestLogAuthorized(w, r, auth) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/admin/request-logs/")
		if id == "" || strings.Contains(id, "/") || len(id) > 128 {
			writeRequestLogError(w, store, 400, "invalid_request_id")
			return
		}
		result, err := store.Detail(id)
		if errors.Is(err, sql.ErrNoRows) {
			writeRequestLogError(w, store, 404, "not_found")
			return
		}
		if err != nil {
			writeRequestLogError(w, store, 503, "storage_unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}
}
