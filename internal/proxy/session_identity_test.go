package proxy

import (
	"strings"
	"testing"
)

func TestFallbackSessionIdentityUsesCompleteStructuredHistory(t *testing.T) {
	base := []ChatMessage{{Role: "system", Content: strings.Repeat("s", 220) + "project-a"}, {Role: "user", Content: strings.Repeat("u", 220) + "task-a"}}
	variants := [][]ChatMessage{
		{{Role: "system", Content: strings.Repeat("s", 220) + "project-b"}, base[1]},
		{base[0], {Role: "user", Content: strings.Repeat("u", 220) + "task-b"}},
		{base[0], base[1], {Role: "assistant", Content: "branch-a"}},
		{base[0], base[1], {Role: "assistant", ToolCalls: []ToolCall{{ID: "call-a", Function: ToolCallFunction{Name: "Read", Arguments: `{"path":"a"}`}}}}},
	}
	fingerprint := computeSessionFingerprintForRequest(base, "", "claude-opus-4-6")
	for i, variant := range variants {
		if fingerprint == computeSessionFingerprintForRequest(variant, "", "claude-opus-4-6") {
			t.Errorf("different fallback history %d shares a session", i)
		}
	}
	if fingerprint != computeSessionFingerprintForRequest(cloneChatMessages(base), "", "claude-opus-4-6") {
		t.Fatal("same request must have deterministic identity")
	}
}

func TestFallbackSessionIdentityIncludesToolCallArguments(t *testing.T) {
	base := []ChatMessage{{Role: "user", Content: "Continue"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Function: ToolCallFunction{Name: "Read", Arguments: `{"file":"a"}`}}}}}
	other := cloneChatMessages(base)
	other[1].ToolCalls[0].Function.Arguments = `{"file":"b"}`
	if computeSessionFingerprintForRequest(base, "", "claude-opus-4-6") == computeSessionFingerprintForRequest(other, "", "claude-opus-4-6") {
		t.Fatal("different tool call histories share a session")
	}
}
