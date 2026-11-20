package proxy

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAutoHonorsSingleToolPerTurn(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
		input := `"messages":[{"role":"user","content":"Read both files."}]`
		function := `{"name":"Read","parameters":{"type":"object"}}`
		tool := `{"type":"function","function":` + function + `}`
		flag := `"parallel_tool_calls":false`
		if path == "/v1/responses" {
			input = `"input":"Read both files."`
			tool = `{"type":"function","name":"Read","parameters":{"type":"object"}}`
		}
		if path == "/v1/messages" {
			tool = `{"name":"Read","input_schema":{"type":"object"}}`
			flag = `"tool_choice":{"type":"auto","disable_parallel_tool_use":true}`
		}
		for _, stream := range []bool{false, true} {
			for _, count := range []int{1, 2} {
				t.Run(fmt.Sprintf("%s/stream=%t/calls=%d", path, stream, count), func(t *testing.T) {
					fixture := newUpgradeProtocolFixture(t, 1)
					inferenceCaller = func(_ *Account, messages []ChatMessage, model string, _ bool, cb StreamCallback, _ ...CallOptions) error {
						call := `{"name":"Read","arguments":{"path":"one"}}`
						if count == 2 {
							call += "\n" + `{"name":"Read","arguments":{"path":"two"}}`
						}
						cb(call, true, nil)
						return nil
					}
					body := fmt.Sprintf(`{"model":"auto","stream":%t,%s,"tools":[%s],%s}`, stream, input, tool, flag)
					w := httptest.NewRecorder()
					RequestLogMiddleware(fixture.store, handlerForPath(path, fixture.pool)).ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
					fixture.waitForIdle(t)
					gotCall := strings.Contains(w.Body.String(), `"name":"Read"`)
					if gotCall != (count == 1) || (count == 1 && w.Code != 200) || (count == 2 && !strings.Contains(w.Body.String(), "error")) {
						t.Fatalf("status %d, body %s", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}
