package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"notion-manager/internal/msalogin"
)

// HandleAdminRegister returns the /admin/register HTTP handler. It accepts:
//
//	POST /admin/register
//	Content-Type: application/json
//	{ "input": "<bulk credentials text>" }
//
// or
//
//	POST /admin/register
//	Content-Type: text/plain
//	<bulk credentials text>
//
// Each non-blank line must follow the format
//
//	<email>----<password>----<client_id>----<refresh_token>
//
// The handler walks the list, drives the Microsoft SSO + Notion onboarding
// flow per account using the N → N+1 backup strategy for MS proofs, writes
// one Account JSON file per success into accountsDir, and reloads pool from
// disk so the new accounts become live without a restart.
//
// The response is a JSON summary of per-line outcomes, suitable for direct
// rendering in a future dashboard UI.
func HandleAdminRegister(pool *AccountPool, accountsDir string, auth *DashboardAuth, businessOnly ...bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if auth.HasAdminPassword() && !auth.ValidateSession(r) {
			http.Error(w, `{"error":"unauthorized, dashboard login required"}`, http.StatusUnauthorized)
			return
		}
		if len(businessOnly) > 0 && businessOnly[0] {
			rejectFreeRegistration(w)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		raw, err := readRegisterBody(r)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"read body: %s"}`, err), http.StatusBadRequest)
			return
		}
		tokens, err := msalogin.ParseTokens(raw)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse: %s"}`, err), http.StatusBadRequest)
			return
		}
		if len(tokens) == 0 {
			http.Error(w, `{"error":"no credentials parsed"}`, http.StatusBadRequest)
			return
		}

		if err := os.MkdirAll(accountsDir, 0o755); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"mkdir: %s"}`, err), http.StatusInternalServerError)
			return
		}

		results := runRegister(tokens, accountsDir)

		// Reload accounts directory so successful registrations show up in
		// the dashboard without a restart. We use a fresh AccountPool
		// LoadFromDir; any accounts already loaded keep their pointer.
		pool.ReloadFromDir(accountsDir)

		ok := 0
		for _, r := range results {
			if r.Status == "ok" {
				ok++
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total":   len(results),
			"ok":      ok,
			"failed":  len(results) - ok,
			"results": results,
		})
	}
}

// readRegisterBody pulls bulk credentials from either a JSON {"input":"..."}
func rejectFreeRegistration(w http.ResponseWriter) {
	http.Error(w, `{"error":"Only verified Business accounts can be imported; automatic registration and retries are disabled."}`, http.StatusConflict)
}

// envelope or a raw text/plain body.
func readRegisterBody(r *http.Request) (string, error) {
	defer r.Body.Close()
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", err
		}
		return body.Input, nil
	}
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, err := r.Body.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			break
		}
		if len(buf) > 16*1024*1024 {
			return "", fmt.Errorf("body too large")
		}
	}
	return string(buf), nil
}

// RegisterResult is the per-line outcome reported back to the caller.
type RegisterResult struct {
	Email   string `json:"email"`
	Status  string `json:"status"` // "ok" | "fail"
	Message string `json:"message,omitempty"`
	File    string `json:"file,omitempty"`
	SpaceID string `json:"space_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
}

// runRegister provisions accounts sequentially. Sequential execution keeps
// MS / Notion happy and avoids inter-account interference (cookies are
// per-Client but we still respect Microsoft's per-IP rate limits).
func runRegister(tokens []msalogin.Token, accountsDir string) []RegisterResult {
	out := make([]RegisterResult, len(tokens))
	backups := msalogin.PairBackups(tokens)

	var mu sync.Mutex
	for i, tok := range tokens {
		log.Printf("[register %d/%d] %s", i+1, len(tokens), tok)
		out[i] = RegisterResult{Email: tok.Email, Status: "fail"}

		c, err := msalogin.New(tok, msalogin.Options{
			Backup:  backups[i],
			Timeout: 30 * time.Second,
			// Legacy /admin/register doesn't expose a per-request proxy
			// field; fall back to the dashboard-configured global proxy
			// so registrations match runtime egress policy. Validation
			// already happened at startup / settings PUT.
			ProxyURL: AppConfig.NotionProxyURL(),
		})
		if err != nil {
			out[i].Message = err.Error()
			continue
		}
		session, err := c.Login()
		if err != nil {
			log.Printf("[register %d/%d] FAIL %s: %v", i+1, len(tokens), tok.Email, err)
			out[i].Message = err.Error()
			continue
		}
		if session == nil || session.SpaceID == "" || session.UserID == "" || session.TokenV2 == "" {
			// Defense in depth: msalogin.Login already errors on an
			// unbound workspace, but reject any half-session here too
			// so the legacy /admin/register endpoint never persists a
			// dashboard-wrecking zombie account.
			log.Printf("[register %d/%d] FAIL %s: incomplete session", i+1, len(tokens), tok.Email)
			out[i].Message = "incomplete session: missing space_id/user_id/token_v2"
			continue
		}

		mu.Lock()
		path := filepath.Join(accountsDir, registerAccountFilename(tok.Email))
		mu.Unlock()
		if err := writeRegisterAccount(path, session); err != nil {
			out[i].Message = "write: " + err.Error()
			continue
		}
		log.Printf("[register %d/%d] OK %s → %s", i+1, len(tokens), tok.Email, path)
		out[i] = RegisterResult{
			Email:   tok.Email,
			Status:  "ok",
			File:    filepath.Base(path),
			SpaceID: session.SpaceID,
			UserID:  session.UserID,
		}
	}
	return out
}

var registerSafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func registerAccountFilename(email string) string {
	clean := registerSafeChars.ReplaceAllString(strings.ToLower(email), "_")
	clean = strings.Trim(clean, "_")
	if clean == "" {
		clean = "account"
	}
	return clean + ".json"
}

func writeRegisterAccount(path string, s *msalogin.NotionSession) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
