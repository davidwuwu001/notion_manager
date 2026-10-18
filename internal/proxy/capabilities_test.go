package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompatibilityDeclaresIgnoredParametersWithoutValues(t *testing.T) {
	w := httptest.NewRecorder()
	setBridgeCompatibilityHeaders(w, []byte(`{"max_tokens":100,"temperature":0.4,"top_p":null,"messages":[{"content":"SECRET"}]}`))
	if got := w.Header().Get("X-Notion-Ignored-Parameters"); got != "max_tokens, temperature" {
		t.Fatal(got)
	}
	if strings.Contains(w.Header().Get("X-Notion-Ignored-Parameters"), "SECRET") {
		t.Fatal("parameter values leaked")
	}
	HandleCapabilities(w, httptest.NewRequest("GET", "/v1/capabilities", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["previous_response_id"] != false || body["accepted_but_not_enforced"] == nil {
		t.Fatal(body)
	}
}

func TestNonClaudeCustomToolsAreRejectedBeforeInference(t *testing.T) {
	original := AppConfig
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig = original })
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"Bash","input_schema":{"type":"object"}}]}`))
	HandleAnthropicMessages(NewAccountPool())(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "custom tool bridge") {
		t.Fatalf("unsupported tools silently accepted: %d %s", w.Code, w.Body.String())
	}
}
