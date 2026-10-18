package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpgradeLeafErrorsAreNotSuccess(t *testing.T) {
	originalConfig := AppConfig
	AppConfig = &Config{}
	t.Cleanup(func() { AppConfig = originalConfig })
	original := inferenceCaller
	t.Cleanup(func() { inferenceCaller = original })
	want := errors.New("upstream unavailable")
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, _ StreamCallback, _ ...CallOptions) error {
		return want
	}
	w := httptest.NewRecorder()
	err := handleAnthropicNonStream(w, &Account{}, nil, "model", "id", false, false, false, nil, true, nil, nil, nil)
	if !errors.Is(err, want) {
		t.Fatalf("failed inference returned success: %v", err)
	}
}

func TestUpgradePartialStreamNeverCompletesAfterError(t *testing.T) {
	original := inferenceCaller
	t.Cleanup(func() { inferenceCaller = original })
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		cb("partial answer", false, nil)
		return ErrQuotaExhausted
	}
	w := httptest.NewRecorder()
	err := streamAnthropicTextResponse(w, &Account{}, nil, "model", "id", false, false, nil, CallOptions{})
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("lost stream failure: %v", err)
	}
	if strings.Contains(w.Body.String(), "message_stop") || strings.Contains(w.Body.String(), "end_turn") {
		t.Fatal("partial failed stream was finalized successfully")
	}
}

func TestUpgradeBridgeWriterCancellationUnblocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := newAnthropicStreamBridgeWriter(ctx)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := w.Write([]byte("event: ping\ndata: {}\n\n")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge producer blocked after cancellation")
	}
	w.Close()
}

func TestUpgradeDashboardDoesNotExposeAPIKey(t *testing.T) {
	key := "test-secret-do-not-inject"
	auth := NewDashboardAuth("", key)
	w := httptest.NewRecorder()
	HandleDashboard(key, auth).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if strings.Contains(w.Body.String(), key) {
		t.Fatal("dashboard HTML exposes API key")
	}
}

func TestUpgradeBusinessWithoutPremiumIsNotLifetimeFree(t *testing.T) {
	acc := &Account{PlanType: "business", QuotaInfo: &QuotaInfo{IsEligible: false}}
	if isFreePlan(acc) {
		t.Fatal("Business without Premium treated as lifetime Free")
	}
}

func TestUpgradeSettingsFailureDoesNotApplyHalfUpdate(t *testing.T) {
	original := AppConfig
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig = original })
	before := AppConfig.WebSearchEnabled()
	handler := HandleAdminSettings(filepath.Join(t.TempDir(), "missing-config.yaml"), NewDashboardAuth("", ""))
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodPut, "/admin/settings", strings.NewReader(`{"enable_web_search":false,"notion_proxy":"https://proxy.example.test"}`)))
	if w.Code != http.StatusInternalServerError || AppConfig.WebSearchEnabled() != before {
		t.Fatal("failed persistence changed runtime settings or claimed success")
	}
	w = httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodPut, "/admin/settings", strings.NewReader(`{"enable_web_search":false,"notion_proxy":"invalid://x"}`)))
	if w.Code != 400 || AppConfig.WebSearchEnabled() != before {
		t.Fatal("invalid proxy partially applied settings")
	}
}

func TestUpgradeAmbiguousDeletePreservesBothWorkspaces(t *testing.T) {
	dir := t.TempDir()
	pool := routingPool(2)
	for _, acc := range pool.AccountsSnapshot() {
		if _, err := SaveAccountToFile(acc, dir); err != nil {
			t.Fatal(err)
		}
	}
	err := deleteAccountByEmail(pool, dir, "shared@example.com")
	var ambiguous *AmbiguousEmailError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("expected ambiguity, got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 || pool.Count() != 2 {
		t.Fatal("ambiguous email deleted workspace data")
	}
}

func TestUpgradeUsageUnknownIsNotKnownZero(t *testing.T) {
	for _, tc := range []struct {
		line  string
		known bool
	}{
		{`{"type":"agent-inference","id":"one","finishedAt":1,"value":[]}`, false},
		{`{"type":"agent-inference","id":"one","finishedAt":1,"inputTokens":0,"outputTokens":0,"value":[]}`, true},
	} {
		var usage *UsageInfo
		err := parseNDJSONStream(strings.NewReader(tc.line+"\n"), "id", func(_ string, _ bool, u *UsageInfo) {
			if u != nil {
				s := *u
				usage = &s
			}
		}, nil, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if usage == nil || usage.InputKnown != tc.known || usage.OutputKnown != tc.known {
			t.Fatalf("usage known flags: %#v", usage)
		}
	}
}

func TestUpgradeNoPasswordUsesSameOriginManagementSession(t *testing.T) {
	auth := NewDashboardAuth("", "test-only-key")
	w := httptest.NewRecorder()
	auth.HandleAuthCheck()(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/dashboard/auth/check", nil))
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("no-password page cannot establish management session")
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/admin/connection", nil)
	request.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	auth.HandleConnection()(w, request)
	if w.Code != 200 {
		t.Fatal("same-origin session cannot access protected management API")
	}
	for _, crossSite := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/dashboard/auth/check", nil)
		if crossSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		} else {
			r.Header.Set("Origin", "https://untrusted.example")
		}
		w = httptest.NewRecorder()
		auth.HandleAuthCheck()(w, r)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("cross-origin request received management session")
		}
	}
}

func TestUpgradeUnsafeReplayIsNeverAllowedByHTTPStatus(t *testing.T) {
	for _, status := range []int{401, 404, 429, 500, 502} {
		err := &notionHTTPError{Status: status}
		if canReplayInference(err, false, true, true) || canReplayInference(err, true, false, true) {
			t.Fatalf("HTTP%d bypassed side-effect/content boundary", status)
		}
	}
	if !canReplayInference(&notionHTTPError{Status: 502}, false, false, true) {
		t.Fatal("safe read-only 502 cannot fail over")
	}
	if canReplayInference(&notionHTTPError{Status: 400}, false, false, true) {
		t.Fatal("invalid request iterates through pool")
	}
}
