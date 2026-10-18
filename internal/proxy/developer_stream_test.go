package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolStreamEmitsConfirmedCallBeforeInferenceReturns(t *testing.T) {
	originalConfig, originalCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig, inferenceCaller = originalConfig, originalCaller })
	recorder := httptest.NewRecorder()
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		cb(`{"name":"Read","arguments":{"file_path":"`, false, nil)
		if strings.Contains(recorder.Body.String(), "tool_use") {
			t.Fatal("incomplete JSON emitted a tool action")
		}
		cb(`a.ts"}}`, false, nil)
		if !strings.Contains(recorder.Body.String(), "input_json_delta") || !recorder.Flushed {
			t.Fatal("complete tool action was buffered until inference ended")
		}
		cb("\n"+`{"name":"Read","arguments":{"file_path":"b.ts"}}`, false, nil)
		cb("", true, &UsageInfo{PromptTokens: 123, CompletionTokens: 7})
		return nil
	}
	err := handleAnthropicStream(recorder, &Account{}, nil, "sonnet-4.6", "msg_fixture", true, true, false, nil, true, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(recorder.Body.String(), `"type":"tool_use"`); got != 2 {
		t.Fatalf("tool actions duplicated or missing: %d", got)
	}
	if !strings.Contains(recorder.Body.String(), "message_stop") {
		t.Fatal("missing successful terminal event")
	}
	if !strings.Contains(recorder.Body.String(), `"input_tokens":123`) {
		t.Fatal("final input usage lost")
	}
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			w := httptest.NewRecorder()
			var handle func(anthropicSSEFrame) error
			if protocol == "chat" {
				handle = newOpenAIChatStreamTranscoder(w, w, "chat_fixture", "sonnet-4.6", 1, true).HandleFrame
			} else {
				handle = newOpenAIResponsesStreamTranscoder(w, w, "resp_fixture", "sonnet-4.6", 1).HandleFrame
			}
			for _, chunk := range strings.Split(recorder.Body.String(), "\n\n") {
				if chunk == "" {
					continue
				}
				var frame anthropicSSEFrame
				for _, line := range strings.Split(chunk, "\n") {
					if strings.HasPrefix(line, "event: ") {
						frame.Event = strings.TrimPrefix(line, "event: ")
					}
					if strings.HasPrefix(line, "data: ") {
						frame.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
					}
				}
				if err := handle(frame); err != nil {
					t.Fatal(err)
				}
			}
			key := `"prompt_tokens":123`
			if protocol == "responses" {
				key = `"input_tokens":123`
			}
			if !strings.Contains(w.Body.String(), key) {
				t.Fatal("transcoder lost final input usage: " + w.Body.String())
			}
		})
	}
}

func TestToolStreamThinkingAndPartialFailure(t *testing.T) {
	originalConfig, originalCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig, inferenceCaller = originalConfig, originalCaller })
	recorder := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &inferenceWriter{ResponseWriter: recorder, ctx: ctx, cancel: cancel}
	want := errors.New("upstream interrupted")
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
		if opts[0].ThinkingCallback == nil {
			t.Fatal("thinking is not streamed")
		}
		opts[0].ThinkingCallback("inspect the test", false, "")
		if !strings.Contains(recorder.Body.String(), "thinking_delta") {
			t.Fatal("thinking was buffered")
		}
		opts[0].ThinkingCallback("", true, "signature")
		cb(`{"name":"Read","arguments":{"file_path":"a.ts"}}`, false, nil)
		return want
	}
	err := handleAnthropicStream(writer, &Account{}, nil, "sonnet-4.6", "msg_fixture", true, true, false, nil, true, nil, nil, nil)
	if !errors.Is(err, want) {
		t.Fatalf("error lost: %v", err)
	}
	if !writer.committed || canReplayInference(err, writer.committed, false, true) {
		t.Fatal("partial stream permits replay")
	}
	if strings.Contains(recorder.Body.String(), "message_stop") {
		t.Fatal("failed stream emitted success")
	}
}

func TestSessionMetadataDoesNotTreatUsersOrCacheAsConversations(t *testing.T) {
	for _, metadata := range []map[string]interface{}{
		{"user_id": "shared-user"}, {"prompt_cache_key": "shared-cache"},
	} {
		if got := extractAnthropicSessionSalt(metadata); got != "" {
			t.Fatalf("non-session metadata bound a thread: %q", got)
		}
	}
	if got := extractAnthropicSessionSalt(map[string]interface{}{"user_id": `{"session_id":"explicit-session"}`}); got != "explicit-session" {
		t.Fatal(got)
	}
}

func TestDebugLogsContainMetadataWithoutContent(t *testing.T) {
	originalConfig, originalWriter, debug := AppConfig, log.Writer(), DebugLoggingEnabled()
	AppConfig = DefaultConfig()
	SetDebugLoggingEnabled(true)
	var captured bytes.Buffer
	log.SetOutput(&captured)
	t.Cleanup(func() { AppConfig = originalConfig; log.SetOutput(originalWriter); SetDebugLoggingEnabled(debug) })
	const secret = "fixture-sensitive-content"
	logAnthropicRequest(AnthropicRequest{System: secret, Messages: []AnthropicMessage{{Role: "user", Content: secret}}, Tools: []AnthropicTool{{Name: "Read", Description: secret}}}, "sonnet-4.6")
	logConvertedMessages([]ChatMessage{{Role: "tool", Content: secret}})
	prepareToolBridgeResponse(`{"name":"__done__","arguments":{"result":"`+secret+`"}}`, nil)
	if strings.Contains(captured.String(), secret) {
		t.Fatal("debug metadata leaked content")
	}
	if captured.Len() == 0 {
		t.Fatal("debug metadata unexpectedly empty")
	}
}
