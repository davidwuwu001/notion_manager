package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// processStartedAt is captured when the proxy package is initialised,
// which is close enough to process start for an uptime display.
var processStartedAt = time.Now()

type systemStatus struct {
	Status        string            `json:"status"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	PID           int               `json:"pid"`
	GoVersion     string            `json:"go_version"`
	MemoryBytes   uint64            `json:"memory_bytes"`
	ListenAddr    string            `json:"listen_addr"`
	Pool          systemPoolStatus  `json:"pool"`
	Storage       systemStorage     `json:"storage"`
	LaunchAgent   launchAgentStatus `json:"launch_agent"`
}

type systemPoolStatus struct {
	Total              int   `json:"total"`
	Available          int   `json:"available"`
	Paused             int   `json:"paused"`
	Exhausted          int   `json:"exhausted"`
	Cooldown           int   `json:"cooldown"`
	Invalid            int   `json:"invalid"`
	Busy               int   `json:"busy"`
	LastQuotaCheckedMs int64 `json:"last_quota_checked_ms"`
}

type systemStorage struct {
	Status        string `json:"status"`
	DataComplete  bool   `json:"data_complete"`
	RetentionDays int    `json:"retention_days"`
	CapturedSince int64  `json:"captured_since_ms"`
}

type launchAgentStatus struct {
	Managed  bool   `json:"managed"`
	Label    string `json:"label,omitempty"`
	State    string `json:"state,omitempty"`
	LastExit *int   `json:"last_exit_code,omitempty"`
}

// HandleSystemStatus reports the proxy's own runtime state, the account
// pool, request-log storage health and whether launchd supervises the
// process. It is read-only and requires the same dashboard session as the
// other /admin endpoints.
func HandleSystemStatus(pool *AccountPool, store *RequestLogStore, auth *DashboardAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requestLogAuthorized(w, r, auth) {
			return
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)

		snapshot := pool.RoutingSnapshot()
		poolStatus := systemPoolStatus{
			Total:     snapshot.Total,
			Available: snapshot.Available,
			Paused:    snapshot.Paused,
			Exhausted: snapshot.Exhausted,
			Cooldown:  snapshot.Cooldown,
			Invalid:   snapshot.Invalid,
			Busy:      snapshot.Busy,
		}
		for _, account := range snapshot.Accounts {
			if account.QuotaCheckedAtMs > poolStatus.LastQuotaCheckedMs {
				poolStatus.LastQuotaCheckedMs = account.QuotaCheckedAtMs
			}
		}

		coverage := store.Coverage()
		status := systemStatus{
			Status:        "ok",
			UptimeSeconds: int64(time.Since(processStartedAt).Seconds()),
			PID:           os.Getpid(),
			GoVersion:     runtime.Version(),
			MemoryBytes:   mem.Sys,
			ListenAddr:    r.Host,
			Pool:          poolStatus,
			Storage: systemStorage{
				Status:        coverage.StorageStatus,
				DataComplete:  coverage.DataComplete,
				RetentionDays: coverage.RetentionDays,
				CapturedSince: coverage.CapturedSince,
			},
			LaunchAgent: detectLaunchAgent(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(status)
	}
}

var (
	plistLabelPattern = regexp.MustCompile(`<key>Label</key>\s*<string>([^<]+)</string>`)
	launchctlState    = regexp.MustCompile(`(?m)^\s*state = (.+)$`)
	launchctlExitCode = regexp.MustCompile(`(?m)^\s*last exit code = (-?\d+)`)
)

// detectLaunchAgent reports whether launchd owns this process. A LaunchAgent
// process is re-parented to launchd (PID 1), so that check is the cheap
// signal; the plist lookup and `launchctl print` only add the label, state
// and last exit code when the plist can be matched to this executable.
func detectLaunchAgent() launchAgentStatus {
	status := launchAgentStatus{Managed: os.Getppid() == 1}
	exe, err := os.Executable()
	if err != nil {
		return status
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return status
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return status
	}
	entries, err := os.ReadDir(filepath.Join(home, "Library", "LaunchAgents"))
	if err != nil {
		return status
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".plist") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", entry.Name()))
		if err != nil || !bytes.Contains(data, []byte(exe)) {
			continue
		}
		match := plistLabelPattern.FindSubmatch(data)
		if len(match) < 2 {
			continue
		}
		status.Label = string(match[1])
		break
	}
	if status.Label == "" {
		return status
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), status.Label)).Output()
	if err != nil {
		return status
	}
	if match := launchctlState.FindStringSubmatch(string(out)); len(match) == 2 {
		status.State = strings.TrimSpace(match[1])
	}
	if match := launchctlExitCode.FindStringSubmatch(string(out)); len(match) == 2 {
		if code, err := strconv.Atoi(match[1]); err == nil {
			status.LastExit = &code
		}
	}
	return status
}
