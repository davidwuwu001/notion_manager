package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestBridgeProbeDisabledLeavesRequestAndOutputUnchanged(t *testing.T) {
	var out bytes.Buffer
	h := bridgeProbeWrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probeFromContext(r.Context()) != nil {
			t.Error("disabled probe attached")
		}
		w.Header().Set("X-Test", "ok")
		_, _ = w.Write([]byte("response"))
	}), false, &out)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses", strings.NewReader("secret")))
	if w.Body.String() != "response" || w.Header().Get("X-Test") != "ok" || out.Len() != 0 {
		t.Fatalf("disabled changed behavior: %q %q", w.Body.String(), out.String())
	}
}

func TestBridgeProbeCapturesToolConversionAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tool, bodyField string
		handler               func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `"messages":[{"role":"user","content":"private-prompt"}]`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `"messages":[{"role":"user","content":"private-prompt"}]`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `"input":"private-prompt"`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.path, stream), func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 1)
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					p := probeFromContext(opts[0].Context)
					p.recordUpstream(200, true, 0, true)
					cb(`{"name":"Read","arguments":{"path":"private-token"}}`, false, nil)
					return nil
				}
				body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, tc.bodyField, tc.tool)
				var out bytes.Buffer
				w := httptest.NewRecorder()
				h := bridgeProbeWrap(RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)), true, &out)
				h.ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(body)))
				fixture.waitForIdle(t)
				var record bridgeProbeData
				if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
					t.Fatalf("decode probe: %v", err)
				}
				if w.Code != 200 || record.DeclaredCount != 1 || record.ConvertedCount != 1 || !record.HasText || record.ParsedTextCount != 1 || record.DeclaredCheck != probePass || record.RequiredCheck != probeUnchecked || record.WireToolItemCount != 1 || !record.WireTerminal {
					t.Fatalf("status=%d metadata=%+v", w.Code, record)
				}
				if strings.Contains(out.String(), "private-") || strings.Contains(out.String(), "Read") {
					t.Fatal("probe leaked sensitive fixture")
				}
			})
		}
	}
}

func TestBridgeProbePrivacyBoundsAndConcurrency(t *testing.T) {
	p := newBridgeProbe(probeResponses)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p.recordWireTool()
			}
		}()
	}
	wg.Wait()
	p.recordIngress(probeAuto, 1<<30, 1<<30)
	p.recordUpstream(200, true, 1<<30, true)
	p.recordParse(1<<30, 1<<30, true, probePass, probeReject)
	b, err := json.Marshal(p.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("secret")) || bytes.Contains(b, []byte("token")) || bytes.Contains(b, []byte("tool_name")) {
		t.Fatalf("private content in probe: %s", b)
	}
	var got map[string]any
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"declared_count", "converted_count", "native_tool_count", "parsed_text_count", "parsed_native_count", "wire_tool_item_count"} {
		if n, ok := got[key].(float64); !ok || n > 1000 {
			t.Errorf("unbounded %s: %v", key, got[key])
		}
	}
}

func TestBridgeProbeResponseProtocolSelection(t *testing.T) {
	for _, tc := range []struct {
		path     string
		expected probeProtocol
	}{{"/v1/messages", probeAnthropic}, {"/v1/chat/completions", probeChat}, {"/v1/responses", probeResponses}} {
		var out bytes.Buffer
		h := bridgeProbeWrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := probeFromContext(r.Context())
			if p == nil || p.snapshot().Protocol != tc.expected {
				t.Error("wrong protocol")
			}
			_, _ = w.Write([]byte("ok"))
		}), true, &out)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", tc.path, nil))
		if w.Body.String() != "ok" || out.Len() == 0 {
			t.Fatal("missing response or probe")
		}
	}
	if probeFromContext(context.Background()) != nil {
		t.Fatal("unexpected context probe")
	}
}

func TestBridgeProbeTracksNDJSONWithoutContent(t *testing.T) {
	for _, tc := range []struct {
		stream                 string
		wantTerminal, wantText bool
		wantNative             int
	}{
		{`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"private-secret"},{"type":"tool_use","id":"fixture","name":"Read","input":{"path":"private-token"}}],"finishedAt":1}`, true, true, 1},
		{`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"private-secret"}]}`, false, true, 0},
	} {
		p := newBridgeProbe(probeResponses)
		_ = parseNDJSONStream(strings.NewReader(tc.stream), "", func(string, bool, *UsageInfo) {}, nil, nil, nil, nil, nil, nil, p)
		s := p.snapshot()
		if s.TerminalSeen != tc.wantTerminal || s.HasText != tc.wantText || s.NativeToolCount != tc.wantNative {
			t.Errorf("unexpected metadata: %+v", s)
		}
		b, _ := json.Marshal(s)
		if strings.Contains(string(b), "private") || strings.Contains(string(b), "Read") {
			t.Fatal("probe leaked fixture")
		}
	}
}

type probeNonFlushWriter struct {
	header http.Header
	body   bytes.Buffer
}

func (w *probeNonFlushWriter) Header() http.Header            { return w.header }
func (w *probeNonFlushWriter) WriteHeader(int)                {}
func (w *probeNonFlushWriter) Write(data []byte) (int, error) { return w.body.Write(data) }

func TestBridgeProbePreservesWriterFlushCapability(t *testing.T) {
	var out bytes.Buffer
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); ok {
			t.Error("unexpected Flush capability")
		}
		_, _ = w.Write([]byte("ok"))
	})
	w := &probeNonFlushWriter{header: make(http.Header)}
	bridgeProbeWrap(next, true, &out).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", nil))
	if w.body.String() != "ok" {
		t.Fatal("lost output")
	}
}

func TestBridgeProbeCapturesRejectionWithoutSensitivePayload(t *testing.T) {
	cases := []struct {
		path, tool, bodyField string
		handler               func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `"messages":[{"role":"user","content":"private-prompt"}]`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `"messages":[{"role":"user","content":"private-prompt"}]`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `"input":"private-prompt"`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			for _, kind := range []string{"native", "text"} {
				t.Run(fmt.Sprintf("%s/%t/%s", tc.path, stream, kind), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
						p := probeFromContext(opts[0].Context)
						if kind == "native" {
							*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "fixture", Name: "private-tool", Input: json.RawMessage(`{"secret":"private-token"}`)}}
							p.recordUpstream(200, false, 1, true)
						} else {
							p.recordUpstream(200, true, 0, true)
							cb(`{"name":"private-tool","arguments":{"secret":"private-token"}}`, false, nil)
						}
						return nil
					}
					body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, tc.bodyField, tc.tool)
					var out bytes.Buffer
					w := httptest.NewRecorder()
					bridgeProbeWrap(RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)), true, &out).ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(body)))
					fixture.waitForIdle(t)
					var record bridgeProbeData
					if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
						t.Fatal(err)
					}
					if record.DeclaredCheck != probeReject || record.WireToolItemCount != 0 || record.WireTerminal {
						t.Fatalf("unsafe metadata: %+v", record)
					}
					wantClass := probeNativeNone
					if kind == "native" {
						wantClass = probeNativeOther
					}
					if record.NativeToolClass != wantClass {
						t.Fatalf("native class = %q, want %q", record.NativeToolClass, wantClass)
					}
					if strings.Contains(out.String(), "private") || strings.Contains(out.String(), "Read") {
						t.Fatal("sensitive output")
					}
				})
			}
		}
	}
}

func TestBridgeProbeNoToolLeavesChecksUnobserved(t *testing.T) {
	fixture := newUpgradeProtocolFixture(t, 1)
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
		probeFromContext(opts[0].Context).recordUpstream(200, true, 0, true)
		cb("ordinary response", false, nil)
		return nil
	}
	var out bytes.Buffer
	w := httptest.NewRecorder()
	body := `{"model":"sonnet-4.6","max_tokens":20,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":"auto"}`
	bridgeProbeWrap(RequestLogMiddleware(fixture.store, HandleAnthropicMessages(fixture.pool)), true, &out).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
	fixture.waitForIdle(t)
	var d bridgeProbeData
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &d); err != nil {
		t.Fatal(err)
	}
	if d.ParsedTextCount != 0 || d.DeclaredCheck != probeUnchecked || d.RequiredCheck != probeUnchecked || d.WireToolItemCount != 0 || !d.WireTerminal {
		t.Fatalf("incorrect no-tool probe: %+v", d)
	}
}

type probeDisconnectedWriter struct{ header http.Header }

func (w *probeDisconnectedWriter) Header() http.Header       { return w.header }
func (w *probeDisconnectedWriter) WriteHeader(int)           {}
func (w *probeDisconnectedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (w *probeDisconnectedWriter) Flush()                    {}

func TestBridgeProbeDoesNotMarkDisconnectedStreamAsDelivered(t *testing.T) {
	for _, tc := range []struct {
		path, tool, bodyField string
		handler               func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `"messages":[{"role":"user","content":"hello"}]`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `"messages":[{"role":"user","content":"hello"}]`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `"input":"hello"`, HandleOpenAIResponses},
	} {
		t.Run(tc.path, func(t *testing.T) {
			fixture := newUpgradeProtocolFixture(t, 1)
			inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
				probeFromContext(opts[0].Context).recordUpstream(200, true, 0, true)
				cb(`{"name":"Read","arguments":{}}`, false, nil)
				return nil
			}
			body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":true,%s,"tools":[%s],"tool_choice":"auto"}`, tc.bodyField, tc.tool)
			var out bytes.Buffer
			w := &probeDisconnectedWriter{header: make(http.Header)}
			bridgeProbeWrap(RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)), true, &out).ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(body)))
			fixture.waitForIdle(t)
			var d bridgeProbeData
			if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &d); err != nil {
				t.Fatal(err)
			}
			if d.WireTerminal || d.WireToolItemCount != 0 {
				t.Fatalf("disconnected output counted as written: %+v", d)
			}
		})
	}
}

func TestBridgeProbeTracksFilteredBridgeToolCount(t *testing.T) {
	tools := []struct {
		name, tools string
		count       int
	}{
		{"read", `{"name":"Read","input_schema":{"type":"object"}}`, 1},
		{"fetch", `{"name":"WebFetch","input_schema":{"type":"object"}}`, 0},
		{"search", `{"name":"WebSearch","input_schema":{"type":"object"}}`, 1},
		{"both", `{"name":"Read","input_schema":{"type":"object"}},{"name":"WebFetch","input_schema":{"type":"object"}}`, 1},
	}
	for _, entry := range tools {
		for _, choice := range []string{"auto", "none"} {
			t.Run(entry.name+"/"+choice, func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 1)
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					cb("hello", false, nil)
					return nil
				}
				body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"messages":[{"role":"user","content":"hello"}],"tools":[%s],"tool_choice":"%s"}`, entry.tools, choice)
				var out bytes.Buffer
				w := httptest.NewRecorder()
				bridgeProbeWrap(RequestLogMiddleware(fixture.store, HandleAnthropicMessages(fixture.pool)), true, &out).ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
				fixture.waitForIdle(t)
				var d bridgeProbeData
				if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &d); err != nil {
					t.Fatal(err)
				}
				want := entry.count
				if choice == "none" {
					want = 0
				}
				if d.BridgeToolCount == nil || *d.BridgeToolCount != want {
					t.Fatalf("got %+v, want %d", d.BridgeToolCount, want)
				}
			})
		}
	}
}

func TestBridgeProbeNativeToolClass(t *testing.T) {
	allowed := map[string]bool{"Read": true, "callFunction": true, "WebSearch": true, "private-secret-name": true}
	cases := []struct {
		name    string
		entries []AgentValueEntry
		want    probeNativeClass
	}{
		{"none", nil, probeNativeNone},
		{"ignore unrelated", []AgentValueEntry{{Type: "text", Name: "Read"}}, probeNativeNone},
		{"notion", []AgentValueEntry{{Type: "tool_use", Name: "callFunction"}}, probeNativeCallFunction},
		{"search", []AgentValueEntry{{Type: "tool_use", Name: "WebSearch"}}, probeNativeWebSearch},
		{"declared repeated", []AgentValueEntry{{Type: "tool_use", Name: "Read"}, {Type: "tool_use", Name: "private-secret-name"}}, probeNativeDeclared},
		{"unknown", []AgentValueEntry{{Type: "tool_use", Name: "private-unknown"}}, probeNativeOther},
		{"missing", []AgentValueEntry{{Type: "tool_use"}}, probeNativeOther},
		{"mixed", []AgentValueEntry{{Type: "tool_use", Name: "Read"}, {Type: "tool_use", Name: "WebSearch"}}, probeNativeMixed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newBridgeProbe(probeResponses)
			if p.snapshot().NativeToolClass != probeNativeUnobserved {
				t.Fatal("must begin unobserved")
			}
			p.recordNativeClass(tc.entries, allowed)
			if got := p.snapshot().NativeToolClass; got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			encoded, err := json.Marshal(p.snapshot())
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte("private")) || bytes.Contains(encoded, []byte("Read")) {
				t.Fatal("sensitive name escaped")
			}
		})
	}
}

func TestBridgeProbeNativeToolClassConcurrentAndPrivate(t *testing.T) {
	p := newBridgeProbe(probeResponses)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				p.recordNativeClass(nil, nil)
			case 1:
				p.recordNativeClass([]AgentValueEntry{{Type: "tool_use", Name: "callFunction", Input: json.RawMessage(`{"secret":"private-token"}`)}}, nil)
			case 2:
				p.recordNativeClass([]AgentValueEntry{{Type: "tool_use", Name: "private-secret-name"}}, map[string]bool{"private-secret-name": true})
			}
		}(i)
	}
	wg.Wait()
	if p.snapshot().NativeToolClass != probeNativeMixed {
		t.Fatal("concurrent observations should aggregate to mixed")
	}
	encoded, err := json.Marshal(p.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private")) {
		t.Fatal("sensitive material escaped")
	}
}

func TestBridgeProbeEscapedWrapperPresence(t *testing.T) {
	if newBridgeProbe(probeResponses).snapshot().WrapperObserved {
		t.Fatal("unobserved collection marked observed")
	}
	cases := []struct {
		text          string
		slash, entity bool
	}{
		{`ordinary private-token`, false, false},
		{`<tool_call>{"name":"private-tool"}</tool_call>`, false, false},
		{`\<tool_call\>{"name":"private-tool"}\</tool_call\>`, true, false},
		{`&lt;tool_call&gt;{"name":"private-tool"}&lt;/tool_call&gt;`, false, true},
		{"```\n\\<tool_call\\> private-token\n```", true, false},
		{`Quoted: &lt;tool_call&gt; private-token`, false, true},
	}
	for _, tc := range cases {
		p := newBridgeProbe(probeResponses)
		p.recordWrapperPresence(tc.text)
		s := p.snapshot()
		if !s.WrapperObserved || s.EscapedToolWrapper != tc.slash || s.EntityToolWrapper != tc.entity {
			t.Fatalf("wrong wrapper flags: %+v", s)
		}
		encoded, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("private")) {
			t.Fatal("private output escaped")
		}
	}
}

func TestBridgeProbeEscapedWrapperPresenceConcurrent(t *testing.T) {
	p := newBridgeProbe(probeResponses)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				p.recordWrapperPresence(`\<tool_call\> private-token`)
			} else {
				p.recordWrapperPresence(`&lt;tool_call&gt; private-token`)
			}
		}(i)
	}
	wg.Wait()
	s := p.snapshot()
	if !s.EscapedToolWrapper || !s.EntityToolWrapper {
		t.Fatal("flags not aggregated")
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private")) {
		t.Fatal("private output escaped")
	}
}

func TestBridgeProbeEscapedWrappersAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tool, bodyField string
		handler               func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `"messages":[{"role":"user","content":"private-prompt"}]`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `"messages":[{"role":"user","content":"private-prompt"}]`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `"input":"private-prompt"`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.path, stream), func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 1)
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					probeFromContext(opts[0].Context).recordUpstream(200, true, 0, true)
					cb(`Quoted example: \<tool_call\> private-token &lt;tool_call&gt;`, false, nil)
					return nil
				}
				body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, tc.bodyField, tc.tool)
				var out bytes.Buffer
				w := httptest.NewRecorder()
				bridgeProbeWrap(RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)), true, &out).ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(body)))
				fixture.waitForIdle(t)
				var record bridgeProbeData
				if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
					t.Fatal(err)
				}
				if !record.EscapedToolWrapper || !record.EntityToolWrapper {
					t.Fatalf("missing presence flags: %+v", record)
				}
				if strings.Contains(out.String(), "private") || strings.Contains(out.String(), "Read") {
					t.Fatal("probe leaked sensitive text")
				}
			})
		}
	}
}

func TestBridgeProbeFailedInferenceWrapperUnobserved(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			fixture := newUpgradeProtocolFixture(t, 1)
			inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
				cb(`\<tool_call\> private-token`, false, nil)
				return fmt.Errorf("fixture failure")
			}
			body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,"input":"fixture","tools":[{"type":"function","name":"Read","parameters":{"type":"object"}}]}`, stream)
			var out bytes.Buffer
			w := httptest.NewRecorder()
			bridgeProbeWrap(RequestLogMiddleware(fixture.store, HandleOpenAIResponses(fixture.pool)), true, &out).ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body)))
			fixture.waitForIdle(t)
			var record bridgeProbeData
			if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
				t.Fatal(err)
			}
			if record.WrapperObserved || record.EscapedToolWrapper || record.EntityToolWrapper {
				t.Fatal("failed inference should remain unobserved")
			}
		})
	}
}
