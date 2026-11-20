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

func TestToolStreamWaitsForWholeResponseValidation(t *testing.T) {
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
			for _, scenario := range []string{"mixed-native", "mixed-undeclared", "mixed-different", "mixed-equivalent", "ordinary-json", "interrupted", "success"} {
				t.Run(fmt.Sprintf("%s/%s/stream-%t", tc.path, scenario, stream), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					calls := 0
					w := httptest.NewRecorder()
					inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
						calls++
						text := `{"name":"Read","arguments":{"file_path":"fixture.txt"}}`
						if scenario == "ordinary-json" {
							text = `{"name":"Alice","occupation":"engineer"}`
						}
						cb(text, false, nil)
						if strings.Contains(w.Body.String(), `"name":"Read"`) {
							t.Error("tool was emitted before upstream finished")
						}
						switch scenario {
						case "mixed-native":
							*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "x", Name: "callFunction", Input: json.RawMessage(`{}`)}}
						case "mixed-different":
							*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "x", Name: "Read", Input: json.RawMessage(`{"file_path":"other.txt"}`)}}
						case "mixed-equivalent", "ordinary-json":
							*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "x", Name: "Read", Input: json.RawMessage(`{"file_path":"fixture.txt"}`)}}
						case "mixed-undeclared":
							cb("\n"+`{"name":"Bash","arguments":{"command":"fixture"}}`, false, nil)
						case "interrupted":
							return errors.New("fixture upstream interrupted")
						case "success":
							cb("\n"+`{"name":"Read","arguments":{"file_path":"second.txt"}}`, false, nil)
						}
						return nil
					}
					body := fmt.Sprintf(`{"model":"sonnet-4.6","max_tokens":20,"stream":%t,%s,"tools":[%s],"tool_choice":"auto"}`, stream, tc.input, tc.tool)
					r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
					RequestLogMiddleware(fixture.store, tc.handler(fixture.pool)).ServeHTTP(w, r)
					fixture.waitForIdle(t)
					out := w.Body.String()
					if calls != 1 {
						t.Fatalf("upstream calls %d", calls)
					}
					if scenario == "success" || scenario == "mixed-equivalent" || scenario == "ordinary-json" {
						count := strings.Count(out, `"name":"Read"`)
						if tc.path == "/v1/responses" && stream {
							count = strings.Count(out, "event: response.output_item.added")
						}
						want := 1
						if scenario == "success" {
							want = 2
						}
						if w.Code != 200 || count != want {
							t.Fatalf("missing calls: status=%d body=%s", w.Code, out)
						}
						return
					}
					if strings.Contains(out, `"name":"Read"`) || strings.Contains(out, `"name":"Bash"`) || strings.Contains(out, `"name":"callFunction"`) || strings.Contains(out, "response.completed") || strings.Contains(out, "message_stop") {
						t.Fatalf("partial action or successful terminal emitted: %s", out)
					}
				})
			}
		}
	}
}
