package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestCorrelationAndStageDurations(t *testing.T) {
	store, err := NewRequestLogStore(filepath.Join(t.TempDir(), "logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var requestID string
	handler := RequestLogMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID = RequestIDFromContext(r.Context())
		trace := TraceRequest(r.Context())
		trace.RecordStage("quota_check", 2*time.Millisecond)
		trace.RecordStage("quota_check", 3*time.Millisecond)
		trace.RecordStage("tool_parse", 0)
		trace.RecordStage("prompt=SECRET", time.Second)
		trace.RecordStage("upstream", -time.Second)
		trace.FinishSuccess()
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("X-Request-ID", "untrusted-value")
	handler.ServeHTTP(w, r)
	if !strings.HasPrefix(requestID, "req_") || w.Header().Get("X-Request-ID") != requestID {
		t.Fatalf("correlation mismatch: %q %+v", requestID, w.Header())
	}
	detail, err := store.Detail(requestID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Request.StageDurationsMs) != 2 || detail.Request.StageDurationsMs["quota_check"] != 5 {
		t.Fatalf("unexpected stages: %+v", detail.Request.StageDurationsMs)
	}
	if RequestIDFromContext(context.Background()) != "" {
		t.Fatal("untraced request has an ID")
	}
	clean := sanitizeRequestLog(RequestLog{StageDurationsMs: map[string]int64{"secret": 10, "upstream": -1, "tool_parse": 1}})
	if len(clean.StageDurationsMs) != 1 || clean.StageDurationsMs["tool_parse"] != 1 {
		t.Fatalf("unsanitized stages: %+v", clean.StageDurationsMs)
	}
}

func TestFileLogRotationBoundsSizeAndBackups(t *testing.T) {
	w := newDebugFilterWriter()
	w.maxBytes = 16
	w.backups = 2
	path := filepath.Join(t.TempDir(), "server.log")
	if err := w.SetOutputPath(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.SetOutputPath("") })
	for i := 0; i < 8; i++ {
		if _, err := w.Write([]byte("normal-log-line\n")); err != nil {
			t.Fatal(err)
		}
	}
	for _, suffix := range []string{"", ".1", ".2"} {
		info, err := os.Stat(path + suffix)
		if err != nil || info.Size() > 16 {
			t.Fatalf("invalid log %s: %v %v", suffix, info, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe permissions: %v", info.Mode())
		}
		contents, err := os.ReadFile(path + suffix)
		if err != nil || string(contents) != "normal-log-line\n" {
			t.Fatalf("normal lines split across rotations: %q %v", contents, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatal("extra backup retained")
	}
	if _, err := w.Write([]byte(strings.Repeat("x", 30))); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 16 {
		t.Fatalf("oversized write not bounded: %v %v", info, err)
	}
}
