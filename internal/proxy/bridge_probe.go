package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

// bridgeProbe is intentionally limited to bounded counters, booleans and
// closed enums. It never stores request text, user data, IDs or tool names.
type probeProtocol string
type probeChoice string
type probeCheck string
type probeNativeClass string

const (
	probeAnthropic          probeProtocol    = "anthropic"
	probeChat               probeProtocol    = "chat"
	probeResponses          probeProtocol    = "responses"
	probeAuto               probeChoice      = "auto"
	probeRequired           probeChoice      = "required"
	probeForced             probeChoice      = "forced"
	probeNone               probeChoice      = "none"
	probePass               probeCheck       = "pass"
	probeReject             probeCheck       = "reject"
	probeUnchecked          probeCheck       = "unchecked"
	probeNativeUnobserved   probeNativeClass = "unobserved"
	probeNativeNone         probeNativeClass = "none"
	probeNativeCallFunction probeNativeClass = "call_function"
	probeNativeWebSearch    probeNativeClass = "web_search"
	probeNativeDeclared     probeNativeClass = "declared"
	probeNativeOther        probeNativeClass = "other"
	probeNativeMixed        probeNativeClass = "mixed"
)

type bridgeProbeData struct {
	Protocol           probeProtocol    `json:"protocol"`
	Choice             probeChoice      `json:"choice"`
	DeclaredCount      int              `json:"declared_count"`
	ConvertedCount     int              `json:"converted_count"`
	BridgeToolCount    *int             `json:"bridge_tool_count,omitempty"`
	UpstreamHTTPClass  int              `json:"upstream_http_class"`
	TerminalSeen       bool             `json:"terminal_seen"`
	HasText            bool             `json:"has_text"`
	NativeToolCount    int              `json:"native_tool_count"`
	NativeToolClass    probeNativeClass `json:"native_tool_class"`
	EscapedToolWrapper bool             `json:"escaped_tool_wrapper"`
	EntityToolWrapper  bool             `json:"entity_tool_wrapper"`
	WrapperObserved    bool             `json:"wrapper_observed"`
	ParsedTextCount    int              `json:"parsed_text_count"`
	ParsedNativeCount  int              `json:"parsed_native_count"`
	DeclaredCheck      probeCheck       `json:"declared_check"`
	RequiredCheck      probeCheck       `json:"required_check"`
	DoneTextPresent    bool             `json:"done_text_present"`
	WireToolItemCount  int              `json:"wire_tool_item_count"`
	WireTerminal       bool             `json:"wire_terminal"`
}
type bridgeProbe struct {
	mu   sync.Mutex
	data bridgeProbeData
}
type bridgeProbeContextKey struct{}

var bridgeProbeOutputMu sync.Mutex

func newBridgeProbe(protocol probeProtocol) *bridgeProbe {
	switch protocol {
	case probeAnthropic, probeChat, probeResponses:
	default:
		return nil
	}
	return &bridgeProbe{data: bridgeProbeData{Protocol: protocol, Choice: probeAuto, DeclaredCheck: probeUnchecked, RequiredCheck: probeUnchecked, NativeToolClass: probeNativeUnobserved}}
}

// none means the completed collection contained no native tool_use; it does
// not prove what the upstream emitted. Failed empty collections are unobserved.
func (p *bridgeProbe) recordNativeClass(entries []AgentValueEntry, allowed map[string]bool) {
	if p == nil {
		return
	}
	class := probeNativeNone
	for _, entry := range entries {
		if entry.Type != "tool_use" {
			continue
		}
		item := probeNativeOther
		switch {
		case entry.Name == "callFunction":
			item = probeNativeCallFunction
		case entry.Name == "WebSearch":
			item = probeNativeWebSearch
		case entry.Name != "" && allowed[entry.Name]:
			item = probeNativeDeclared
		}
		class = mergeNativeClass(class, item)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.NativeToolClass = mergeNativeClass(p.data.NativeToolClass, class)
}
func mergeNativeClass(a, b probeNativeClass) probeNativeClass {
	if a == probeNativeUnobserved || a == probeNativeNone {
		return b
	}
	if b == probeNativeUnobserved || b == probeNativeNone || a == b {
		return a
	}
	return probeNativeMixed
}
func boundedProbeCount(n int) int {
	if n < 0 {
		return 0
	}
	if n > 1000 {
		return 1000
	}
	return n
}
func (p *bridgeProbe) snapshot() bridgeProbeData { p.mu.Lock(); defer p.mu.Unlock(); return p.data }
func probeFromContext(ctx context.Context) *bridgeProbe {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(bridgeProbeContextKey{}).(*bridgeProbe)
	return p
}
func (p *bridgeProbe) recordIngress(choice probeChoice, declared, converted int) {
	if p == nil {
		return
	}
	switch choice {
	case probeAuto, probeRequired, probeForced, probeNone:
	default:
		choice = probeAuto
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.Choice = choice
	p.data.DeclaredCount = boundedProbeCount(declared)
	p.data.ConvertedCount = boundedProbeCount(converted)
}

func (p *bridgeProbe) recordBridgeToolCount(count int) {
	if p == nil {
		return
	}
	value := boundedProbeCount(count)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.BridgeToolCount = &value
}

// Presence only: these markers may occur in quoted text or code examples.
func (p *bridgeProbe) recordWrapperPresence(content string) {
	if p == nil {
		return
	}
	escaped := strings.Contains(content, `\<tool_call\>`) || strings.Contains(content, `\</tool_call\>`)
	entity := strings.Contains(content, `&lt;tool_call&gt;`) || strings.Contains(content, `&lt;/tool_call&gt;`)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.WrapperObserved = true
	p.data.EscapedToolWrapper = p.data.EscapedToolWrapper || escaped
	p.data.EntityToolWrapper = p.data.EntityToolWrapper || entity
}
func (p *bridgeProbe) recordUpstream(status int, text bool, native int, terminal bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if status >= 100 && status <= 599 {
		p.data.UpstreamHTTPClass = status / 100
	}
	p.data.HasText = p.data.HasText || text
	p.data.NativeToolCount = boundedProbeCount(p.data.NativeToolCount + native)
	p.data.TerminalSeen = p.data.TerminalSeen || terminal
}
func (p *bridgeProbe) recordParse(text, native int, done bool, declared, required probeCheck) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.ParsedTextCount = boundedProbeCount(p.data.ParsedTextCount + text)
	p.data.ParsedNativeCount = boundedProbeCount(p.data.ParsedNativeCount + native)
	p.data.DoneTextPresent = p.data.DoneTextPresent || done
	if declared == probePass || declared == probeReject {
		p.data.DeclaredCheck = declared
	}
	if required == probePass || required == probeReject {
		p.data.RequiredCheck = required
	}
}
func (p *bridgeProbe) recordWireTool() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.WireToolItemCount = boundedProbeCount(p.data.WireToolItemCount + 1)
}
func (p *bridgeProbe) recordWireTerminal() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.WireTerminal = true
}
func probeChoiceFor(value interface{}) probeChoice {
	if s, ok := value.(string); ok {
		switch s {
		case "none":
			return probeNone
		case "required", "any":
			return probeRequired
		}
	}
	if m, ok := value.(map[string]interface{}); ok {
		switch m["type"] {
		case "none":
			return probeNone
		case "required", "any":
			return probeRequired
		case "tool", "function", "custom":
			return probeForced
		}
	}
	return probeAuto
}

type bridgeProbeWriter struct {
	http.ResponseWriter
	probe *bridgeProbe
}

func (w *bridgeProbeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type flushingBridgeProbeWriter struct{ *bridgeProbeWriter }

func (w *flushingBridgeProbeWriter) Unwrap() http.ResponseWriter { return w.bridgeProbeWriter }
func (w *flushingBridgeProbeWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func probeFromWriter(w http.ResponseWriter) *bridgeProbe {
	for n := 0; n < 8 && w != nil; n++ {
		if v, ok := w.(*bridgeProbeWriter); ok {
			return v.probe
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		w = u.Unwrap()
	}
	return nil
}

func probeCheckResult(ok bool) probeCheck {
	if ok {
		return probePass
	}
	return probeReject
}

func (p *bridgeProbe) recordPrepared(content string, native []AgentValueEntry, prepared preparedToolBridgeResponse, declared, required, checkRequired bool) {
	if p == nil {
		return
	}
	parsedText := 0
	if len(native) == 0 {
		calls, _, ok := parseToolCalls(content)
		if ok {
			parsedText = len(calls)
		}
	}
	declaredStatus := probeUnchecked
	if parsedText > 0 || len(native) > 0 {
		declaredStatus = probeCheckResult(declared)
	}
	requiredStatus := probeUnchecked
	if checkRequired {
		requiredStatus = probeCheckResult(required)
	}
	p.recordParse(parsedText, len(native), prepared.DoneText != "", declaredStatus, requiredStatus)
}
func bridgeProbeWrap(next http.Handler, enabled bool, out io.Writer) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var kind probeProtocol
		switch r.URL.Path {
		case "/v1/messages":
			kind = probeAnthropic
		case "/v1/chat/completions":
			kind = probeChat
		case "/v1/responses":
			kind = probeResponses
		default:
			next.ServeHTTP(w, r)
			return
		}
		p := newBridgeProbe(kind)
		defer func() {
			b, err := json.Marshal(p.snapshot())
			if err == nil {
				bridgeProbeOutputMu.Lock()
				defer bridgeProbeOutputMu.Unlock()
				fmt.Fprintln(out, string(b))
			}
		}()
		writer := &bridgeProbeWriter{ResponseWriter: w, probe: p}
		var response http.ResponseWriter = writer
		if _, ok := w.(http.Flusher); ok {
			response = &flushingBridgeProbeWriter{writer}
		}
		next.ServeHTTP(response, r.WithContext(context.WithValue(r.Context(), bridgeProbeContextKey{}, p)))
	})
}

// BridgeProbeHandler is inert in ordinary builds. An isolated diagnostic build
// must explicitly use -tags bridgeprobe, set NOTION_MANAGER_BRIDGE_PROBE=1,
// and provide a writable inherited file descriptor via NOTION_MANAGER_BRIDGE_PROBE_FD.
func BridgeProbeHandler(next http.Handler) http.Handler {
	if !bridgeProbeBuildEnabled() || os.Getenv("NOTION_MANAGER_BRIDGE_PROBE") != "1" {
		return next
	}
	fd, err := strconv.Atoi(os.Getenv("NOTION_MANAGER_BRIDGE_PROBE_FD"))
	if err != nil || fd < 3 || fd > 1024 {
		return next
	}
	if !bridgeProbeIsPipe(fd) {
		return next
	}
	return bridgeProbeWrap(next, true, os.NewFile(uintptr(fd), "bridge-probe"))
}
