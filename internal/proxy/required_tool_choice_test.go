package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequiredToolChoiceNeverReportsPlainAnswerAsSuccess(t *testing.T) {
	oldConfig, oldCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	defer func() { AppConfig, inferenceCaller = oldConfig, oldCaller }()
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		cb("I cannot follow those instructions.", false, nil)
		return nil
	}
	for _, streaming := range []bool{false, true} {
		w := httptest.NewRecorder()
		var err error
		if streaming {
			err = handleAnthropicStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, map[string]interface{}{"type": "any"})
		} else {
			err = handleAnthropicNonStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, map[string]interface{}{"type": "any"})
		}
		if !errors.Is(err, ErrToolBridgeNoTool) || strings.Contains(w.Body.String(), "message_stop") {
			t.Fatalf("stream=%v: err=%v response=%s", streaming, err, w.Body.String())
		}
	}
}

func TestAutoChoiceRejectsUndeclaredNativeAndTextTools(t *testing.T) {
	oldConfig, oldCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	defer func() { AppConfig, inferenceCaller = oldConfig, oldCaller }()
	contract := toolChoiceContract{choice: "auto", allowed: map[string]bool{"Read": true}}
	for _, kind := range []string{"native", "text"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", kind, stream), func(t *testing.T) {
				w := httptest.NewRecorder()
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					if kind == "native" {
						*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "fixture", Name: "callFunction", Input: json.RawMessage(`{"function":"connections.fs.readFiles"}`)}}
					} else {
						cb(`{"name":"Bash","arguments":{"command":"fixture"}}`, false, nil)
					}
					if strings.Contains(w.Body.String(), `"name":"Bash"`) {
						t.Error("undeclared tool emitted before inference completed")
					}
					return nil
				}
				var err error
				if stream {
					err = handleAnthropicStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, contract)
				} else {
					err = handleAnthropicNonStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, contract)
				}
				if !errors.Is(err, ErrToolBridgeNoTool) || strings.Contains(w.Body.String(), `"name":"Bash"`) || strings.Contains(w.Body.String(), `"name":"callFunction"`) || strings.Contains(w.Body.String(), "message_stop") {
					t.Fatalf("err=%v body=%s", err, w.Body.String())
				}
			})
		}
	}
}

func TestAutoChoiceEnforcesDeclaredToolsAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tool string
		handler    func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			for _, kind := range []string{"native", "text"} {
				t.Run(fmt.Sprintf("%s/stream-%t/%s", tc.path, stream, kind), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					calls := 0
					inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
						calls++
						if kind == "native" {
							*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "fixture", Name: "callFunction", Input: json.RawMessage(`{"function":"connections.fs.readFiles"}`)}}
						} else {
							cb(`{"name":"Bash","arguments":{"command":"fixture"}}`, false, nil)
						}
						return nil
					}
					messages := `"messages":[{"role":"user","content":"hello"}]`
					if tc.path == "/v1/responses" {
						messages = `"input":"hello"`
					}
					body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, messages, tc.tool)
					w := httptest.NewRecorder()
					r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
					RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
					fixture.waitForIdle(t)
					out := w.Body.String()
					if w.Code != 502 || calls != 1 || strings.Contains(out, `"name":"Bash"`) || strings.Contains(out, `"name":"callFunction"`) || strings.Contains(out, "message_stop") || strings.Contains(out, "response.completed") {
						t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, out)
					}
				})
			}
		}
	}
}

func TestAutoSearchRequiresDeclaredWebSearchAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, read, search string
		handler            func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `{"name":"WebSearch","input_schema":{"type":"object"}}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `{"type":"function","function":{"name":"WebSearch","parameters":{"type":"object"}}}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `{"type":"function","name":"WebSearch","parameters":{"type":"object"}}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			for _, declared := range []bool{false, true} {
				for _, kind := range []string{"native", "text"} {
					t.Run(fmt.Sprintf("%s/stream-%t/declared-%t/%s", tc.path, stream, declared, kind), func(t *testing.T) {
						fixture := newUpgradeProtocolFixture(t, 1)
						calls := 0
						inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
							calls++
							if calls == 1 {
								if kind == "native" {
									*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "search", Name: "WebSearch", Input: json.RawMessage(`{"query":"fixture"}`)}}
								} else {
									cb(`{"name":"WebSearch","arguments":{"query":"fixture"}}`, false, nil)
								}
							} else {
								cb("fixture search response", false, nil)
							}
							return nil
						}
						messages := `"messages":[{"role":"user","content":"hello"}]`
						if tc.path == "/v1/responses" {
							messages = `"input":"hello"`
						}
						tools := tc.read
						if declared {
							tools += "," + tc.search
						}
						body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, messages, tools)
						w := httptest.NewRecorder()
						r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
						RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
						fixture.waitForIdle(t)
						if !declared && (calls != 1 || w.Code != 502) {
							t.Fatalf("undeclared search executed: status=%d calls=%d", w.Code, calls)
						}
						if declared && (calls != 2 || w.Code != 200 || !strings.Contains(w.Body.String(), "fixture search response")) {
							t.Fatalf("declared search failed: status=%d calls=%d", w.Code, calls)
						}
					})
				}
			}
		}
	}
}

func TestAutoChoiceAllowsDeclaredNativeAndTextTools(t *testing.T) {
	oldConfig, oldCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	defer func() { AppConfig, inferenceCaller = oldConfig, oldCaller }()
	contract := toolChoiceContract{choice: "auto", allowed: map[string]bool{"Read": true}}
	for _, kind := range []string{"native", "text"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", kind, stream), func(t *testing.T) {
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					if kind == "native" {
						*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "fixture", Name: "Read", Input: json.RawMessage(`{"path":"fixture.txt"}`)}}
					} else {
						cb(`{"name":"Read","arguments":{"path":"fixture.txt"}}`, false, nil)
					}
					return nil
				}
				w := httptest.NewRecorder()
				var err error
				if stream {
					err = handleAnthropicStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, contract)
				} else {
					err = handleAnthropicNonStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, contract)
				}
				if err != nil || !strings.Contains(w.Body.String(), `"name":"Read"`) {
					t.Fatalf("err=%v body=%s", err, w.Body.String())
				}
			})
		}
	}
}

func TestAutoChoiceAllowsResponsesNamespacedAlias(t *testing.T) {
	body := `{"model":"sonnet-4.6","input":"hello","tools":[{"type":"namespace","name":"alpha","tools":[{"type":"function","name":"same_name","parameters":{"type":"object"}}]}],"tool_choice":"auto"}`
	var request OpenAIResponsesRequest
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatal(err)
	}
	converted, aliases, err := convertOpenAIResponsesRequestWithToolAliases(&request)
	if err != nil || len(converted.Tools) != 1 || len(aliases) != 1 {
		t.Fatalf("alias setup: %v", err)
	}
	alias := converted.Tools[0].Name
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream-%t", stream), func(t *testing.T) {
			fixture := newUpgradeProtocolFixture(t, 1)
			inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
				value, _ := json.Marshal(map[string]interface{}{"name": alias, "arguments": map[string]interface{}{"x": 1}})
				cb(string(value), false, nil)
				return nil
			}
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatal(err)
			}
			payload["stream"] = stream
			encoded, _ := json.Marshal(payload)
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(string(encoded)))
			RequestLogMiddleware(fixture.store, HandleOpenAIResponses(fixture.pool)).ServeHTTP(w, r)
			fixture.waitForIdle(t)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"same_name"`) || !strings.Contains(w.Body.String(), `"namespace":"alpha"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestForcedToolChoiceCannotEmitAnotherTool(t *testing.T) {
	oldConfig, oldCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	defer func() { AppConfig, inferenceCaller = oldConfig, oldCaller }()
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		cb(`{"name":"Other","arguments":{}}`, false, nil)
		return nil
	}
	w := httptest.NewRecorder()
	err := handleAnthropicStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, map[string]interface{}{"type": "tool", "name": "Read"})
	if !errors.Is(err, ErrToolBridgeNoTool) || strings.Contains(w.Body.String(), `"name":"Other"`) || strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("wrong tool emitted: err=%v response=%s", err, w.Body.String())
	}
}

func TestForcedToolChoiceRejectsMixedTools(t *testing.T) {
	p := preparedToolBridgeResponse{HasCalls: true, ToolCalls: []ToolCall{{Function: ToolCallFunction{Name: "Read"}}, {Function: ToolCallFunction{Name: "Other"}}}}
	if requiredToolSatisfied(p, true, "Read") {
		t.Fatal("forced choice accepted extra tool")
	}
}

func TestRequiredToolChoiceRejectsUndeclaredTool(t *testing.T) {
	p := preparedToolBridgeResponse{HasCalls: true, ToolCalls: []ToolCall{{Function: ToolCallFunction{Name: "Invented"}}}}
	contract := toolChoiceContract{choice: "required", allowed: map[string]bool{"Read": true}}
	required, name := requiredToolChoice([]interface{}{contract})
	if requiredToolSatisfied(p, required, name, contract.allowed) {
		t.Fatal("required choice accepted an undeclared tool")
	}
}

func TestExplicitToolChoiceAcceptsOnlyMatchingDeclaredTool(t *testing.T) {
	oldConfig, oldCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	defer func() { AppConfig, inferenceCaller = oldConfig, oldCaller }()
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		cb(`{"name":"Read","arguments":{"file_path":"fixture.txt"}}`, false, nil)
		return nil
	}
	contract := toolChoiceContract{choice: map[string]interface{}{"type": "tool", "name": "Read"}, allowed: map[string]bool{"Read": true}}
	for _, stream := range []bool{false, true} {
		w := httptest.NewRecorder()
		var err error
		if stream {
			err = handleAnthropicStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, contract)
		} else {
			err = handleAnthropicNonStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, contract)
		}
		if err != nil || !strings.Contains(w.Body.String(), `"name":"Read"`) {
			t.Fatalf("stream=%v err=%v body=%s", stream, err, w.Body.String())
		}
	}
}

func TestAutoChoiceStillPermitsPlainAnswer(t *testing.T) {
	oldConfig, oldCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	defer func() { AppConfig, inferenceCaller = oldConfig, oldCaller }()
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		cb("I cannot follow those instructions.", false, nil)
		return nil
	}
	w := httptest.NewRecorder()
	err := handleAnthropicNonStream(w, &Account{}, nil, "sonnet-4.6", "fixture", true, false, false, nil, true, nil, nil, nil, map[string]interface{}{"type": "auto"})
	if err != nil || !strings.Contains(w.Body.String(), "I cannot follow") {
		t.Fatalf("auto reply changed: err=%v response=%s", err, w.Body.String())
	}
}

func TestRequiredToolContractThroughAllProtocolsNeverRetriesOrLeaks(t *testing.T) {
	cases := []struct {
		path, body string
		handler    func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"model":"sonnet-4.6","max_tokens":20,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":{"type":"any"}}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"model":"sonnet-4.6","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}],"tool_choice":"required"}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"model":"sonnet-4.6","input":"hello","tools":[{"type":"function","name":"Read","parameters":{"type":"object"}}],"tool_choice":"required"}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			fixture := newUpgradeProtocolFixture(t, 2)
			calls := 0
			inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
				calls++
				cb("fixture-sensitive-refusal", false, nil)
				return nil
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
			fixture.waitForIdle(t)
			if w.Code != 502 || calls != 1 || strings.Contains(w.Body.String(), "fixture-sensitive-refusal") {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

func TestExplicitWebSearchNeverExecutesInternallyAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tools, required, forced string
		handler                       func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `[{"name":"WebSearch","input_schema":{"type":"object"}}]`, `{"type":"any"}`, `{"type":"tool","name":"WebSearch"}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `[{"type":"function","function":{"name":"WebSearch","parameters":{"type":"object"}}}]`, `"required"`, `{"type":"function","function":{"name":"WebSearch"}}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `[{"type":"function","name":"WebSearch","parameters":{"type":"object"}}]`, `"required"`, `{"type":"function","name":"WebSearch"}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, toolName := range []string{"WebSearch", "WebFetch"} {
			for _, stream := range []bool{false, true} {
				for _, originalChoice := range []string{tc.required, tc.forced} {
					choice := strings.ReplaceAll(originalChoice, "WebSearch", toolName)
					t.Run(fmt.Sprintf("%s/%s/%s/stream-%t", tc.path, toolName, choice, stream), func(t *testing.T) {
						fixture := newUpgradeProtocolFixture(t, 2)
						calls := 0
						inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
							calls++
							cb(`{"name":"WebSearch","arguments":{"query":"fixture-query"}}`, false, nil)
							return nil
						}
						messages := `"messages":[{"role":"user","content":"hello"}]`
						if tc.path == "/v1/responses" {
							messages = `"input":"hello"`
						}
						body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":%s,"tool_choice":%s}`, stream, messages, strings.ReplaceAll(tc.tools, "WebSearch", toolName), choice)
						w := httptest.NewRecorder()
						r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
						RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
						fixture.waitForIdle(t)
						if w.Code != 400 || calls != 0 || strings.Contains(w.Body.String(), "fixture-query") {
							t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
						}
					})
				}
			}
		}
	}
}

func TestMixedExplicitToolChoicesDisableUpstreamSearchAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, read, search, required, forced string
		handler                              func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `{"name":"WebSearch","input_schema":{"type":"object"}}`, `{"type":"any"}`, `{"type":"tool","name":"Read"}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `{"type":"function","function":{"name":"WebSearch","parameters":{"type":"object"}}}`, `"required"`, `{"type":"function","function":{"name":"Read"}}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `{"type":"function","name":"WebSearch","parameters":{"type":"object"}}`, `"required"`, `{"type":"function","name":"Read"}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, choice := range []string{tc.required, tc.forced} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream-%t", tc.path, choice, stream), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					calls := 0
					inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, disable bool, cb StreamCallback, opts ...CallOptions) error {
						calls++
						if !disable || len(opts) != 1 || opts[0].EnableWebSearch || opts[0].EnableWorkspaceSearch == nil || *opts[0].EnableWorkspaceSearch {
							t.Error("explicit tool selection enabled upstream search")
						} else {
							config := buildConfigValue("fixture", "", disable, opts[0].EnableWebSearch, opts[0].EnableWorkspaceSearch, true, false, false)
							for _, key := range []string{"enableComputer", "enableAgentIntegrations", "enableAgentAutomations", "enableCustomAgents", "enableScriptAgent", "useWebSearch"} {
								if config[key] != false {
									t.Errorf("%s not disabled", key)
								}
							}
						}
						cb(`{"name":"Read","arguments":{"path":"fixture.txt"}}`, false, nil)
						return nil
					}
					messages := `"messages":[{"role":"user","content":"hello"}]`
					if tc.path == "/v1/responses" {
						messages = `"input":"hello"`
					}
					body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s,%s],"tool_choice":%s}`, stream, messages, tc.read, tc.search, choice)
					w := httptest.NewRecorder()
					r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
					r.Header.Set("X-Web-Search", "true")
					RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
					fixture.waitForIdle(t)
					if w.Code != 200 || calls != 1 {
						t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
					}
				})
			}
		}
	}
}

func TestToolChoiceAutoRetainsBuiltinSearchConfiguration(t *testing.T) {
	cases := []struct {
		path, read, search string
		handler            func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `{"name":"WebSearch","input_schema":{"type":"object"}}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `{"type":"function","function":{"name":"WebSearch","parameters":{"type":"object"}}}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `{"type":"function","name":"WebSearch","parameters":{"type":"object"}}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", tc.path, stream), func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 1)
				calls := 0
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, disable bool, cb StreamCallback, opts ...CallOptions) error {
					calls++
					if disable || len(opts) != 1 || !opts[0].EnableWebSearch {
						t.Error("auto mode changed built-in search behavior")
					}
					cb("ordinary answer", false, nil)
					return nil
				}
				messages := `"messages":[{"role":"user","content":"hello"}]`
				if tc.path == "/v1/responses" {
					messages = `"input":"hello"`
				}
				body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s,%s],"tool_choice":"auto"}`, stream, messages, tc.read, tc.search)
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
				RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
				fixture.waitForIdle(t)
				if w.Code != 200 || calls != 1 {
					t.Fatalf("status=%d calls=%d", w.Code, calls)
				}
			})
		}
	}
}

func TestToolChoiceInvalidRequestRejectedAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tool, missing, wrong string
		handler                    func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, `{"type":"tool"}`, `{"type":"tool","name":"Unknown"}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, `{"type":"function","function":{}}`, `{"type":"function","function":{"name":"Unknown"}}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, `{"type":"tool"}`, `{"type":"function","name":"Unknown"}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			for i := 0; i < 3; i++ {
				t.Run(fmt.Sprintf("%s/stream-%t/case-%d", tc.path, stream, i), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					calls := 0
					inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, _ StreamCallback, _ ...CallOptions) error {
						calls++
						return nil
					}
					messages := `"messages":[{"role":"user","content":"hello"}]`
					if tc.path == "/v1/responses" {
						messages = `"input":"hello"`
					}
					tools, choice := `[`+tc.tool+`]`, tc.missing
					if i == 0 {
						tools = `[]`
						choice = `"required"`
					} else if i == 2 {
						choice = tc.wrong
					}
					body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":%s,"tool_choice":%s}`, stream, messages, tools, choice)
					w := httptest.NewRecorder()
					r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
					RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
					fixture.waitForIdle(t)
					if w.Code != 400 || calls != 0 {
						t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
					}
				})
			}
		}
	}
}

func TestToolChoiceNoneNeverEmitsToolAcrossProtocols(t *testing.T) {
	cases := []struct {
		path, tool string
		handler    func(*AccountPool) http.HandlerFunc
	}{
		{"/v1/messages", `{"name":"Read","input_schema":{"type":"object"}}`, HandleAnthropicMessages},
		{"/v1/chat/completions", `{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`, HandleOpenAIChatCompletions},
		{"/v1/responses", `{"type":"function","name":"Read","parameters":{"type":"object"}}`, HandleOpenAIResponses},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", tc.path, stream), func(t *testing.T) {
				fixture := newUpgradeProtocolFixture(t, 1)
				calls := 0
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, disable bool, cb StreamCallback, opts ...CallOptions) error {
					calls++
					if !disable || len(opts) == 0 || opts[0].EnableWebSearch || opts[0].EnableWorkspaceSearch == nil || *opts[0].EnableWorkspaceSearch {
						t.Error("none did not disable built-in and search tools")
					}
					cb(`{"name":"Read","arguments":{"path":"fixture.txt"}}`, false, nil)
					return nil
				}
				messages := `"messages":[{"role":"user","content":"hello"}]`
				if tc.path == "/v1/responses" {
					messages = `"input":"hello"`
				}
				body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s],"tool_choice":"none"}`, stream, messages, tc.tool)
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
				RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
				fixture.waitForIdle(t)
				out := w.Body.String()
				if w.Code != 200 || calls != 1 || !strings.Contains(out, "fixture.txt") || strings.Contains(out, `"type":"tool_use"`) || strings.Contains(out, `"type":"function_call"`) || strings.Contains(out, `"tool_calls"`) {
					t.Fatalf("status=%d calls=%d output=%s", w.Code, calls, out)
				}
			})
		}
	}
}

func TestRequiredToolsCannotSilentlyPassThroughResearchMode(t *testing.T) {
	fixture := newUpgradeProtocolFixture(t, 1)
	calls := 0
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, _ StreamCallback, _ ...CallOptions) error {
		calls++
		return nil
	}
	body := `{"model":"researcher","max_tokens":20,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":{"type":"any"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	RequestLogMiddleware(fixture.store, HandleAnthropicMessages(fixture.pool)).ServeHTTP(w, r)
	fixture.waitForIdle(t)
	if w.Code != 400 || calls != 0 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
}

func TestToolChoiceNoneObjectDisablesTools(t *testing.T) {
	if !toolChoiceIsNone(map[string]interface{}{"type": "none"}) {
		t.Fatal("object none not recognized")
	}
	if err := validateToolChoice(map[string]interface{}{"type": "none"}, nil); err != nil {
		t.Fatal(err)
	}
}
