package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpgradeReviewWebSearchFailureCannotCompleteRequest(t *testing.T) {
	originalConfig, originalCaller := AppConfig, inferenceCaller
	AppConfig = DefaultConfig()
	t.Cleanup(func() { AppConfig, inferenceCaller = originalConfig, originalCaller })
	want := errors.New("search was interrupted")
	for _, streaming := range []bool{true, false} {
		name := "nonstream"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			inferenceCaller = func(_ *Account, _ []ChatMessage, _ string, _ bool, cb StreamCallback, opts ...CallOptions) error {
				calls++
				if len(opts) != 1 || opts[0].Context != ctx {
					t.Fatal("search call lost the inbound request context")
				}
				if calls == 1 {
					if opts[0].NativeToolUses == nil {
						t.Fatal("tool request did not collect native calls")
					}
					*opts[0].NativeToolUses = []AgentValueEntry{{Type: "tool_use", ID: "search", Name: "WebSearch", Input: json.RawMessage(`{"query":"sample"}`)}}
					return nil
				}
				if streaming {
					cb("partial search answer", false, nil)
				}
				return want
			}
			recorder := httptest.NewRecorder()
			writer := &inferenceWriter{ResponseWriter: recorder, ctx: ctx, cancel: cancel}
			var err error
			if streaming {
				err = handleAnthropicStream(writer, &Account{}, nil, "model", "id", true, false, false, nil, true, nil, nil, nil)
			} else {
				err = handleAnthropicNonStream(writer, &Account{}, nil, "model", "id", true, false, false, nil, true, nil, nil, nil)
			}
			if !errors.Is(err, want) || calls != 2 {
				t.Fatalf("search failure became success: calls=%d err=%v", calls, err)
			}
			if strings.Contains(recorder.Body.String(), "message_stop") || strings.Contains(recorder.Body.String(), "end_turn") {
				t.Fatal("failed subrequest emitted a normal completion")
			}
		})
	}
}

func TestUpgradeReviewRetentionExpansionDoesNotInventCoverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.sqlite")
	store, err := NewRequestLogStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now()
	store.capturedSince = now.AddDate(0, 0, -90).UnixMilli()
	if _, err := store.db.Exec("UPDATE request_log_meta SET value=? WHERE key='captured_since'", store.capturedSince); err != nil {
		t.Fatal(err)
	}
	start := now.AddDate(0, 0, -20).UnixMilli()
	storeRequestFixture(t, store, "older", start, 1, "success", "model", "")
	if err := store.SetRetentionDays(1); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRetentionDays(30); err != nil {
		t.Fatal(err)
	}
	assertCoverage := func(t *testing.T) {
		t.Helper()
		logs, err := store.List(RequestLogFilter{From: start, To: start + 100, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if logs.Total != 0 || logs.DataComplete || logs.RetainedFrom < now.AddDate(0, 0, -1).UnixMilli() {
			t.Fatalf("deleted period is falsely covered after retention expansion: %+v", logs.RequestLogCoverage)
		}
	}
	assertCoverage(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewRequestLogStore(path, 30)
	if err != nil {
		t.Fatal(err)
	}
	assertCoverage(t)
}

func TestUpgradeReviewCumulativeStepUsageIsNotDoubleCounted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
	}{
		{"agent", []string{
			`{"type":"agent-inference","id":"same-step","finishedAt":1,"inputTokens":10,"outputTokens":2,"value":[]}`,
			`{"type":"agent-inference","id":"same-step","finishedAt":1,"inputTokens":12,"outputTokens":3,"value":[]}`,
			`{"type":"agent-inference","id":"next-step","finishedAt":2,"inputTokens":5,"outputTokens":1,"value":[]}`,
		}},
		{"patch", []string{
			`{"type":"patch","v":[{"o":"a","p":"/s/0/inputTokens","v":10},{"o":"a","p":"/s/0/outputTokens","v":2}]}`,
			`{"type":"patch","v":[{"o":"a","p":"/s/0/inputTokens","v":12},{"o":"a","p":"/s/0/outputTokens","v":3}]}`,
			`{"type":"patch","v":[{"o":"a","p":"/s/1/inputTokens","v":5},{"o":"a","p":"/s/1/outputTokens","v":1}]}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var final UsageInfo
			err := parseNDJSONStream(strings.NewReader(strings.Join(tc.lines, "\n")), "usage-review", func(_ string, _ bool, usage *UsageInfo) {
				if usage != nil {
					final = *usage
				}
			}, nil, nil, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if final.PromptTokens != 17 || final.CompletionTokens != 4 {
				t.Fatalf("cumulative step snapshots counted more than once: %#v", final)
			}
		})
	}
}
