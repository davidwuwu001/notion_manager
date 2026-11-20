package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type modelProbe func(context.Context, *Account, string) (bool, error)

func modelProbeFailure(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, ErrQuotaExhausted) || errors.Is(err, ErrResearchQuotaExhausted) {
		return "quota_exhausted"
	}
	if errors.Is(err, ErrPremiumFeatureUnavailable) {
		return "model_unavailable"
	}
	if errors.Is(err, ErrEmptyResponse) || err == nil {
		return "empty_response"
	}
	var upstream *notionHTTPError
	if errors.As(err, &upstream) {
		switch upstream.Status {
		case 401:
			return "authentication_failed"
		case 403:
			return "permission_denied"
		case 404:
			return "model_unavailable"
		case 429:
			return "rate_limited"
		default:
			return "upstream_error"
		}
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "timeout"
		}
		return "network_error"
	}
	return "inference_failed"
}

func HandleAccountModelTest(pool *AccountPool, auth *DashboardAuth) http.HandlerFunc {
	return accountModelTestHandler(pool, auth, func(ctx context.Context, acc *Account, model string) (bool, error) {
		hasText := false
		search := false
		err := CallInference(acc, []ChatMessage{{Role: "user", Content: "Reply only OK."}}, model, true, func(delta string, done bool, usage *UsageInfo) {
			if strings.TrimSpace(delta) != "" {
				hasText = true
			}
		}, CallOptions{Context: ctx, UseReadOnlyMode: true, EnableWorkspaceSearch: &search})
		return hasText, err
	})
}

func accountModelTestHandler(pool *AccountPool, auth *DashboardAuth, probe modelProbe) http.HandlerFunc {
	gate := make(chan struct{}, 1)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if !auth.ValidateSession(r) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host && r.Header.Get("Origin") != "https://"+r.Host) {
			http.Error(w, `{"error":"cross_origin"}`, http.StatusForbidden)
			return
		}
		var body struct {
			AccountID string `json:"account_id"`
			Model     string `json:"model"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		acc := pool.FindByAccountID(body.AccountID)
		if acc == nil {
			http.Error(w, `{"error":"account_not_found"}`, http.StatusNotFound)
			return
		}
		baseModel, _ := StripAskModeSuffix(body.Model)
		if IsResearcherModel(baseModel) {
			http.Error(w, `{"error":"model_unavailable"}`, http.StatusBadRequest)
			return
		}
		found := false
		if IsAutoModel(body.Model) {
			found = true
		} else {
			for _, m := range acc.modelsSnapshot() {
				if m.ID == body.Model {
					found = true
					break
				}
			}
			if !found {
				http.Error(w, `{"error":"model_unavailable"}`, http.StatusBadRequest)
				return
			}
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			http.Error(w, `{"error":"test_busy"}`, http.StatusTooManyRequests)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		start := time.Now()
		ok, err := probe(ctx, acc, body.Model)
		if err != nil || !ok {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "reason": modelProbeFailure(err), "duration_ms": time.Since(start).Milliseconds()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "duration_ms": time.Since(start).Milliseconds()})
	}
}
