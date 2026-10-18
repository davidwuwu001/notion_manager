package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"notion-manager/internal/regjob"
	"notion-manager/internal/regjob/providers"
	"notion-manager/internal/regjob/providers/microsoft"
)

const (
	testCookieName = "dashboard_session"
)

// regHandlerHarness wires together the minimum dependencies needed by the
// admin_register_jobs handlers: a fresh AccountPool, a temp accountsDir,
// a regjob.Store, an authenticated DashboardAuth, and a Registry holding
// a fake Microsoft provider so the runner runs offline.
type regHandlerHarness struct {
	t          *testing.T
	pool       *AccountPool
	accounts   string
	store      regjob.Store
	auth       *DashboardAuth
	registry   *providers.Registry
	cookieAuth *http.Cookie
	deps       *RegisterJobsDeps
}

// fakeMicrosoft is the offline replacement for providers/microsoft.Provider
// used in handler tests. It keeps the same ID ("microsoft") so the wiring
// layer is exercised exactly as in production. Login is stubbed to return
// a deterministic Session.
func fakeMicrosoft() *microsoft.Provider {
	p := microsoft.New()
	p.LoginFn = func(ctx context.Context, c providers.Credential, b *providers.Credential, opts providers.LoginOptions) (*providers.Session, error) {
		return &providers.Session{
			TokenV2:   "tk-" + c.Email,
			UserID:    "u-" + c.Email,
			SpaceID:   "s-" + c.Email,
			UserEmail: c.Email,
		}, nil
	}
	return p
}

func newRegHandlerHarness(t *testing.T) *regHandlerHarness {
	t.Helper()
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "accounts")
	if err := os.MkdirAll(accountsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := regjob.NewStore(filepath.Join(dir, "history.json"), 100)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(store.Close)

	// adminPassword "test" → set hash
	hashed := HashAdminPassword("test")
	auth := NewDashboardAuth(hashed, "test-key")

	// Manually create a session by simulating CreateSession output. We
	// invoke CreateSession against a recorder, capture the cookie, and
	// reuse it on subsequent requests.
	rec := httptest.NewRecorder()
	auth.CreateSession(rec)
	resp := rec.Result()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == testCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatalf("could not capture session cookie")
	}

	registry := providers.NewRegistry()
	registry.Register(fakeMicrosoft())

	// Install benign defaults for the package-level Notion fetchers so
	// the post-register quota refresh goroutine doesn't panic on tests
	// that don't explicitly stub them. Tests that need to assert on the
	// fetched values can replace them via withFetchers.
	prevQ, prevM := quotaFetcher, modelsFetcher
	quotaFetcher = func(*Account) (*QuotaInfo, error) {
		return &QuotaInfo{IsEligible: true}, nil
	}
	modelsFetcher = func(*Account) ([]ModelEntry, error) { return nil, nil }
	t.Cleanup(func() {
		quotaFetcher, modelsFetcher = prevQ, prevM
	})

	pool := NewAccountPool()
	deps := &RegisterJobsDeps{
		Pool:        pool,
		AccountsDir: accountsDir,
		Store:       store,
		Providers:   registry,
		Auth:        auth,
	}

	return &regHandlerHarness{
		t:          t,
		pool:       pool,
		accounts:   accountsDir,
		store:      store,
		auth:       auth,
		registry:   registry,
		cookieAuth: cookie,
		deps:       deps,
	}
}

// authReq attaches the dashboard session cookie used by all handler tests.
func (h *regHandlerHarness) authReq(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.AddCookie(h.cookieAuth)
	return req
}

// waitJobDone busy-waits up to 3s for a job to reach a terminal state. It
// keeps the runner goroutine and TempDir cleanup from racing.
func (h *regHandlerHarness) waitJobDone(jobID string) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := h.store.Get(jobID)
		if ok && (j.State == regjob.JobDone || j.State == regjob.JobCancelled) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("job %s did not reach terminal state", jobID)
}

func TestStartRegisterJobReturnsID(t *testing.T) {
	h := newRegHandlerHarness(t)
	body := `{"input":"a@x----p----c----rt\nb@x----p----c----rt2", "concurrency": 2}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID    string `json:"job_id"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.JobID == "" {
		t.Fatalf("missing job_id: %s", rec.Body.String())
	}
	if resp.Provider != "microsoft" {
		t.Fatalf("provider: %q, want microsoft", resp.Provider)
	}
	if _, ok := h.store.Get(resp.JobID); !ok {
		t.Fatalf("job not in store: %s", resp.JobID)
	}
	h.waitJobDone(resp.JobID)
}

func TestStartRegisterRejectsUnknownProvider(t *testing.T) {
	h := newRegHandlerHarness(t)
	body := `{"provider":"google","input":"a@x----p----c----rt"}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProvidersEndpointReturnsMicrosoftOnly(t *testing.T) {
	h := newRegHandlerHarness(t)
	req := h.authReq("GET", "/admin/register/providers", nil)
	rec := httptest.NewRecorder()
	HandleAdminRegisterProviders(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Providers []providers.Info `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Providers) != 1 || resp.Providers[0].ID != "microsoft" {
		t.Fatalf("providers list: %+v", resp.Providers)
	}
	if !resp.Providers[0].Enabled {
		t.Fatalf("microsoft should be enabled")
	}
}

func TestProvidersEndpointRequiresAuth(t *testing.T) {
	h := newRegHandlerHarness(t)
	req := httptest.NewRequest("GET", "/admin/register/providers", nil)
	rec := httptest.NewRecorder()
	HandleAdminRegisterProviders(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without cookie, got %d", rec.Code)
	}
}

func TestStartRegisterRejectsEmptyBody(t *testing.T) {
	h := newRegHandlerHarness(t)
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(`{"input":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStartRegisterDefaultsConcurrencyToOne(t *testing.T) {
	h := newRegHandlerHarness(t)
	body := `{"input":"a@x----p----c----rt"}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	job, _ := h.store.Get(resp.JobID)
	if job.Concurrency != 1 {
		t.Fatalf("concurrency: %d", job.Concurrency)
	}
	h.waitJobDone(resp.JobID)
}

func TestStartRequiresAuth(t *testing.T) {
	h := newRegHandlerHarness(t)
	req := httptest.NewRequest("POST", "/admin/register/start",
		strings.NewReader(`{"input":"a@x----p----c----rt"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without cookie, got %d", rec.Code)
	}
}

func TestEventsStreamSSE(t *testing.T) {
	h := newRegHandlerHarness(t)

	// Drive a job through the handler so it actually runs to completion.
	body := `{"input":"a@x----p----c----rt", "concurrency": 1}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d", rec.Code)
	}
	var startResp struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	url := srv.URL + "/admin/register/jobs/" + startResp.JobID + "/events"
	creq, _ := http.NewRequest("GET", url, nil)
	creq.AddCookie(h.cookieAuth)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	creq = creq.WithContext(ctx)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type: %s", ct)
	}

	// Consume events until done. Expect at least one snapshot + one done.
	br := bufio.NewReader(resp.Body)
	gotSnapshot := false
	gotDone := false
	deadline := time.Now().Add(3 * time.Second)
	currentEvent := ""
	for time.Now().Before(deadline) && !(gotSnapshot && gotDone) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			if currentEvent == "snapshot" {
				gotSnapshot = true
			} else if currentEvent == "done" {
				gotDone = true
			}
		}
	}
	if !gotSnapshot {
		t.Fatalf("no snapshot event seen")
	}
	if !gotDone {
		t.Fatalf("no done event seen")
	}
	h.waitJobDone(startResp.JobID)
}

func TestListReturnsRecentJobs(t *testing.T) {
	h := newRegHandlerHarness(t)
	for i := 0; i < 3; i++ {
		emails := []string{fmt.Sprintf("u%02d@x", i)}
		j := h.store.Create("microsoft", "", 1, 1, emails)
		h.store.UpdateStep(j.ID, 0, func(st *regjob.Step) { st.Status = regjob.StepOK })
		h.store.Finish(j.ID)
	}
	req := h.authReq("GET", "/admin/register/jobs", nil)
	rec := httptest.NewRecorder()
	HandleAdminRegisterJobsList(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got []regjob.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(got))
	}
}

func TestListLimitParam(t *testing.T) {
	h := newRegHandlerHarness(t)
	for i := 0; i < 5; i++ {
		h.store.Create("microsoft", "", 0, 1, nil)
	}
	req := h.authReq("GET", "/admin/register/jobs?limit=2", nil)
	rec := httptest.NewRecorder()
	HandleAdminRegisterJobsList(h.deps).ServeHTTP(rec, req)
	var got []regjob.Job
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("limit honored: %d", len(got))
	}
}

func TestGetJobReturnsSnapshot(t *testing.T) {
	h := newRegHandlerHarness(t)
	j := h.store.Create("microsoft", "", 1, 1, []string{"a@x"})
	h.store.UpdateStep(j.ID, 0, func(st *regjob.Step) { st.Status = regjob.StepOK })
	h.store.Finish(j.ID)

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("GET", srv.URL+"/admin/register/jobs/"+j.ID, nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got regjob.Job
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != j.ID || got.State != regjob.JobDone {
		t.Fatalf("job mismatch: %+v", got)
	}
}

func TestGetJobNotFoundReturns404(t *testing.T) {
	h := newRegHandlerHarness(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("GET", srv.URL+"/admin/register/jobs/does-not-exist", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDeleteAccountRemovesFile(t *testing.T) {
	h := newRegHandlerHarness(t)

	target := filepath.Join(h.accounts, "alpha_at_example.json")
	if err := os.WriteFile(target, []byte(`{"user_email":"alpha@example.com","token_v2":"tk","user_id":"u","space_id":"s"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/accounts/", HandleAdminDeleteAccount(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("DELETE", srv.URL+"/admin/accounts/alpha@example.com", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("file still exists: err=%v", err)
	}
}

func TestDeleteAccountUnknownEmailReturns404(t *testing.T) {
	h := newRegHandlerHarness(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/accounts/", HandleAdminDeleteAccount(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("DELETE", srv.URL+"/admin/accounts/missing@example.com", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDeleteAccountRejectsEmptyEmail(t *testing.T) {
	h := newRegHandlerHarness(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/accounts/", HandleAdminDeleteAccount(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("DELETE", srv.URL+"/admin/accounts/", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected non-200 for empty email")
	}
}

// TestStartRegisterPersistsProxyOnJob asserts that a body with a proxy URL
// shows up on the job snapshot so the history view can render the badge
// and the retry handler can reuse the upstream.
func TestStartRegisterPersistsProxyOnJob(t *testing.T) {
	h := newRegHandlerHarness(t)
	body := `{"input":"a@x----p----c----rt", "concurrency": 1, "proxy":"socks5://u:p@127.0.0.1:1080"}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"job_id"`
		Proxy string `json:"proxy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Proxy != "socks5://u:p@127.0.0.1:1080" {
		t.Fatalf("proxy in response: %q", resp.Proxy)
	}
	job, ok := h.store.Get(resp.JobID)
	if !ok {
		t.Fatalf("job not found: %s", resp.JobID)
	}
	if job.Proxy != "socks5://u:p@127.0.0.1:1080" {
		t.Fatalf("job.Proxy=%q want socks5://...", job.Proxy)
	}
	h.waitJobDone(resp.JobID)

	// Sidecar should also have been written so retry can reload it.
	sc, ok := h.store.LoadInputs(resp.JobID)
	if !ok {
		t.Fatalf("sidecar not written")
	}
	if sc.Proxy != "socks5://u:p@127.0.0.1:1080" {
		t.Fatalf("sidecar.Proxy=%q", sc.Proxy)
	}
	if len(sc.Credentials) != 1 || sc.Credentials[0].Email != "a@x" {
		t.Fatalf("sidecar credentials: %+v", sc.Credentials)
	}
}

// TestStartRegisterRejectsBadProxy asserts an unsupported scheme returns 400
// before the job is created.
func TestStartRegisterRejectsBadProxy(t *testing.T) {
	h := newRegHandlerHarness(t)
	body := `{"input":"a@x----p----c----rt", "proxy":"ftp://h:1"}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteJobRemovesHistoryAndSidecar drives the DELETE handler through
// the same router that production main.go wires up, then asserts the job
// is gone from the store and the sidecar file no longer exists.
func TestDeleteJobRemovesHistoryAndSidecar(t *testing.T) {
	h := newRegHandlerHarness(t)
	// Drive a job through Start so a sidecar is created.
	body := `{"input":"a@x----p----c----rt", "concurrency":1}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var startResp struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)
	h.waitJobDone(startResp.JobID)

	if _, ok := h.store.LoadInputs(startResp.JobID); !ok {
		t.Fatalf("sidecar should exist before delete")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("DELETE", srv.URL+"/admin/register/jobs/"+startResp.JobID, nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	if _, ok := h.store.Get(startResp.JobID); ok {
		t.Fatalf("job still in store after delete")
	}
	if _, ok := h.store.LoadInputs(startResp.JobID); ok {
		t.Fatalf("sidecar not removed")
	}
}

// TestDeleteJobUnknownReturns404 confirms the handler still 404s on a
// missing id (idempotent at the store level, but the HTTP shape should
// stay 404 so the UI can show "already gone").
func TestDeleteJobUnknownReturns404(t *testing.T) {
	h := newRegHandlerHarness(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("DELETE", srv.URL+"/admin/register/jobs/does-not-exist", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// TestRetryJobReprocessesFailedSteps drives a job to completion with an
// injected failure for one credential, then retries via the API and
// verifies a fresh job is created that reprocesses only the failed row.
func TestRetryJobReprocessesFailedSteps(t *testing.T) {
	h := newRegHandlerHarness(t)
	// Replace the registered provider with one that fails for "b@x" until
	// the second invocation, mirroring a transient upstream error.
	flaky := microsoft.New()
	var bAttempts int
	flaky.LoginFn = func(ctx context.Context, c providers.Credential, b *providers.Credential, opts providers.LoginOptions) (*providers.Session, error) {
		if c.Email == "b@x" {
			bAttempts++
			if bAttempts == 1 {
				return nil, fmt.Errorf("simulated transient failure")
			}
		}
		return &providers.Session{
			TokenV2:   "tk-" + c.Email,
			UserID:    "u-" + c.Email,
			SpaceID:   "s-" + c.Email,
			UserEmail: c.Email,
		}, nil
	}
	registry := providers.NewRegistry()
	registry.Register(flaky)
	h.deps.Providers = registry
	h.registry = registry

	body := `{"input":"a@x----p----c----rt\nb@x----p----c----rt2", "concurrency":1}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var startResp struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)
	h.waitJobDone(startResp.JobID)

	first, _ := h.store.Get(startResp.JobID)
	if first.OK != 1 || first.Fail != 1 {
		t.Fatalf("first job counts: ok=%d fail=%d", first.OK, first.Fail)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("POST", srv.URL+"/admin/register/jobs/"+startResp.JobID+"/retry", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("retry status: %d body=%s", resp.StatusCode, body)
	}
	var retryResp struct {
		JobID string `json:"job_id"`
		Total int    `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&retryResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if retryResp.JobID == "" || retryResp.JobID == startResp.JobID {
		t.Fatalf("retry should return a NEW job id, got %q (orig=%q)", retryResp.JobID, startResp.JobID)
	}
	if retryResp.Total != 1 {
		t.Fatalf("retry total: %d (want 1, only failed row)", retryResp.Total)
	}
	h.waitJobDone(retryResp.JobID)
	retried, _ := h.store.Get(retryResp.JobID)
	if retried.OK != 1 || retried.Fail != 0 {
		t.Fatalf("retried counts: ok=%d fail=%d (want 1/0)", retried.OK, retried.Fail)
	}
	if retried.Steps[0].Email != "b@x" {
		t.Fatalf("retried wrong email: %q", retried.Steps[0].Email)
	}
}

// TestRetryJobNoFailuresReturns400 — there's nothing to retry on a
// fully-successful run, so the API short-circuits.
func TestRetryJobNoFailuresReturns400(t *testing.T) {
	h := newRegHandlerHarness(t)
	body := `{"input":"a@x----p----c----rt", "concurrency":1}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	var startResp struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)
	h.waitJobDone(startResp.JobID)

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("POST", srv.URL+"/admin/register/jobs/"+startResp.JobID+"/retry", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
}

// TestStartRegisterTriggersPostRegisterRefreshAndPersists exercises the
// "auto-refresh quota after register" pipeline end-to-end:
//
//  1. Start handler kicks off a job with a fake provider that returns a
//     deterministic Session.
//  2. Runner writes the account JSON to disk and fires OnSuccess(email).
//  3. OnSuccess reloads the pool, then triggers RefreshAndPersistAccount.
//  4. RefreshAndPersistAccount calls the (stubbed) quotaFetcher /
//     modelsFetcher and writes quota_info + available_models back to the
//     account file atomically.
//
// The test stubs the package-level fetchers so we don't need a live
// Notion server, then polls the account file for quota_info to land
// (the refresh runs in a background goroutine after the runner finishes).
func TestStartRegisterTriggersPostRegisterRefreshAndPersists(t *testing.T) {
	originalBusinessCheck := businessSubscriptionChecker
	businessSubscriptionChecker = func(*Account) error { return nil }
	t.Cleanup(func() { businessSubscriptionChecker = originalBusinessCheck })
	h := newRegHandlerHarness(t)

	var fetcherHits atomic.Int32
	restore := withFetchers(t,
		func(acc *Account) (*QuotaInfo, error) {
			fetcherHits.Add(1)
			return &QuotaInfo{
				IsEligible:     true,
				SpaceUsage:     3,
				SpaceLimit:     100,
				UserUsage:      1,
				UserLimit:      50,
				HasPremium:     true,
				PremiumBalance: 90,
				PremiumUsage:   10,
				PremiumLimit:   100,
			}, nil
		},
		func(acc *Account) ([]ModelEntry, error) {
			return []ModelEntry{{ID: "gpt-5", Name: "GPT-5"}}, nil
		},
	)
	defer restore()

	body := `{"input":"a@x----p----c----rt", "concurrency":1}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var startResp struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	h.waitJobDone(startResp.JobID)

	// Poll for quota_info to appear in the per-account file. The refresh
	// hook runs in a background goroutine after OnSuccess, so we give it
	// up to 3 seconds to settle.
	deadline := time.Now().Add(3 * time.Second)
	var got map[string]interface{}
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(h.accounts)
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(h.accounts, e.Name()))
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				continue
			}
			if email, _ := raw["user_email"].(string); email == "a@x" {
				if _, ok := raw["quota_info"]; ok {
					got = raw
					break
				}
			}
		}
		if got != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got == nil {
		t.Fatalf("quota_info never landed on disk after registration (fetcher hits: %d)", fetcherHits.Load())
	}
	if fetcherHits.Load() == 0 {
		t.Fatalf("quotaFetcher was not invoked")
	}

	q, ok := got["quota_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("quota_info not a map: %v", got["quota_info"])
	}
	if v, _ := q["is_eligible"].(bool); !v {
		t.Fatalf("is_eligible: %v", q["is_eligible"])
	}
	if v, _ := q["space_usage"].(float64); int(v) != 3 {
		t.Fatalf("space_usage: %v", q["space_usage"])
	}
	if v, _ := q["premium_balance"].(float64); int(v) != 90 {
		t.Fatalf("premium_balance: %v", q["premium_balance"])
	}
	if _, ok := got["quota_checked_at"]; !ok {
		t.Fatalf("quota_checked_at should be set")
	}
	models, ok := got["available_models"].([]interface{})
	if !ok || len(models) != 1 {
		t.Fatalf("available_models on disk: %v", got["available_models"])
	}
}

// TestPostRegisterHookOverrideReceivesEmail asserts the package-level
// postRegisterRefreshHook seam is honoured by Start so callers (and
// tests) can intercept the per-account refresh deterministically.
func TestPostRegisterHookOverrideReceivesEmail(t *testing.T) {
	h := newRegHandlerHarness(t)

	prevHook := postRegisterRefreshHook
	defer func() { postRegisterRefreshHook = prevHook }()

	var (
		mu     sync.Mutex
		emails []string
	)
	postRegisterRefreshHook = func(deps *RegisterJobsDeps, email string) {
		mu.Lock()
		defer mu.Unlock()
		emails = append(emails, email)
	}

	body := `{"input":"a@x----p----c----rt\nb@x----p----c----rt2", "concurrency":2}`
	req := h.authReq("POST", "/admin/register/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleAdminRegisterStart(h.deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var startResp struct {
		JobID string `json:"job_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)
	h.waitJobDone(startResp.JobID)

	mu.Lock()
	defer mu.Unlock()
	if len(emails) != 2 {
		t.Fatalf("hook should fire once per success, got: %v", emails)
	}
	want := map[string]bool{"a@x": false, "b@x": false}
	for _, e := range emails {
		if _, ok := want[e]; !ok {
			t.Fatalf("unexpected email: %s", e)
		}
		want[e] = true
	}
	for e, seen := range want {
		if !seen {
			t.Fatalf("hook never fired for %s", e)
		}
	}
}

// TestRetryJobWithoutSidecarReturns410 — older jobs created before the
// sidecar feature have no recoverable input, so the API tells the client
// the resource is "Gone" rather than producing a phantom retry job.
func TestRetryJobWithoutSidecarReturns410(t *testing.T) {
	h := newRegHandlerHarness(t)
	// Create a job directly via the store, bypassing the sidecar-saving
	// Start handler. Mark a step as failed so it's a candidate for retry.
	j := h.store.Create("microsoft", "", 1, 1, []string{"a@x"})
	h.store.UpdateStep(j.ID, 0, func(st *regjob.Step) {
		st.Status = regjob.StepFail
		st.Message = "old job"
	})
	h.store.Finish(j.ID)

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/register/jobs/", HandleAdminRegisterJobsRouter(h.deps))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	creq, _ := http.NewRequest("POST", srv.URL+"/admin/register/jobs/"+j.ID+"/retry", nil)
	creq.AddCookie(h.cookieAuth)
	resp, err := srv.Client().Do(creq)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("status: %d (want 410)", resp.StatusCode)
	}
}
