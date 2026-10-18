package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"
)

type RequestAnalyticsSummary struct {
	TotalRequests int64    `json:"total_requests"`
	InProgress    int64    `json:"in_progress"`
	Success       int64    `json:"success"`
	Rejected      int64    `json:"rejected"`
	Failed        int64    `json:"failed"`
	Cancelled     int64    `json:"cancelled"`
	Interrupted   int64    `json:"interrupted"`
	SuccessRate   *float64 `json:"success_rate"`
	AvgDurationMs *float64 `json:"avg_duration_ms"`
	P50DurationMs *int64   `json:"p50_duration_ms"`
	P95DurationMs *int64   `json:"p95_duration_ms"`
	RequestUsageTotals
}

type RequestAnalyticsPoint struct {
	StartedAtMs   int64 `json:"started_at_ms"`
	TotalRequests int64 `json:"total_requests"`
	InProgress    int64 `json:"in_progress"`
	Success       int64 `json:"success"`
	Rejected      int64 `json:"rejected"`
	Failed        int64 `json:"failed"`
	Cancelled     int64 `json:"cancelled"`
	Interrupted   int64 `json:"interrupted"`
}

type RequestModelAnalytics struct {
	Model string `json:"model"`
	RequestAnalyticsSummary
}

type RequestAccountAnalytics struct {
	AccountID string `json:"account_id"`
	Requests  int64  `json:"requests"`
	Attempts  int64  `json:"attempts"`
	Success   int64  `json:"success"`
	Failed    int64  `json:"failed"`
	Cancelled int64  `json:"cancelled"`
	RequestUsageTotals
}

type RequestAnalyticsResponse struct {
	From     int64                     `json:"from"`
	To       int64                     `json:"to"`
	BucketMs int64                     `json:"bucket_ms"`
	Overview RequestAnalyticsSummary   `json:"overview"`
	Timeline []RequestAnalyticsPoint   `json:"timeline"`
	Models   []RequestModelAnalytics   `json:"models"`
	Accounts []RequestAccountAnalytics `json:"accounts"`
	RequestLogCoverage
}

type requestAnalyticsAccumulator struct {
	summary   RequestAnalyticsSummary
	durations []int64
}

func (a *requestAnalyticsAccumulator) add(record RequestLog) {
	a.summary.TotalRequests++
	switch record.Status {
	case "in_progress":
		a.summary.InProgress++
	case "success":
		a.summary.Success++
	case "rejected":
		a.summary.Rejected++
	case "failed":
		a.summary.Failed++
	case "cancelled":
		a.summary.Cancelled++
	case "interrupted":
		a.summary.Interrupted++
	}
	if record.Status == "success" && record.DurationMs != nil {
		a.durations = append(a.durations, *record.DurationMs)
	}
	a.summary.RequestUsageTotals.merge(record.RequestUsageTotals)
}

func (a *requestAnalyticsAccumulator) result() RequestAnalyticsSummary {
	if terminal := a.summary.TotalRequests - a.summary.InProgress; terminal > 0 {
		rate := 100 * float64(a.summary.Success) / float64(terminal)
		a.summary.SuccessRate = &rate
	}
	if n := len(a.durations); n > 0 {
		sort.Slice(a.durations, func(i, j int) bool { return a.durations[i] < a.durations[j] })
		var total float64
		for _, duration := range a.durations {
			total += float64(duration)
		}
		average := total / float64(n)
		a.summary.AvgDurationMs = &average
		p50 := a.durations[int(math.Ceil(float64(n)*0.50))-1]
		p95 := a.durations[int(math.Ceil(float64(n)*0.95))-1]
		a.summary.P50DurationMs = &p50
		a.summary.P95DurationMs = &p95
	}
	a.summary.RequestUsageTotals.normalize()
	return a.summary
}

func (s *RequestLogStore) coverageFor(filter RequestLogFilter) RequestLogCoverage {
	coverage := s.Coverage()
	if filter.From < coverage.RetainedFrom {
		coverage.DataComplete = false
	}
	return coverage
}

// Analytics consumes every matching row. Only successful duration samples are
// retained for exact nearest-rank percentiles; request bodies are never stored.
func (s *RequestLogStore) Analytics(filter RequestLogFilter) (RequestAnalyticsResponse, error) {
	const hourMs = int64(time.Hour / time.Millisecond)
	response := RequestAnalyticsResponse{From: filter.From, To: filter.To, BucketMs: hourMs, Timeline: []RequestAnalyticsPoint{}, Models: []RequestModelAnalytics{}, Accounts: []RequestAccountAnalytics{}, RequestLogCoverage: s.coverageFor(filter)}
	if s == nil {
		return response, errors.New("request log storage unavailable")
	}
	if filter.To <= filter.From || filter.To-filter.From > 3660*24*hourMs {
		return response, errors.New("invalid time range")
	}
	first := filter.From / hourMs * hourMs
	for bucket := first; bucket < filter.To; bucket += hourMs {
		response.Timeline = append(response.Timeline, RequestAnalyticsPoint{StartedAtMs: bucket})
	}
	where, args := filter.where()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return response, s.failure(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT r.record_json FROM requests r WHERE "+where, args...)
	if err != nil {
		return response, s.failure(err)
	}
	var overall requestAnalyticsAccumulator
	models := map[string]*requestAnalyticsAccumulator{}
	for rows.Next() {
		var raw []byte
		var record RequestLog
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &record); err != nil {
			break
		}
		overall.add(record)
		model := record.ResolvedModel
		if model == "" {
			model = record.RequestedModel
		}
		if model == "" {
			model = "unknown"
		}
		if models[model] == nil {
			models[model] = &requestAnalyticsAccumulator{}
		}
		models[model].add(record)
		point := &response.Timeline[(record.StartedAtMs-first)/hourMs]
		point.TotalRequests++
		switch record.Status {
		case "in_progress":
			point.InProgress++
		case "success":
			point.Success++
		case "rejected":
			point.Rejected++
		case "failed":
			point.Failed++
		case "cancelled":
			point.Cancelled++
		case "interrupted":
			point.Interrupted++
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return response, s.failure(err)
	}
	response.Overview = overall.result()
	for model, accumulator := range models {
		response.Models = append(response.Models, RequestModelAnalytics{Model: model, RequestAnalyticsSummary: accumulator.result()})
	}
	sort.Slice(response.Models, func(i, j int) bool {
		if response.Models[i].TotalRequests != response.Models[j].TotalRequests {
			return response.Models[i].TotalRequests > response.Models[j].TotalRequests
		}
		return response.Models[i].Model < response.Models[j].Model
	})
	rows, err = tx.QueryContext(ctx, "SELECT a.record_json FROM request_attempts a JOIN requests r ON r.request_id=a.request_id WHERE "+where+" ORDER BY a.account_id,a.request_id,a.attempt_no", args...)
	if err != nil {
		return response, s.failure(err)
	}
	accounts := map[string]*RequestAccountAnalytics{}
	lastRequest := map[string]string{}
	for rows.Next() {
		var raw []byte
		var attempt RequestAttempt
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &attempt); err != nil {
			break
		}
		if attempt.AccountID == "" {
			continue
		}
		account := accounts[attempt.AccountID]
		if account == nil {
			account = &RequestAccountAnalytics{AccountID: attempt.AccountID}
			accounts[attempt.AccountID] = account
		}
		account.Attempts++
		if lastRequest[attempt.AccountID] != attempt.RequestID {
			account.Requests++
			lastRequest[attempt.AccountID] = attempt.RequestID
		}
		switch attempt.Outcome {
		case "success":
			account.Success++
		case "cancelled":
			account.Cancelled++
		default:
			account.Failed++
		}
		account.RequestUsageTotals.merge(attempt.RequestUsageTotals)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return response, s.failure(err)
	}
	for _, account := range accounts {
		account.RequestUsageTotals.normalize()
		response.Accounts = append(response.Accounts, *account)
	}
	sort.Slice(response.Accounts, func(i, j int) bool {
		if response.Accounts[i].Requests != response.Accounts[j].Requests {
			return response.Accounts[i].Requests > response.Accounts[j].Requests
		}
		return response.Accounts[i].AccountID < response.Accounts[j].AccountID
	})
	response.RequestLogCoverage = s.coverageFor(filter)
	return response, nil
}

func HandleAnalytics(store *RequestLogStore, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requestLogAuthorized(w, r, auth) {
			return
		}
		filter, err := parseRequestLogFilter(r)
		if err != nil {
			writeRequestLogError(w, store, http.StatusBadRequest, "invalid_filter")
			return
		}
		result, err := store.Analytics(filter)
		if err != nil {
			writeRequestLogError(w, store, http.StatusServiceUnavailable, "storage_unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}
}
