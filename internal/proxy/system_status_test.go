package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHandleSystemStatusReportsRuntimeState(t *testing.T) {
	auth := NewDashboardAuth("", "api-key")
	sessionRecorder := httptest.NewRecorder()
	auth.CreateSession(sessionRecorder)
	cookies := sessionRecorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a dashboard session cookie")
	}

	request := httptest.NewRequest(http.MethodGet, "/admin/system", nil)
	request.AddCookie(cookies[0])
	recorder := httptest.NewRecorder()
	HandleSystemStatus(NewAccountPool(), nil, auth)(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var status systemStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if status.Status != "ok" {
		t.Errorf("status = %q, want ok", status.Status)
	}
	if status.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", status.PID, os.Getpid())
	}
	if status.UptimeSeconds < 0 {
		t.Errorf("uptime = %d, want >= 0", status.UptimeSeconds)
	}
	if status.MemoryBytes == 0 {
		t.Error("expected non-zero memory usage")
	}
	if status.Pool.Total != 0 || status.Pool.Available != 0 {
		t.Errorf("empty pool = %+v, want all zeroes", status.Pool)
	}
	if status.Storage.Status != "unavailable" {
		t.Errorf("storage = %q, want unavailable for a nil store", status.Storage.Status)
	}
}
