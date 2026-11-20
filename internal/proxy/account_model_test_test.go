package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func modelProbeRequest(auth *DashboardAuth, body string) *http.Request {
	w := httptest.NewRecorder()
	auth.CreateSession(w)
	r := httptest.NewRequest("POST", "/admin/accounts/test", strings.NewReader(body))
	r.AddCookie(w.Result().Cookies()[0])
	return r
}

func TestAccountModelTestPinsAccountAndRedactsErrors(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserID: "user", SpaceID: "space", Models: []ModelEntry{{ID: "model-a", Name: "Model A"}}}
	pool.AddAccount(acc)
	called := 0
	auth := NewDashboardAuth("", "")
	h := accountModelTestHandler(pool, auth, func(ctx context.Context, got *Account, model string) (bool, error) {
		called++
		if got != acc || model != "model-a" {
			t.Fatal("wrong target")
		}
		return true, nil
	})
	req := modelProbeRequest(auth, `{"account_id":"`+acc.AccountID+`","model":"model-a"}`)
	w := httptest.NewRecorder()
	h(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusOK || body["ok"] != true || called != 1 {
		t.Fatalf("unexpected response %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h(w, modelProbeRequest(auth, `{"account_id":"`+acc.AccountID+`","model":"other"}`))
	if w.Code != http.StatusBadRequest || called != 1 {
		t.Fatalf("unknown model accepted: %d", w.Code)
	}
}

func TestAccountModelTestFailureNeverExposesCredentials(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserID: "u", SpaceID: "s", Models: []ModelEntry{{ID: "m"}}}
	pool.AddAccount(acc)
	auth := NewDashboardAuth("", "")
	h := accountModelTestHandler(pool, auth, func(context.Context, *Account, string) (bool, error) { return false, errors.New("token-secret") })
	w := httptest.NewRecorder()
	h(w, modelProbeRequest(auth, `{"account_id":"`+acc.AccountID+`","model":"m"}`))
	if strings.Contains(w.Body.String(), "token-secret") || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("unsafe failure: %s", w.Body.String())
	}
}

func TestAccountModelTestRequiresSession(t *testing.T) {
	pool := NewAccountPool()
	called := false
	h := accountModelTestHandler(pool, NewDashboardAuth("$sha256$salt$hash", ""), func(context.Context, *Account, string) (bool, error) { called = true; return true, nil })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/admin/accounts/test", strings.NewReader(`{}`)))
	if w.Code != http.StatusUnauthorized || called {
		t.Fatalf("unauthorized probe: %d", w.Code)
	}
}

func TestAccountModelTestRequiresSessionEvenWithoutPassword(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserID: "u", SpaceID: "s", Models: []ModelEntry{{ID: "m"}}}
	pool.AddAccount(acc)
	h := accountModelTestHandler(pool, NewDashboardAuth("", ""), func(context.Context, *Account, string) (bool, error) {
		t.Fatal("unauthorized test invoked")
		return true, nil
	})
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/admin/accounts/test", strings.NewReader(`{"account_id":"`+acc.AccountID+`","model":"m"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", w.Code)
	}
}

func TestAccountModelTestReportsSafeFailureClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{ErrQuotaExhausted, "quota_exhausted"},
		{&notionHTTPError{Status: 401}, "authentication_failed"},
		{&notionHTTPError{Status: 403}, "permission_denied"},
		{&notionHTTPError{Status: 404}, "model_unavailable"},
		{&notionHTTPError{Status: 429}, "rate_limited"},
		{ErrEmptyResponse, "empty_response"},
		{errors.New("private token"), "inference_failed"},
	} {
		if got := modelProbeFailure(tc.err); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestAccountModelTestRejectsCrossSiteAndTrailingJSON(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserID: "u", SpaceID: "s", Models: []ModelEntry{{ID: "m"}}}
	pool.AddAccount(acc)
	called := false
	auth := NewDashboardAuth("", "")
	h := accountModelTestHandler(pool, auth, func(context.Context, *Account, string) (bool, error) { called = true; return true, nil })
	for _, request := range []struct{ body, site string }{
		{`{"account_id":"` + acc.AccountID + `","model":"m"} {}`, "same-origin"},
		{`{"account_id":"` + acc.AccountID + `","model":"m"}`, "cross-site"},
	} {
		w := httptest.NewRecorder()
		r := modelProbeRequest(auth, request.body)
		r.Header.Set("Sec-Fetch-Site", request.site)
		h(w, r)
		if w.Code < 400 || called {
			t.Fatalf("request accepted: %d", w.Code)
		}
	}
}

func TestAccountModelTestLimitsConcurrentProbes(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserID: "u", SpaceID: "s", Models: []ModelEntry{{ID: "m"}}}
	pool.AddAccount(acc)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	auth := NewDashboardAuth("", "")
	h := accountModelTestHandler(pool, auth, func(context.Context, *Account, string) (bool, error) { close(started); <-release; return true, nil })
	body := `{"account_id":"` + acc.AccountID + `","model":"m"}`
	go func() {
		defer close(done)
		h(httptest.NewRecorder(), modelProbeRequest(auth, body))
	}()
	<-started
	w := httptest.NewRecorder()
	h(w, modelProbeRequest(auth, body))
	close(release)
	<-done
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d", w.Code)
	}
}

func TestAccountModelTestRejectsResearchModes(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{UserID: "u", SpaceID: "s", Models: []ModelEntry{{ID: "researcher"}, {ID: "fast-researcher"}, {ID: "researcher-ask"}}}
	pool.AddAccount(acc)
	auth := NewDashboardAuth("", "")
	h := accountModelTestHandler(pool, auth, func(context.Context, *Account, string) (bool, error) {
		t.Fatal("research mode must not run as a short test")
		return true, nil
	})
	for _, model := range []string{"researcher", "fast-researcher", "researcher-ask"} {
		w := httptest.NewRecorder()
		h(w, modelProbeRequest(auth, `{"account_id":"`+acc.AccountID+`","model":"`+model+`"}`))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status %d", model, w.Code)
		}
	}
}
