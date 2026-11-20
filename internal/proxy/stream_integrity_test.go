package proxy

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNDJSONRewriteOfEmittedTextFailsRatherThanAppendingContradiction(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"你好世界"}]}`,
		`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"你好地球"}],"finishedAt":1}`,
	}, "\n")
	var text strings.Builder
	finished := false
	err := parseNDJSONStream(strings.NewReader(stream), "", func(delta string, done bool, _ *UsageInfo) {
		text.WriteString(delta)
		finished = finished || done
	}, nil, nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("rewriting committed text must fail")
	}
	if text.String() != "你好世界" {
		t.Fatalf("corrupted text delivered: %q", text.String())
	}
	if finished {
		t.Fatal("failure emitted a successful terminal callback")
	}
}

func TestRewrittenStreamDoesNotEmitSuccessfulProtocolTerminal(t *testing.T) {
	originalConfig, originalCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig, inferenceCaller = originalConfig, originalCaller })
	inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		return parseNDJSONStream(strings.NewReader(`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"hello"}]}`+"\n"+`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"help"}],"finishedAt":1}`), "", cb, nil, nil, nil, nil, nil, nil)
	}
	w := httptest.NewRecorder()
	err := handleAnthropicStream(w, &Account{}, nil, "sonnet-4.6", "msg_fixture", false, false, false, nil, true, nil, nil, nil)
	if err == nil || strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("rewrite falsely succeeded: error=%v", err)
	}
	if strings.Contains(w.Body.String(), `"text":"p"`) {
		t.Fatal("contradictory suffix emitted")
	}
}

func TestNDJSONErrorDoesNotExposeUpstreamMessage(t *testing.T) {
	const secret = "fixture-secret-token"
	previousWriter, previousSetting := log.Writer(), NotionResponseLoggingEnabled()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	SetNotionResponseLoggingEnabled(true)
	t.Cleanup(func() { log.SetOutput(previousWriter); SetNotionResponseLoggingEnabled(previousSetting) })
	err := parseNDJSONStream(strings.NewReader(`{"type":"error","message":"`+secret+`"}`), "", func(string, bool, *UsageInfo) {}, nil, nil, nil, nil, nil, nil)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("upstream error must be classified without sensitive body: %v", err)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatal("upstream error leaked in diagnostic logs")
	}
}

func TestNativeToolUseUpdatesArgumentsWithoutDuplicatingCall(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"s","value":[{"type":"tool_use","id":"tool-1","name":"Read","input":{}}]}`,
		`{"type":"agent-inference","id":"s","value":[{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"fixture.txt"}}],"finishedAt":1}`,
	}, "\n")
	var calls []AgentValueEntry
	err := parseNDJSONStream(strings.NewReader(stream), "", func(string, bool, *UsageInfo) {}, &calls, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || string(calls[0].Input) != `{"path":"fixture.txt"}` {
		t.Fatalf("native call was duplicated or stale: %#v", calls)
	}
}

func TestNativeToolUseRejectsConflictingNameForSameID(t *testing.T) {
	stream := `{"type":"agent-inference","id":"s","value":[{"type":"tool_use","id":"tool-1","name":"Read","input":{}}]}` + "\n" + `{"type":"agent-inference","id":"s","value":[{"type":"tool_use","id":"tool-1","name":"Write","input":{}}],"finishedAt":1}`
	var calls []AgentValueEntry
	finished := false
	err := parseNDJSONStream(strings.NewReader(stream), "", func(_ string, done bool, _ *UsageInfo) { finished = finished || done }, &calls, nil, nil, nil, nil, nil)
	if err == nil || finished {
		t.Fatal("conflicting tool ID falsely succeeded")
	}
}

func TestUnexpectedCleanEOFDoesNotProduceSuccessfulCompletion(t *testing.T) {
	fixtures := []string{
		`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"partial"}]}`,
		`{"type":"patch-start","data":{"s":[]}}` + "\n" + `{"type":"patch","v":[{"o":"a","p":"/s/-","v":{"type":"agent-inference","value":[{"type":"text","content":"partial"}]}},{"o":"a","p":"/s/0/outputTokens","v":1}]}`,
		`{"type":"patch-start","data":{"s":[]}}` + "\n" + `{"type":"patch-sync","version":1}` + "\n" + `{"type":"patch","v":[{"o":"a","p":"/s/-","v":{"type":"agent-inference","value":[{"type":"text","content":"partial"}]}}]}`,
		`{"type":"patch-start","data":{"s":[{"type":"agent-inference","value":[{"type":"text","content":""}]}]}}` + "\n" + `{"type":"patch","v":[{"o":"x","p":"/s/0/value/0/content","v":"partial"}]}`,
		`{"type":"patch-start","data":{"s":[]}}` + "\n" + `{"type":"patch","v":[{"o":"a","p":"/s/-","v":{"type":"agent-inference","value":[{"type":"text","content":"partial"}]}},{"o":"a","p":"/s/0/finishedAt","v":null}]}`,
		`{"type":"patch-start","data":{"s":[]}}` + "\n" + `{"type":"patch","v":[{"o":"a","p":"/s/-","v":{"type":"agent-inference","value":[{"type":"text","content":"partial"}]}},{"o":"r","p":"/s/0/finishedAt","v":"invalid"}]}`,
		`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"partial"}]}` + "\n" + `{"type":"patch-sync","version":1}`,
		`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"partial"}],"finishedAt":1}` + "\n" + `{"type":"agent-inference","id":"next"`,
		`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"partial"}],"finishedAt":1}` + "\n" + `{"type":"agent-inference","id":"next","finishedAt":"invalid","value":[]}`,
		`{"type":"agent-inference","id":"s","value":[{"type":"text","content":"partial"}],"finishedAt":1}` + "\n" + `{"type":"patch","v":"invalid"}`,
	}
	for i, stream := range fixtures {
		originalConfig, originalCaller := AppConfig, inferenceCaller
		AppConfig = DefaultConfig()
		inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
			return parseNDJSONStream(strings.NewReader(stream), "", cb, nil, nil, nil, nil, nil, nil)
		}
		w := httptest.NewRecorder()
		err := handleAnthropicStream(w, &Account{}, nil, "sonnet-4.6", "msg_fixture", false, false, false, nil, true, nil, nil, nil)
		AppConfig, inferenceCaller = originalConfig, originalCaller
		if err == nil || strings.Contains(w.Body.String(), "message_stop") {
			t.Errorf("fixture %d: truncated stream was reported as successful: %v", i, err)
		}
	}
}

func TestUnknownNDJSONEventRemainsCompatible(t *testing.T) {
	stream := `{"type":"agent-inference","id":"s","value":[{"type":"text","content":"ok"}],"finishedAt":1}` + "\n" + `{"type":"future-event","v":"arbitrary"}`
	var output strings.Builder
	err := parseNDJSONStream(strings.NewReader(stream), "", func(delta string, _ bool, _ *UsageInfo) { output.WriteString(delta) }, nil, nil, nil, nil, nil, nil)
	if err != nil || output.String() != "ok" {
		t.Fatalf("unknown event must remain ignorable: %v", err)
	}
}

func TestPatchStartInferenceCompletionStillSucceeds(t *testing.T) {
	for _, terminal := range []string{
		`{"type":"patch","v":[{"o":"a","p":"/s/0/finishedAt","v":2}]}`,
		`{"type":"patch-sync","version":1}`,
		`{"type":"patch","v":[{"o":"a","p":"/s/0/finishedAt","v":null},{"o":"r","p":"/s/0/finishedAt","v":2}]}`,
	} {
		stream := `{"type":"patch-start","data":{"s":[{"type":"agent-inference","value":[{"type":"text","content":""}]}]}}` + "\n" + `{"type":"patch","v":[{"o":"x","p":"/s/0/value/0/content","v":"完整"}]}` + "\n" + terminal
		var output strings.Builder
		err := parseNDJSONStream(strings.NewReader(stream), "", func(delta string, _ bool, _ *UsageInfo) { output.WriteString(delta) }, nil, nil, nil, nil, nil, nil)
		if err != nil || output.String() != "完整" {
			t.Errorf("valid patch stream failed: %v, text=%q", err, output.String())
		}
	}
}

func TestPatchStartEmitsOnlyCurrentOpenStepInitialText(t *testing.T) {
	stream := `{"type":"patch-start","data":{"s":[{"type":"agent-inference","value":[{"type":"text","content":"history"}],"finishedAt":1},{"type":"agent-inference","value":[{"type":"text","content":"hello"}]}]}}` + "\n" + `{"type":"patch","v":[{"o":"x","p":"/s/1/value/0/content","v":" world"},{"o":"a","p":"/s/1/finishedAt","v":2}]}`
	var output strings.Builder
	err := parseNDJSONStream(strings.NewReader(stream), "", func(delta string, _ bool, _ *UsageInfo) { output.WriteString(delta) }, nil, nil, nil, nil, nil, nil)
	if err != nil || output.String() != "hello world" {
		t.Fatalf("wrong text %q, error=%v", output.String(), err)
	}
}

func TestCompletedInferenceStepsAppendWithoutConfusingStepRevisions(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"agent-inference","id":"one","value":[{"type":"text","content":"hello"}],"finishedAt":1}`,
		`{"type":"agent-inference","id":"two","value":[{"type":"text","content":"wor"}]}`,
		`{"type":"agent-inference","id":"two","value":[{"type":"text","content":"world"}],"finishedAt":2}`,
	}, "\n")
	var output strings.Builder
	err := parseNDJSONStream(strings.NewReader(stream), "", func(delta string, _ bool, _ *UsageInfo) { output.WriteString(delta) }, nil, nil, nil, nil, nil, nil)
	if err != nil || output.String() != "helloworld" {
		t.Fatalf("wrong text %q, error=%v", output.String(), err)
	}
}
