package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ConfigureRouting reads only the separate routing file; account credentials and config.yaml are untouched.
func (p *AccountPool) ConfigureRouting(path string) error {
	config := RoutingConfig{}
	if path != "" {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect routing config: %w", err)
		}
		if err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("routing config must be a regular file")
			}
			if err := os.Chmod(path, accountFileMode); err != nil {
				return fmt.Errorf("secure routing config: %w", err)
			}
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open routing config: %w", err)
			}
			decodeErr := decodeRouting(io.LimitReader(file, 1<<20), &config)
			closeErr := file.Close()
			if decodeErr != nil {
				return fmt.Errorf("read routing config: %w", decodeErr)
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	config, err := normalizedRoutingConfig(config)
	if err != nil {
		return err
	}
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	p.routingPath = path
	p.routingConfig = config
	return nil
}

func decodeRouting(reader io.Reader, target *RoutingConfig) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var decoded *RoutingConfig
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if decoded == nil {
		return fmt.Errorf("expected a routing object")
	}
	*target = *decoded
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("expected exactly one routing object")
	}
	return nil
}

func saveRoutingConfig(path string, config RoutingConfig) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateAccountsDir(dir); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".routing-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err = file.Chmod(accountFileMode); err == nil {
		err = json.NewEncoder(file).Encode(config)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func HandleAdminRouting(pool *AccountPool, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if auth.HasAdminPassword() && !auth.ValidateSession(r) {
			http.Error(w, `{"error":"unauthorized, dashboard login required"}`, http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
		case http.MethodPut:
			var config RoutingConfig
			if err := decodeRouting(http.MaxBytesReader(w, r.Body, 1<<20), &config); err != nil {
				http.Error(w, `{"error":"invalid routing configuration"}`, http.StatusBadRequest)
				return
			}
			if _, err := normalizedRoutingConfig(config); err != nil {
				http.Error(w, `{"error":"invalid routing mode, limits, or account IDs"}`, http.StatusBadRequest)
				return
			}
			if err := pool.UpdateRouting(config); err != nil {
				http.Error(w, `{"error":"routing configuration could not be saved"}`, http.StatusInternalServerError)
				return
			}
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(pool.RoutingSnapshot())
	}
}
