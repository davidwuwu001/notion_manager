package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutingConfigurationPersistsAndRejectsFailedSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".routing.json")
	pool := routingPool(2)
	if err := pool.ConfigureRouting(path); err != nil {
		t.Fatal(err)
	}
	config := RoutingConfig{Mode: "round_robin", MaxAttempts: 2, MaxConcurrentPerAccount: 1, DisabledAccountIDs: []string{pool.AccountsSnapshot()[1].AccountID}, RetentionDays: 14}
	if err := pool.UpdateRouting(config); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private config: %v %v", info, err)
	}
	reloaded := routingPool(2)
	if err := reloaded.ConfigureRouting(path); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.RoutingSnapshot(); got.Mode != "round_robin" || got.Available != 1 || got.RetentionDays != 14 {
		t.Fatalf("wrong restored config: %+v", got)
	}
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(dir+"-moved", dir) })
	if err := os.WriteFile(dir, []byte("blocks parent directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(dir) })
	config.Mode = "quota"
	if err := pool.UpdateRouting(config); err == nil {
		t.Fatal("failed save reported success")
	}
	if pool.RoutingSnapshot().Mode != "round_robin" {
		t.Fatal("failed save changed live routing")
	}
}

func TestAdminRoutingRequiresSessionAndValidates(t *testing.T) {
	pool := routingPool(1)
	auth := NewDashboardAuth(HashAdminPassword("fixture-admin"), "fixture-api-key")
	handler := HandleAdminRouting(pool, auth)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/routing", nil))
	if rec.Code != 401 {
		t.Fatalf("unauthenticated status=%d", rec.Code)
	}
	cookieRec := httptest.NewRecorder()
	auth.CreateSession(cookieRec)
	for _, body := range []string{
		`{"mode":"primary"}`, `{"max_attempts":-1}`, `{"disabled_account_ids":["email@example.com"]}`,
		`{"mode":"quota","secret":"must reject"}`, `{"mode":"quota"} {}`,
	} {
		req := httptest.NewRequest("PUT", "/admin/routing", strings.NewReader(body))
		req.AddCookie(cookieRec.Result().Cookies()[0])
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 400 {
			t.Fatalf("body %s status=%d", body, rec.Code)
		}
	}
	req := httptest.NewRequest("PUT", "/admin/routing", strings.NewReader(`{"mode":"round_robin","retention_days":7}`))
	req.AddCookie(cookieRec.Result().Cookies()[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var got RoutingStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || got.Mode != "round_robin" || got.Total != 1 {
		t.Fatalf("response %d %s %v", rec.Code, rec.Body.String(), err)
	}
}
