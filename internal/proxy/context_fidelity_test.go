package proxy

import (
	"reflect"
	"strings"
	"testing"
)

func fidelityTools() []Tool {
	var tools []Tool
	for _, name := range []string{"Read", "Write", "Edit", "Bash", "Grep", "mcp_custom_lookup"} {
		tools = append(tools, Tool{Type: "function", Function: ToolFunction{
			Name: name, Description: "Use the complete schema and preserve all output.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"mode": map[string]interface{}{"type": "string", "enum": []string{"precise", "complete"}},
			}},
		}})
	}
	return tools
}

func TestClientWrappersPreserveDocumentAndInstructions(t *testing.T) {
	input := "<system-reminder>Never modify production.</system-reminder>\n<title>登录页</title>\n<custom-data>keep</custom-data>"
	got := stripSystemReminders(input)
	for _, want := range []string{"Never modify production.", "<title>登录页</title>", "<custom-data>keep</custom-data>"} {
		if !strings.Contains(got, want) {
			t.Errorf("client wrapper cleaning removed %q", want)
		}
	}
}

func TestConfiguredClaudeModelIDKeepsToolBridge(t *testing.T) {
	id, ok := GetModelID("opus-4.6")
	if !ok {
		t.Fatal("fixture requires the configured Opus model")
	}
	if detectModelFamily(id) != familyAnthropic {
		t.Fatalf("configured Opus ID %q was not recognized as Claude", id)
	}
	got := injectToolsIntoMessages([]ChatMessage{{Role: "user", Content: "Read the test."}}, fidelityTools(), id, nil)
	if len(got) != 1 || !strings.Contains(got[0].Content, "Available functions:") {
		t.Fatal("configured Claude model ID silently skipped tool injection")
	}
}

func TestLargeToolBridgePreservesSystemToolsAndInput(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "Repository rule: never remove validation."},
		{Role: "system", Content: "Project constraint: keep the API stable."},
		{Role: "user", Content: "Inspect <title>登录页</title> and use mcp_custom_lookup."},
	}
	before := cloneChatMessages(messages)
	got := injectToolsIntoMessages(messages, fidelityTools(), "claude-opus-4-6", nil)
	var body strings.Builder
	for _, m := range got {
		body.WriteString(m.Content)
	}
	for _, want := range []string{messages[0].Content, messages[1].Content, "<title>登录页</title>", "mcp_custom_lookup", `"enum":["precise","complete"]`} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("large tool bridge lost %q", want)
		}
	}
	if !reflect.DeepEqual(messages, before) {
		t.Fatal("tool bridge changed the caller's messages")
	}
}

func TestToolBridgePreservesLongResultsWithAndWithoutSession(t *testing.T) {
	old := strings.Repeat("旧结果", 2000) + "OLD-RESULT-END"
	latest := strings.Repeat("新结果", 2000) + "<title>result</title>NEW-RESULT-END"
	messages := []ChatMessage{
		{Role: "system", Content: "Keep every test assertion."},
		{Role: "user", Content: "Inspect the repository."},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "old", Function: ToolCallFunction{Name: "Read", Arguments: `{"file":"old"}`}}}},
		{Role: "tool", ToolCallID: "old", Name: "Read", Content: old},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "new", Function: ToolCallFunction{Name: "Read", Arguments: `{"file":"new"}`}}}},
		{Role: "tool", ToolCallID: "new", Name: "Read", Content: latest},
	}
	for _, session := range []*Session{nil, {TurnCount: 2}} {
		got := injectToolsIntoMessages(cloneChatMessages(messages), fidelityTools(), "claude-opus-4-6", session)
		var body strings.Builder
		for _, m := range got {
			body.WriteString(m.Content)
		}
		if !strings.Contains(body.String(), latest) {
			t.Errorf("latest tool result truncated (has session: %v)", session != nil)
		}
		if session == nil && (!strings.Contains(body.String(), old) || !strings.Contains(body.String(), "Keep every test assertion.")) {
			t.Fatal("fresh tool chain lost earlier results or system instructions")
		}
	}
}

func TestRecoveryPreservesCompleteHistory(t *testing.T) {
	first := strings.Repeat("first", 1000) + "FIRST-END"
	answer := strings.Repeat("answer", 1000) + "ANSWER-END"
	tool := strings.Repeat("工具", 1000) + "TOOL-END"
	messages := []ChatMessage{
		{Role: "user", Content: first},
		{Role: "assistant", Content: answer},
		{Role: "tool", Content: tool, Name: "Read"},
		{Role: "user", Content: "Continue."},
	}
	got := buildFreshThreadRecoveryMessages(messages)
	for _, want := range []string{first, answer, tool} {
		if !strings.Contains(got[0].Content, want) {
			t.Errorf("recovery truncated a %d-byte history entry", len(want))
		}
	}
}

func TestForcedToolChoicePreservesCompleteSchema(t *testing.T) {
	for _, model := range []string{"claude-opus-4-6", "claude-haiku-4-5"} {
		for _, count := range []int{1, 6} {
			tools := fidelityTools()[:count]
			got := injectToolsIntoMessages([]ChatMessage{{Role: "user", Content: "Read the file."}}, tools, model, nil,
				map[string]interface{}{"type": "tool", "name": "Read"})
			if len(got) != 1 || !strings.Contains(got[0].Content, `"enum":["precise","complete"]`) {
				t.Errorf("forced Read lost its parameter schema (model %s, tool count %d)", model, count)
			}
		}
	}
}

func TestSmallToolContinuationPreservesAssistantContentAndArguments(t *testing.T) {
	const explanation = "Do not remove validation. The failing test demonstrates a boundary condition."
	const arguments = `{"path":"src/check.go","offset":123}`
	messages := []ChatMessage{
		{Role: "user", Content: "Fix the test."},
		{Role: "assistant", Content: explanation, ToolCalls: []ToolCall{{
			ID: "read-1", Function: ToolCallFunction{Name: "Read", Arguments: arguments},
		}}},
		{Role: "tool", Name: "Read", ToolCallID: "read-1", Content: "Source code and test evidence."},
	}
	got := injectToolsIntoMessages(messages, fidelityTools()[:1], "claude-opus-4-6", nil)
	var content strings.Builder
	for _, m := range got {
		content.WriteString(m.Content)
	}
	for _, want := range []string{explanation, arguments, messages[2].Content} {
		if !strings.Contains(content.String(), want) {
			t.Errorf("small tool continuation lost %q", want)
		}
	}
}
