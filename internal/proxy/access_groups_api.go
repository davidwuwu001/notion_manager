package proxy

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

func safeAccessWrite(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	host, err := url.Parse("http://" + r.Host)
	if err != nil || host.User != nil || host.Path != "" {
		return false
	}
	switch strings.ToLower(host.Hostname()) {
	case "127.0.0.1", "localhost", "::1":
	default:
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// HandleAdminAccess never returns stored key hashes or credentials; new keys are returned once.
func HandleAdminAccess(store *AccessStore, pool *AccountPool, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !auth.ValidateSession(r) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			groups, keys := store.Snapshot()
			json.NewEncoder(w).Encode(map[string]any{"groups": groups, "keys": keys})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if !safeAccessWrite(r) {
			http.Error(w, `{"error":"invalid_origin_or_content_type"}`, http.StatusForbidden)
			return
		}
		var input struct {
			Action     string   `json:"action"`
			Name       string   `json:"name"`
			Group      string   `json:"group"`
			ID         string   `json:"id"`
			AccountIDs []string `json:"account_ids"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		var err error
		var key CreatedAccessKey
		switch input.Action {
		case "set_group":
			for _, id := range input.AccountIDs {
				if pool.FindByAccountID(id) == nil {
					http.Error(w, `{"error":"unknown_account"}`, http.StatusBadRequest)
					return
				}
			}
			err = store.SetGroup(input.Name, input.AccountIDs)
		case "delete_group":
			err = store.DeleteGroup(input.Name)
		case "create_key":
			key, err = store.CreateKey(input.Name, input.Group)
		case "update_key":
			err = store.UpdateKey(input.ID, input.Name, input.Group)
		case "delete_key":
			err = store.DeleteKey(input.ID)
		default:
			http.Error(w, `{"error":"invalid_action"}`, http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, `{"error":"operation_failed"}`, http.StatusBadRequest)
			return
		}
		if input.Action == "create_key" {
			json.NewEncoder(w).Encode(key)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}
