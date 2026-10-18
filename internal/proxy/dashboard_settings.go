package proxy

import (
	"sync"
	"sync/atomic"
)

type dashboardConfigOverride struct{ original, config *Config }

var dashboardRuntimeConfig atomic.Pointer[dashboardConfigOverride]
var dashboardSettingsWriteMu sync.Mutex

// Publish settings only after persistence; in-flight readers keep an immutable snapshot.
func effectiveDashboardConfig(c *Config) *Config {
	if override := dashboardRuntimeConfig.Load(); override != nil && override.original == c {
		return override.config
	}
	return c
}
