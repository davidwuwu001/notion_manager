package proxy

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseNDJSONStreamEmitsWorkflowProcessThinking(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"step1","value":[{"type":"thinking","content":"Let me search"}]}`,
		`{"type":"agent-inference","id":"step1","value":[{"type":"thinking","content":"Let me search for information about Notion Agent."},{"type":"tool_use","id":"toolu_1","name":"search","content":"{\"web\":{\"queries\":[\"What is Notion Agent"}]}`,
		`{"type":"agent-inference","id":"step1","value":[{"type":"thinking","content":"Let me search for information about Notion Agent."},{"type":"tool_use","id":"toolu_1","name":"search","content":"{\"web\":{\"queries\":[\"What is Notion Agent and what can you do with it?\"]}}"}],"finishedAt":1,"inputTokens":10,"outputTokens":2}`,
		`{"type":"agent-search-extracted-results","toolCallId":"toolu_1","results":[{"id":"webpage://?url=https%3A%2F%2Fexample.com%2Fagent","title":"How to work with your Agent"},{"id":"webpage://?url=https%3A%2F%2Fexample.com%2Fnotion-agent","title":"Notion Agent"}]}`,
		`{"type":"agent-inference","id":"step2","value":[{"type":"thinking","content":"Let me summarize"}]}`,
		`{"type":"agent-inference","id":"step2","value":[{"type":"thinking","content":"Let me summarize the search results."},{"type":"text","content":"Final answer text."}],"finishedAt":2,"inputTokens":20,"outputTokens":4}`,
	}, "\n")

	var thinking strings.Builder
	var text strings.Builder
	var events []string
	doneCount := 0

	err := parseNDJSONStream(bytes.NewBufferString(stream), "", func(delta string, done bool, usage *UsageInfo) {
		if delta != "" {
			events = append(events, "text")
			text.WriteString(delta)
		}
	}, nil, nil, func(delta string, done bool, signature string) {
		if done {
			doneCount++
			events = append(events, "thinking_done")
			return
		}
		if delta != "" {
			events = append(events, "thinking")
			thinking.WriteString(delta)
		}
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("parseNDJSONStream returned error: %v", err)
	}

	thinkingText := thinking.String()
	if !strings.Contains(thinkingText, "Let me search for information about Notion Agent.") {
		t.Fatalf("expected initial workflow thinking, got %q", thinkingText)
	}
	if !strings.Contains(thinkingText, "**Web Search**: What is Notion Agent and what can you do with it?") {
		t.Fatalf("expected search query summary, got %q", thinkingText)
	}
	if !strings.Contains(thinkingText, "**Searching**: What is Notion Agent and what can you do with it?") {
		t.Fatalf("expected search start status, got %q", thinkingText)
	}
	if !strings.Contains(thinkingText, "**Search Complete**: What is Notion Agent and what can you do with it?") {
		t.Fatalf("expected search completion status, got %q", thinkingText)
	}
	if !strings.Contains(thinkingText, "**Found 2 Results**") {
		t.Fatalf("expected result count summary, got %q", thinkingText)
	}
	if !strings.Contains(thinkingText, "How to work with your Agent") || !strings.Contains(thinkingText, "Notion Agent") {
		t.Fatalf("expected result titles in thinking output, got %q", thinkingText)
	}
	if !strings.Contains(thinkingText, "Let me summarize the search results.") {
		t.Fatalf("expected summarization thinking, got %q", thinkingText)
	}
	if text.String() != "Final answer text." {
		t.Fatalf("unexpected text output: %q", text.String())
	}
	if doneCount != 1 {
		t.Fatalf("expected exactly one thinking_done callback, got %d", doneCount)
	}

	doneIndex := -1
	textIndex := -1
	for i, event := range events {
		if event == "thinking_done" && doneIndex == -1 {
			doneIndex = i
		}
		if event == "text" && textIndex == -1 {
			textIndex = i
		}
	}
	if doneIndex == -1 || textIndex == -1 || doneIndex > textIndex {
		t.Fatalf("expected thinking_done before first text event, got events=%v", events)
	}
}

func TestParseNDJSONStreamTrimsIncompleteCitationRewrites(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude Sonnet 4.6**：2026 年 2 月 "}]}`,
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude Sonnet 4.6**：2026 年 2 月 17 日发布[^{{https://www.anthropic.com/news/claude-sonnet-4-6"}]}`,
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude Sonnet 4.6**：2026 年 2 月 17 日发布[^view://artifact-123]\n- **速度快**"}],"finishedAt":1,"inputTokens":1,"outputTokens":1}`,
	}, "\n")

	var got strings.Builder
	err := parseNDJSONStream(bytes.NewBufferString(stream), "", func(delta string, done bool, usage *UsageInfo) {
		if delta != "" {
			got.WriteString(delta)
		}
	}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parseNDJSONStream returned error: %v", err)
	}

	want := "- **Claude Sonnet 4.6**：2026 年 2 月 17 日发布[^https://www.anthropic.com/news/claude-sonnet-4-6]\n- **速度快**"
	if got.String() != want {
		t.Fatalf("unexpected parser output: got %q want %q", got.String(), want)
	}
}

func TestParseNDJSONStreamUsesPendingFragmentForFirstNewInternalCitation(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude 3 Sonnet**：2024年3月4日发布[^{{https://en.wikipedia.org/wiki/Claude_(language_model)}}]\n- **Claude 3.5 Sonnet**：2024年6月20日发布[^{{https://en.wikipedia.org/wiki/Claude_(language_model)}}]\n- **Claude Sonnet 4**：2025年5月22日发布[^{{https://www.anthropic.com/news/claude-4"}]}`,
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude 3 Sonnet**：2024年3月4日发布[^{{https://en.wikipedia.org/wiki/Claude_(language_model)}}]\n- **Claude 3.5 Sonnet**：2024年6月20日发布[^{{https://en.wikipedia.org/wiki/Claude_(language_model)}}]\n- **Claude Sonnet 4**：2025年5月22日发布[^view://artifact-123]\n- **Claude Sonnet 4.6**：2026年2月17日发布[^view://artifact-123]"}],"finishedAt":1,"inputTokens":1,"outputTokens":1}`,
	}, "\n")

	var got strings.Builder
	err := parseNDJSONStream(bytes.NewBufferString(stream), "", func(delta string, done bool, usage *UsageInfo) {
		if delta != "" {
			got.WriteString(delta)
		}
	}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parseNDJSONStream returned error: %v", err)
	}

	want := "- **Claude 3 Sonnet**：2024年3月4日发布[^https://en.wikipedia.org/wiki/Claude_(language_model)]\n" +
		"- **Claude 3.5 Sonnet**：2024年6月20日发布[^https://en.wikipedia.org/wiki/Claude_(language_model)]\n" +
		"- **Claude Sonnet 4**：2025年5月22日发布[^https://www.anthropic.com/news/claude-4]\n" +
		"- **Claude Sonnet 4.6**：2026年2月17日发布[^view://artifact-123]"
	if got.String() != want {
		t.Fatalf("unexpected parser output: got %q want %q", got.String(), want)
	}
}

func TestParseNDJSONStreamDoesNotRewriteToolCitationsFromObservedFragments(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude Sonnet 4.5**：2025年9月29日发布[^{{https://www.anthropic.com/news/claude-son"}]}`,
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude Sonnet 4.5**：2025年9月29日发布[^toolu_test123]\n- **Claude Sonnet 4.6**：2026年2月17日发布[^toolu_test123]"}],"finishedAt":1,"inputTokens":1,"outputTokens":1}`,
	}, "\n")

	var got strings.Builder
	err := parseNDJSONStream(bytes.NewBufferString(stream), "", func(delta string, done bool, usage *UsageInfo) {
		if delta != "" {
			got.WriteString(delta)
		}
	}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parseNDJSONStream returned error: %v", err)
	}

	want := "- **Claude Sonnet 4.5**：2025年9月29日发布[^toolu_test123]\n" +
		"- **Claude Sonnet 4.6**：2026年2月17日发布[^toolu_test123]"
	if got.String() != want {
		t.Fatalf("unexpected parser output: got %q want %q", got.String(), want)
	}
}

func TestParseNDJSONStreamKeepsInternalCitationWhenObservedHTTPPrefixIsAmbiguous(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude Sonnet 4**：2025年5月22日发布[^{{https://www.anthropic.com/news/claude"}]}`,
		`{"type":"agent-inference","id":"step1","value":[{"type":"text","content":"- **Claude Sonnet 4**：2025年5月22日发布[^view://artifact-123]"}],"finishedAt":1,"inputTokens":1,"outputTokens":1}`,
	}, "\n")

	known := []string{
		"https://www.anthropic.com/news/claude-3-family",
		"https://www.anthropic.com/news/claude-4",
		"https://www.anthropic.com/news/claude-sonnet-4-5",
		"https://www.anthropic.com/news/claude-sonnet-4-6",
	}

	var got strings.Builder
	err := parseNDJSONStream(bytes.NewBufferString(stream), "", func(delta string, done bool, usage *UsageInfo) {
		if delta != "" {
			got.WriteString(delta)
		}
	}, nil, nil, nil, &known, nil, nil)
	if err != nil {
		t.Fatalf("parseNDJSONStream returned error: %v", err)
	}

	want := "- **Claude Sonnet 4**：2025年5月22日发布[^view://artifact-123]"
	if got.String() != want {
		t.Fatalf("unexpected parser output: got %q want %q", got.String(), want)
	}
}
