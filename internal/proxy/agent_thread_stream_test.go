package proxy

import (
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func autoTranscriptLine(patch string) string {
	return `{"type":"agent_service_transcript_delta","patches":[` + patch + "]}\n"
}

func autoTextPut(source, id, text string) string {
	return autoTranscriptLine(fmt.Sprintf(`{"op":"put","entity":{"kind":"assistant_message","source":%q,"id":%q,"content":[{"type":"text","text":%s}]}}`, source, id, strconv.Quote(text)))
}

var autoCompleteLine = autoTranscriptLine(`{"op":"put","entity":{"kind":"turn_completed"}}`)

func TestAutoCommittedTextArrivesBeforeTurnCompletion(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	chunks := make(chan string, 8)
	result := make(chan error, 1)
	go func() {
		result <- parseAgentThreadStream(r, nil, func(delta string, done bool, _ *UsageInfo) {
			if done {
				chunks <- "<done>"
			} else {
				chunks <- delta
			}
		})
	}()
	write := func(s string) {
		t.Helper()
		if _, err := io.WriteString(w, s); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-chunks:
			if got != want {
				t.Fatalf("chunk %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout before chunk %q", want)
		}
	}

	write(autoTextPut("provisional", "a", "draft"))
	write(autoTextPut("committed", "a", "edited"))
	expect("edited")
	write(autoTextPut("committed", "a", "edited"))
	write(autoTextPut("committed", "a", "edited!"))
	expect("!")
	write(autoTextPut("committed", "b", "next"))
	expect("\n\nnext")
	write(autoCompleteLine)
	expect("<done>")
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestAutoNonterminalAndRevisedStreams(t *testing.T) {
	for _, tc := range []struct {
		name, stream, text string
		wantEOF, wantOK    bool
	}{
		{"committed rich user message", autoTranscriptLine(`{"op":"put","entity":{"kind":"user_message","source":"committed","text":[["question"]],"content":{"metadata":true}}}`) + autoTextPut("committed", "a", "answer") + autoCompleteLine, "answer", false, true},
		{"partial draft", autoTextPut("provisional", "p", "draft"), "", true, false},
		{"partial committed", autoTextPut("committed", "c", "first"), "first", true, false},
		{"malformed transcript", autoTextPut("committed", "c", "first") + "{broken\n" + autoCompleteLine, "first", false, false},
		{"rewrite after commit", autoTextPut("committed", "c", "first") + autoTextPut("committed", "c", "other") + autoCompleteLine, "first", false, false},
		{"private patch", autoTranscriptLine(`{"op":"patch","id":"other","ops":[{"op":"append","path":"/content/0/text","value":"hidden"}]}`) + autoTextPut("provisional", "a", "ok") + autoCompleteLine, "ok", false, true},
		{"block revision", autoTranscriptLine(`{"op":"put","entity":{"kind":"assistant_message","source":"provisional","id":"a","content":[{"type":"text","text":"first "},{"type":"text","text":"draft"}]}}`) + autoTranscriptLine(`{"op":"patch","id":"a","ops":[{"op":"replace","path":"/content/1/text","value":"corrected"},{"op":"append","path":"/content/1/text","value":"!"}]}`) + autoCompleteLine, "first corrected!", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			doneCalls := 0
			err := parseAgentThreadStream(strings.NewReader(tc.stream), nil, func(delta string, done bool, _ *UsageInfo) {
				out.WriteString(delta)
				if done {
					doneCalls++
				}
			})
			if (err == nil) != tc.wantOK || errors.Is(err, io.ErrUnexpectedEOF) != tc.wantEOF || out.String() != tc.text || (doneCalls == 1) != tc.wantOK {
				t.Fatalf("err=%v text=%q done=%d", err, out.String(), doneCalls)
			}
		})
	}
}

func TestAutoTruncatedTranscriptAcrossProtocols(t *testing.T) {
	for _, tc := range []struct{ path, input, tools string }{
		{"/v1/messages", `"messages":[{"role":"user","content":"hello"}]`, `[{"name":"Read","input_schema":{"type":"object"}}]`},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`, `[{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}]`},
		{"/v1/responses", `"input":"hello"`, `[{"type":"function","name":"Read","parameters":{"type":"object"}}]`},
	} {
		for _, hasTools := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/tools-%t", tc.path, hasTools), func(t *testing.T) {
				f := newUpgradeProtocolFixture(t, 1)
				text := "partial"
				toolParam := ""
				if hasTools {
					text = `{"name":"Read","arguments":{"file_path":"`
					toolParam = `,"tools":` + tc.tools
				}
				inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
					return parseAgentThreadStream(strings.NewReader(autoTextPut("committed", "c", text)), &opts[0], cb)
				}
				request := fmt.Sprintf(`{"model":"auto","max_tokens":32,"stream":true,%s%s}`, tc.input, toolParam)
				r := httptest.NewRequest("POST", tc.path, strings.NewReader(request))
				w := httptest.NewRecorder()
				RequestLogMiddleware(f.store, handlerForPath(tc.path, f.pool)).ServeHTTP(w, r)
				f.waitForIdle(t)
				out := w.Body.String()
				for _, bad := range []string{"message_stop", "response.completed", `"finish_reason":"stop"`, `"name":"Read"`} {
					if strings.Contains(out, bad) {
						t.Fatalf("unexpected %s after truncation: %s", bad, out)
					}
				}
				if !strings.Contains(out, "error") && !strings.Contains(out, "response.failed") {
					t.Fatalf("missing failure: %s", out)
				}
				if !hasTools && !strings.Contains(out, text) {
					t.Fatalf("missing delivered prefix: %s", out)
				}
			})
		}
	}
}
