package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
)

// toolBridgeStream emits only confirmed JSON calls; ambiguous prose/fences use the final parser.
type toolBridgeStream struct {
	w                  http.ResponseWriter
	flusher            http.Flusher
	id, model, pending string
	started, thinking  bool
	thinkingEmitted    bool
	index              int
	calls              []ToolCall
}

func (s *toolBridgeStream) start() {
	if s.started {
		return
	}
	s.started = true
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
	sendAnthropicSSE(s.w, s.flusher, "message_start", map[string]interface{}{
		"type": "message_start", "message": map[string]interface{}{
			"id": s.id, "type": "message", "role": "assistant", "content": []interface{}{}, "model": s.model,
			"stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

func (s *toolBridgeStream) closeThinking(signature string) {
	if !s.thinking {
		return
	}
	if signature == "" {
		signature = generateFakeSignature()
	}
	sendAnthropicSSE(s.w, s.flusher, "content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": s.index, "delta": map[string]interface{}{"type": "signature_delta", "signature": signature},
	})
	sendAnthropicSSE(s.w, s.flusher, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": s.index})
	s.index++
	s.thinking = false
}

func (s *toolBridgeStream) think(delta string, done bool, signature string) {
	if done {
		s.closeThinking(signature)
		return
	}
	if delta == "" {
		return
	}
	s.thinkingEmitted = true
	s.start()
	if !s.thinking {
		sendAnthropicSSE(s.w, s.flusher, "content_block_start", map[string]interface{}{
			"type": "content_block_start", "index": s.index, "content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
		})
		s.thinking = true
	}
	sendAnthropicSSE(s.w, s.flusher, "content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": s.index, "delta": map[string]interface{}{"type": "thinking_delta", "thinking": delta},
	})
}

func (s *toolBridgeStream) feed(delta string) {
	s.pending += delta
	for {
		input := strings.TrimSpace(s.pending)
		if !strings.HasPrefix(input, "{") {
			return
		}
		decoder := json.NewDecoder(strings.NewReader(input))
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return
		}
		calls, remaining, ok := parseToolCalls(string(raw))
		if !ok || remaining != "" || len(calls) != 1 {
			return
		}
		call := calls[0]
		if call.Function.Name == "__done__" || call.Function.Name == "WebSearch" || call.Function.Name == "" {
			return
		}
		s.start()
		s.closeThinking("")
		sendAnthropicSSE(s.w, s.flusher, "content_block_start", map[string]interface{}{
			"type": "content_block_start", "index": s.index, "content_block": map[string]interface{}{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": map[string]interface{}{}},
		})
		sendAnthropicSSE(s.w, s.flusher, "content_block_delta", map[string]interface{}{
			"type": "content_block_delta", "index": s.index, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": call.Function.Arguments},
		})
		sendAnthropicSSE(s.w, s.flusher, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": s.index})
		s.index++
		s.calls = append(s.calls, call)
		s.pending = input[int(decoder.InputOffset()):]
	}
}
