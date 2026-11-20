package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrdinaryJSONIsNotAToolCall(t *testing.T) {
	for _, text := range []string{
		`{"name":"Alice","role":"engineer"}`,
		`{"name":"Read","description":"ordinary data"}`,
		`{"tool_call":{"name":"Read"}}`,
		"```json\n" + `{"name":"Read","description":"ordinary data"}` + "\n```",
		"<tool_call>" + `{"name":"Read"}` + "</tool_call>",
		`{"name":"Read"}` + "\n" + `{"name":"Alice"}`,
		`{"NAME":"Read","arguments":{"path":"first","path":"second"}}`,
		`{"name":"Read","Arguments":{"path":"first","path":"second"}}`,
		`{"TOOL_CALL":{"name":"Read","arguments":{}}}`,
	} {
		calls, remaining, ok := parseToolCalls(text)
		if ok || len(calls) != 0 || remaining != text {
			t.Errorf("ordinary content misclassified or changed: %q => %d %q", text, len(calls), remaining)
		}
	}
}

func TestExplicitToolArgumentsRemainCompatible(t *testing.T) {
	for _, text := range []string{
		`{"name":"Read","arguments":{}}`,
		`{"name":"custom","arguments":"raw custom input"}`,
		`{"tool_call":{"name":"Read","arguments":{}}}`,
		"```json\n" + `{"name":"Read","arguments":{}}` + "\n```",
		"<tool_call>" + `{"name":"Read","arguments":{}}` + "</tool_call>",
		`{"name":"__done__","arguments":{"result":"ok"}}`,
	} {
		calls, _, ok := parseToolCalls(text)
		if !ok || len(calls) != 1 {
			t.Errorf("valid envelope lost: %q", text)
		}
	}
}

func TestOrdinaryJSONDoesNotCauseToolActionAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tool, input string
		handler           func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `"messages":[{"role":"user","content":"hello"}]`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `"messages":[{"role":"user","content":"hello"}]`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `"input":"hello"`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.path, stream), func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 1)
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
					cb(`{"name":"Read","description":"ordinary data"}`, false, nil)
					return nil
				}
				body := fmt.Sprintf(`{"model":"sonnet-4.6","stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, tc.input, tc.tool)
				w := httptest.NewRecorder()
				RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(body)))
				fixture.waitForIdle(t)
				out := w.Body.String()
				if w.Code != 200 || !strings.Contains(out, "ordinary data") || strings.Contains(out, `"type":"tool_use"`) || strings.Contains(out, `"type":"function_call"`) || strings.Contains(out, `"tool_calls"`) {
					t.Fatalf("status=%d unexpected response %s", w.Code, out)
				}
			})
		}
	}
}

func TestAmbiguousToolJSONNeverEmitsActionsAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tool, input string
		handler           func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `"messages":[{"role":"user","content":"hello"}]`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `"messages":[{"role":"user","content":"hello"}]`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `"input":"hello"`, HandleOpenAIResponses},
	}
	duplicateName := `{"name":"Other","name":"Read","arguments":{"path":"fixture"}}`
	duplicateArgs := `{"name":"Read","arguments":{"path":"first"},"arguments":{"path":"second"}}`
	duplicateNested := `{"name":"Read","arguments":{"nested":{"path":"first","path":"second"}}}`
	fixtures := []string{duplicateName, duplicateArgs, duplicateNested,
		`{"name":"Read","NAME":"Other","arguments":{}}`,
		`{"name":"Read","arguments":{},"Arguments":{"path":"second"}}`,
		`{"tool_call":{"name":"Read","arguments":{}},"TOOL_CALL":{"name":"Other","arguments":{}}}`,
		`{"tool_call":` + duplicateName + `}`,
		"```json\n" + duplicateArgs + "\n```",
		"<tool_call>" + duplicateNested + "</tool_call>",
		`{"name":"Read","arguments":{}}` + "\n" + duplicateNested,
		"<tool_call>" + `{"name":"Read","arguments":{}}` + "</tool_call>\n```json\n" + duplicateArgs + "\n```",
		"<tool_call>" + `{"name":"Read","arguments":{"path":"first"}}` + "</tool_call>\n```json\n" + `{"name":"Read","arguments":{"path":"second"}}` + "\n```",
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			for i, response := range fixtures {
				t.Run(fmt.Sprintf("%s/%t/%d", tc.path, stream, i), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					calls := 0
					inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
						calls++
						cb(response, false, nil)
						return nil
					}
					body := fmt.Sprintf(`{"model":"sonnet-4.6","stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, tc.input, tc.tool)
					w := httptest.NewRecorder()
					RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(body)))
					fixture.waitForIdle(t)
					out := w.Body.String()
					if calls != 1 || w.Code != 502 || strings.Contains(out, `"type":"tool_use"`) || strings.Contains(out, `"type":"function_call"`) || strings.Contains(out, `"tool_calls"`) || strings.Contains(out, "first") || strings.Contains(out, "second") {
						t.Fatalf("ambiguous action escaped: status=%d calls=%d body=%s", w.Code, calls, out)
					}
				})
			}
		}
	}
}

func TestEquivalentMixedToolEnvelopesAreDeduplicated(t *testing.T) {
	content := "<tool_call>{\"name\":\"Read\",\"arguments\":{\"path\":\"fixture\"}}</tool_call>\n```json\n{\"arguments\":{\"path\":\"fixture\"},\"name\":\"Read\"}\n```"
	calls, ambiguous := explicitTextToolCalls(content)
	if ambiguous || len(calls) != 1 || calls[0].Function.Name != "Read" {
		t.Fatalf("equivalent tool envelopes should resolve to one call: ambiguous=%v, count=%d", ambiguous, len(calls))
	}
	parsed, _, ok := parseToolCalls(content)
	if !ok || len(parsed) != 1 {
		t.Fatalf("equivalent tool envelopes should produce exactly one parsed call: ok=%v, count=%d", ok, len(parsed))
	}
}
