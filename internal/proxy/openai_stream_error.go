package proxy

import "net/http"

func writeOpenAIStreamError(w http.ResponseWriter, flusher http.Flusher, message string, responses bool) {
	payload := map[string]interface{}{"error": map[string]string{"message": message, "type": "api_error"}}
	if responses {
		payload["type"] = "response.failed"
		payload["response"] = map[string]interface{}{"status": "failed", "error": payload["error"]}
		_ = sendOpenAISSEEvent(w, flusher, "response.failed", payload)
		return
	}
	_ = sendOpenAISSE(w, flusher, payload)
}
