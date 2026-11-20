package proxy

import (
	"encoding/json"
	"testing"
)

func TestMixedToolRepresentationDoesNotRoundLargeNumbers(t *testing.T) {
	native := []AgentValueEntry{{Type: "tool_use", Name: "Read", Input: json.RawMessage(`{"value":9007199254740992}`)}}
	if mixedToolRepresentationsAgree(`{"name":"Read","arguments":{"value":9007199254740993}}`, native) {
		t.Fatal("distinct integers were rounded to the same value")
	}
	if !mixedToolRepresentationsAgree(`{"name":"Read","arguments":{"value":9007199254740992}}`, native) {
		t.Fatal("identical values rejected")
	}
}

func TestMixedToolRepresentationPreservesOrdinaryJSON(t *testing.T) {
	native := []AgentValueEntry{{Type: "tool_use", Name: "Read", Input: json.RawMessage(`{"path":"fixture"}`)}}
	for _, text := range []string{`{"name":"Read","title":"report"}`, `{"name":"Alice","occupation":"engineer"}`, `{"name":"Read","tool_call":{"arguments":{}}}`} {
		if !mixedToolRepresentationsAgree(text, native) {
			t.Fatalf("ordinary JSON rejected: %s", text)
		}
	}
}

func TestMixedToolRepresentationRejectsDuplicateKeys(t *testing.T) {
	for _, tc := range []struct{ text, input string }{
		{`{"name":"Read","arguments":{"path":"first","path":"last"}}`, `{"path":"last"}`},
		{`{"name":"Read","arguments":{"nested":{"path":"first","path":"last"}}}`, `{"nested":{"path":"last"}}`},
		{`{"name":"Read","arguments":{"path":"last"}}`, `{"path":"first","path":"last"}`},
		{`{"name":"Bash","name":"Read","arguments":{"path":"last"}}`, `{"path":"last"}`},
		{`{"tool_call":{"name":"Bash","name":"Read","arguments":{"path":"last"}}}`, `{"path":"last"}`},
		{`{"name":"Read","arguments":{"path":"first"},"arguments":{"path":"last"}}`, `{"path":"last"}`},
	} {
		native := []AgentValueEntry{{Type: "tool_use", Name: "Read", Input: json.RawMessage(tc.input)}}
		if mixedToolRepresentationsAgree(tc.text, native) {
			t.Fatal("duplicate JSON keys accepted as equivalent")
		}
	}
}
