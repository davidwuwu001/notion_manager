package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccessGroupKeyPersistenceAndRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	store, err := NewAccessStore(path)
	if err != nil {
		t.Fatal(err)
	}
	id := ComputeAccountID("user", "space")
	if err = store.SetGroup("work", []string{id}); err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateKey("test", "work")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key.Secret, "sk-nm-") {
		t.Fatal("missing secret")
	}
	scope, ok := store.Authorize(key.Secret)
	if !ok || !scope.Allowed[id] {
		t.Fatal("scope mismatch")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), key.Secret) {
		t.Fatal("secret stored in plaintext")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file permissions: %o", info.Mode().Perm())
	}
	reloaded, err := NewAccessStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok = reloaded.Authorize(key.Secret); !ok {
		t.Fatal("key not restored")
	}
	if err = reloaded.DeleteGroup("work"); err != nil {
		t.Fatal(err)
	}
	if _, ok = reloaded.Authorize(key.Secret); ok {
		t.Fatal("deleted group key still authorized")
	}
}

func TestScopedRequestsAcrossProtocolsAndFailover(t *testing.T) {
	for _, protocol := range upgradeProtocolCases {
		t.Run(protocol.protocol, func(t *testing.T) {
			fixture := newUpgradeProtocolFixture(t, 2)
			accounts := fixture.pool.AccountsSnapshot()
			allowed := accounts[0]
			calls := 0
			inferenceCaller = func(acc *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
				calls++
				if acc != allowed {
					t.Fatalf("attempt crossed account group")
				}
				return ErrQuotaExhausted
			}
			ctx := WithAccessScope(context.Background(), &AccessScope{Allowed: map[string]bool{allowed.AccountID: true}, Identity: "scoped-key"})
			fixture.serve(t, protocol.path, false, []map[string]string{{"role": "user", "content": "hello"}}, ctx)
			if calls != 1 {
				t.Fatalf("unexpected attempts %d", calls)
			}
		})
	}
}

func TestScopedFingerprintSeparatesKeyAndGroup(t *testing.T) {
	fp := "session"
	a := ScopedFingerprint(fp, &AccessScope{Identity: "key:one"})
	if a == ScopedFingerprint(fp, &AccessScope{Identity: "key:two"}) || a == ScopedFingerprint(fp, &AccessScope{Identity: "other:one"}) || ScopedFingerprint(fp, nil) != fp {
		t.Fatal("session scope not isolated")
	}
}

func TestScopedModelCatalogOnlyIncludesAllowedAccounts(t *testing.T) {
	pool := NewAccountPool()
	a := &Account{UserID: "a", SpaceID: "s", Models: []ModelEntry{{ID: "alpha", Name: "Alpha"}}}
	b := &Account{UserID: "b", SpaceID: "s", Models: []ModelEntry{{ID: "bravo", Name: "Bravo"}}}
	pool.AddAccount(a)
	pool.AddAccount(b)
	for _, tc := range []struct {
		name     string
		scope    *AccessScope
		expected []string
	}{
		{"master", nil, []string{"auto", "alpha", "bravo"}},
		{"one", &AccessScope{Allowed: map[string]bool{a.AccountID: true}}, []string{"auto", "alpha"}},
		{"empty", &AccessScope{Allowed: map[string]bool{}}, []string{}},
	} {
		for _, path := range []string{"/v1/models", "/models"} {
			t.Run(tc.name+path, func(t *testing.T) {
				r := httptest.NewRequest("GET", path, nil)
				if tc.scope != nil {
					r = r.WithContext(WithAccessScope(r.Context(), tc.scope))
				}
				w := httptest.NewRecorder()
				HandlePublicModels(pool)(w, r)
				var data publicModelResponse
				if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
					t.Fatal(err)
				}
				found := map[string]bool{}
				for _, model := range data.Data {
					found[model.ID] = true
				}
				if len(found) != len(tc.expected) {
					t.Fatalf("unexpected models %v", found)
				}
				for _, id := range tc.expected {
					if !found[id] {
						t.Fatalf("missing model %s", id)
					}
				}
			})
		}
	}
	r := httptest.NewRequest("GET", "/admin/models", nil).WithContext(WithAccessScope(context.Background(), &AccessScope{Allowed: map[string]bool{a.AccountID: true}}))
	w := httptest.NewRecorder()
	HandleAdminModels(pool, NewDashboardAuth("", ""))(w, r)
	var admin struct {
		Available []ModelEntry `json:"available_models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &admin); err != nil || len(admin.Available) != 3 {
		t.Fatalf("admin catalog unexpectedly scoped: %s %v", w.Body.String(), err)
	}
}

func TestLatencyRoutingIsOptInAndRespectsCapacity(t *testing.T) {
	pool := NewAccountPool()
	a := &Account{UserID: "a", SpaceID: "s"}
	b := &Account{UserID: "b", SpaceID: "s"}
	pool.AddAccount(a)
	pool.AddAccount(b)
	if pool.RoutingSnapshot().Mode != "quota" {
		t.Fatal("changed default mode")
	}
	pool.RecordRoutingLatency(a, 8*time.Second)
	pool.RecordRoutingLatency(b, 2*time.Second)
	if err := pool.UpdateRouting(RoutingConfig{Mode: "latency", MaxConcurrentPerAccount: 1}); err != nil {
		t.Fatal(err)
	}
	got, release, err := pool.SelectForRequest("", false, nil, "")
	if err != nil || got != b {
		t.Fatalf("fastest not selected: %v", err)
	}
	second, release2, err := pool.SelectForRequest("", false, nil, "")
	if err != nil || second != a {
		t.Fatalf("capacity not respected: %v", err)
	}
	release2()
	release()
	pool.RecordRoutingLatency(a, 1000*time.Second)
	pool.routingMu.Lock()
	measured := pool.routingStates[a.AccountID].latencySeconds
	pool.routingMu.Unlock()
	if measured > 120 {
		t.Fatalf("unbounded latency %v", measured)
	}
}

func TestLatencyRoutingDistributesUnsampledConcurrentRequests(t *testing.T) {
	pool := NewAccountPool()
	a := &Account{UserID: "first", SpaceID: "s"}
	b := &Account{UserID: "second", SpaceID: "s"}
	pool.AddAccount(a)
	pool.AddAccount(b)
	if err := pool.UpdateRouting(RoutingConfig{Mode: "latency"}); err != nil {
		t.Fatal(err)
	}
	first, release1, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	// Control the tie-breaker so equal scores would select the busy first account.
	pool.index.Store(0)
	second, release2, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	if first == second {
		t.Fatal("unsampled busy account was not penalized")
	}
}

func TestLatencyRoutingDoesNotOverloadUnknownAgainstKnownLatency(t *testing.T) {
	pool := NewAccountPool()
	unknown := &Account{UserID: "unknown", SpaceID: "s"}
	known := &Account{UserID: "known", SpaceID: "s"}
	pool.AddAccount(unknown)
	pool.AddAccount(known)
	if err := pool.UpdateRouting(RoutingConfig{Mode: "latency"}); err != nil {
		t.Fatal(err)
	}
	pool.RecordRoutingLatency(known, 8*time.Second)
	first, release1, err := pool.SelectForRequest("", false, nil, unknown.AccountID)
	if err != nil || first != unknown {
		t.Fatal("could not establish unknown in-flight request")
	}
	defer release1()
	second, release2, err := pool.SelectForRequest("", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	if second != known {
		t.Fatal("unknown account with an in-flight request outranked an idle 8-second account")
	}
}

func TestAccessAPIRequiresSessionAndSecretShownOnce(t *testing.T) {
	store, _ := NewAccessStore("")
	pool := NewAccountPool()
	a := &Account{UserID: "u", SpaceID: "s"}
	pool.AddAccount(a)
	auth := NewDashboardAuth("$sha256$salt$hash", "master")
	h := HandleAdminAccess(store, pool, auth)
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/admin/access", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated read")
	}
	session := httptest.NewRecorder()
	auth.CreateSession(session)
	cookie := session.Result().Cookies()[0]
	call := func(payload string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/admin/access", strings.NewReader(payload))
		r.Host = "127.0.0.1:8081"
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookie)
		h(w, r)
		return w
	}
	w = call(`{"action":"set_group","name":"work","account_ids":["` + a.AccountID + `"]}`)
	if w.Code != 200 {
		t.Fatalf("group %d", w.Code)
	}
	w = call(`{"action":"create_key","name":"test","group":"work"}`)
	var created CreatedAccessKey
	if json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Secret == "" {
		t.Fatal("key not returned")
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/access", nil)
	r.AddCookie(cookie)
	h(w, r)
	if strings.Contains(w.Body.String(), created.Secret) || strings.Contains(w.Body.String(), "hash") {
		t.Fatal("secret leaked")
	}
}

func TestAccessAPIRejectsCrossSiteAndMalformedWrites(t *testing.T) {
	store, _ := NewAccessStore("")
	pool := NewAccountPool()
	auth := NewDashboardAuth("$sha256$salt$hash", "master")
	h := HandleAdminAccess(store, pool, auth)
	session := httptest.NewRecorder()
	auth.CreateSession(session)
	cookie := session.Result().Cookies()[0]
	for _, tc := range []struct{ host, origin, site, contentType, body string }{
		{"127.0.0.1:8081", "https://evil.test", "cross-site", "application/json", `{"action":"set_group","name":"x"}`},
		{"evil.test:8081", "http://evil.test:8081", "same-origin", "application/json", `{"action":"set_group","name":"x"}`},
		{"127.0.0.1:8081", "http://127.0.0.1:8081", "same-origin", "text/plain", `{"action":"set_group","name":"x"}`},
		{"127.0.0.1:8081", "http://127.0.0.1:8081", "same-origin", "application/json", `{"action":"set_group","name":"x"} {}`},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/admin/access", strings.NewReader(tc.body))
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("Content-Type", tc.contentType)
		r.AddCookie(cookie)
		h(w, r)
		if w.Code == 200 {
			t.Fatalf("accepted %s %s", tc.host, tc.contentType)
		}
	}
	groups, _ := store.Snapshot()
	if len(groups) != 0 {
		t.Fatal("invalid write mutated store")
	}
}

func TestAccessChangesCancelInflightRequests(t *testing.T) {
	store, _ := NewAccessStore("")
	id := ComputeAccountID("u", "s")
	_ = store.SetGroup("work", []string{id})
	key, err := store.CreateKey("test", "work")
	if err != nil {
		t.Fatal(err)
	}
	_, ctx, done, ok := store.AuthorizeRequest(context.Background(), key.Secret)
	if !ok {
		t.Fatal("request unauthorized")
	}
	defer done()
	if err = store.SetGroup("work", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("scope change did not cancel in-flight request")
	}
	scope, ok := store.Authorize(key.Secret)
	if !ok || len(scope.Allowed) != 0 {
		t.Fatal("group change not reflected")
	}
}

func TestAccessFailedSaveDoesNotChangePolicyOrCancel(t *testing.T) {
	store, _ := NewAccessStore("")
	id := ComputeAccountID("u", "s")
	if err := store.SetGroup("work", []string{id}); err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateKey("test", "work")
	if err != nil {
		t.Fatal(err)
	}
	_, ctx, done, ok := store.AuthorizeRequest(context.Background(), key.Secret)
	if !ok {
		t.Fatal("unauthorized")
	}
	defer done()
	store.path = t.TempDir() // rename to a directory fails after temporary file creation
	if err = store.SetGroup("work", nil); err == nil {
		t.Fatal("expected save failure")
	}
	select {
	case <-ctx.Done():
		t.Fatal("failed save revoked in-flight request")
	default:
	}
	scope, ok := store.Authorize(key.Secret)
	if !ok || !scope.Allowed[id] {
		t.Fatal("failed save altered policy")
	}
}

func TestScopedSelectionNeverCrossesGroup(t *testing.T) {
	pool := NewAccountPool()
	a := &Account{UserID: "a", SpaceID: "s", QuotaInfo: &QuotaInfo{IsEligible: true}}
	b := &Account{UserID: "b", SpaceID: "s", QuotaInfo: &QuotaInfo{IsEligible: true}}
	pool.AddAccount(a)
	pool.AddAccount(b)
	allowed := map[string]bool{a.AccountID: true}
	selected, release, err := pool.SelectForRequestScoped("", false, nil, b.AccountID, allowed)
	if err != nil || selected != a {
		t.Fatalf("out of scope selection: %v %v", selected, err)
	}
	release()
	if _, _, err := pool.SelectForRequestScoped("", false, map[string]bool{a.AccountID: true}, "", allowed); err == nil {
		t.Fatal("fallback escaped scope")
	}
}
