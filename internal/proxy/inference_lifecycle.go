package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

var inferenceCaller = CallInference

type anthropicBridgeContextKey struct{}

// inferenceWriter observes delivery without buffering responses or retaining content.
type inferenceWriter struct {
	http.ResponseWriter
	ctx                      context.Context
	cancel                   context.CancelFunc
	committed, content, tool bool
	writeErr                 error
}

func (w *inferenceWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *inferenceWriter) WriteHeader(status int) {
	if !w.committed {
		w.committed = true
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *inferenceWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.committed = true
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.writeErr = err
		w.cancel()
	}
	return n, err
}
func (w *inferenceWriter) Flush() {
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.writeErr = err
		w.cancel()
	}
}
func inferenceContext(w http.ResponseWriter) context.Context {
	if iw, ok := w.(*inferenceWriter); ok {
		return iw.ctx
	}
	return context.Background()
}

type notionHTTPError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *notionHTTPError) Error() string { return fmt.Sprintf("upstream HTTP %d", e.Status) }

func parseRetryAfter(value string) time.Duration {
	if n, err := strconv.Atoi(value); err == nil && n > 0 {
		return time.Duration(min(n, 86400*31)) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil && t.After(time.Now()) {
		d := time.Until(t)
		if d > 31*24*time.Hour {
			d = 31 * 24 * time.Hour
		}
		return d
	}
	return 0
}

func inferenceErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "upstream_timeout"
	case errors.Is(err, ErrQuotaExhausted):
		return "quota_exhausted"
	case errors.Is(err, ErrResearchQuotaExhausted):
		return "research_exhausted"
	case errors.Is(err, ErrPremiumFeatureUnavailable):
		return "premium_unavailable"
	case errors.Is(err, ErrEmptyResponse):
		return "empty_response"
	case errors.Is(err, ErrToolBridgeNoTool):
		return "tool_bridge_error"
	}
	var he *notionHTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case 400:
			return "invalid_request"
		case 401:
			return "invalid_credentials"
		case 403:
			return "upstream_forbidden"
		case 404:
			return "model_unavailable"
		case 429:
			return "rate_limited"
		}
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return "transport_error"
	}
	return "upstream_error"
}

func canReplayInference(err error, committed, sideEffects, readOnly bool) bool {
	if committed || sideEffects || errors.Is(err, context.Canceled) {
		return false
	}
	var he *notionHTTPError
	if errors.As(err, &he) {
		if he.Status == 401 || he.Status == 404 || he.Status == 429 {
			return true
		}
		return readOnly && he.Status >= 500
	}
	if !readOnly {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, ErrQuotaExhausted) || errors.Is(err, ErrResearchQuotaExhausted) || errors.Is(err, ErrPremiumFeatureUnavailable) || errors.Is(err, ErrEmptyResponse) || errors.Is(err, context.DeadlineExceeded)
}
