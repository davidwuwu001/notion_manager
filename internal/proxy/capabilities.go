package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
)

var unenforcedParameters = []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p", "top_k", "stop", "stop_sequences", "seed", "presence_penalty", "frequency_penalty", "parallel_tool_calls"}

func setBridgeCompatibilityHeaders(w http.ResponseWriter, body []byte) {
	w.Header().Set("X-Notion-Capabilities", "/v1/capabilities")
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return
	}
	var ignored []string
	for _, name := range unenforcedParameters {
		if value, ok := fields[name]; ok && string(value) != "null" {
			ignored = append(ignored, name)
		}
	}
	if len(ignored) > 0 {
		w.Header().Set("X-Notion-Ignored-Parameters", strings.Join(ignored, ", "))
	}
}

// HandleCapabilities describes bridge behavior without promising native provider semantics.
func HandleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "proxy.capabilities", "protocols": []string{"anthropic_messages", "openai_chat", "openai_responses"},
		"accepted_but_not_enforced": unenforcedParameters,
		"custom_tools":              map[string]interface{}{"mode": "text_bridge", "model_family": "anthropic", "native_protocol": false, "incremental": "thinking and complete direct JSON calls; other formats buffered"},
		"structured_output":         "prompt_bridge_not_schema_enforced", "previous_response_id": false,
		"session_identity": "metadata.session_id or metadata.conversation_id; user_id JSON with explicit session_id; user/cache keys alone are not conversations",
		"quota":            "per-workspace polling; source freshness and last-good cache are independent",
	})
}
