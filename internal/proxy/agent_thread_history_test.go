package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Two clients may use the same conversation and call IDs on different keys.
// Each continuation must carry its own complete history into its fresh Auto thread.
func TestAutoParallelScopedHistoryReplay(t *testing.T) {
	f := newUpgradeProtocolFixture(t, 2)
	accounts := f.pool.AccountsSnapshot()
	var mu sync.Mutex
	prompts := map[string][]string{}
	inferenceCaller = func(acc *Account, messages []ChatMessage, _ string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		mu.Lock()
		prompts[acc.AccountID] = append(prompts[acc.AccountID], buildAgentThreadPrompt(messages))
		mu.Unlock()
		cb(`{"name":"__done__","arguments":{"result":"ok"}}`, true, nil)
		return nil
	}
	var wg sync.WaitGroup
	for i, acc := range accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			label := []string{"case-alpha", "case-beta"}[i]
			scope := &AccessScope{Identity: label, Allowed: map[string]bool{acc.AccountID: true}}
			request := AnthropicRequest{
				Model: "auto", MaxTokens: 100, System: "instructions-" + label,
				Metadata: map[string]interface{}{"session_id": "same-session"},
				Messages: []AnthropicMessage{{Role: "user", Content: "fix main.txt for " + label}},
			}
			for _, name := range []string{"Read", "Edit", "Bash", "Glob", "Grep", "Write"} {
				request.Tools = append(request.Tools, AnthropicTool{Name: name, InputSchema: map[string]string{"type": "object"}})
			}
			for turn := 0; turn < 2; turn++ {
				if turn == 1 {
					request.Messages = append(request.Messages,
						AnthropicMessage{Role: "assistant", Content: []map[string]interface{}{
							{"type": "tool_use", "id": "call-1", "name": "Read", "input": map[string]string{"path": "main.txt"}},
							{"type": "tool_use", "id": "call-2", "name": "Read", "input": map[string]string{"path": "test.txt"}},
						}},
						AnthropicMessage{Role: "user", Content: []map[string]interface{}{
							{"type": "tool_result", "tool_use_id": "call-2", "is_error": true, "content": "missing-" + label},
							{"type": "tool_result", "tool_use_id": "call-1", "content": "result-" + label},
						}},
					)
				}
				data, err := json.Marshal(request)
				if err != nil {
					t.Error(err)
					return
				}
				r := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(data)).WithContext(WithAccessScope(context.Background(), scope))
				w := httptest.NewRecorder()
				HandleAnthropicMessages(f.pool).ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Errorf("%s turn %d: %d %s", label, turn, w.Code, w.Body.String())
					return
				}
			}
		}()
	}
	wg.Wait()
	for i, acc := range accounts {
		t.Run(acc.SpaceID, func(t *testing.T) {
			requests := prompts[acc.AccountID]
			if len(requests) != 2 {
				t.Fatalf("got %d requests", len(requests))
			}
			label := []string{"case-alpha", "case-beta"}[i]
			other := []string{"case-beta", "case-alpha"}[i]
			prompt := requests[1]
			if strings.Contains(prompt, other) {
				t.Error("foreign history replayed")
			}
			historyStart := strings.Index(prompt, `[{"role":"system"`)
			if historyStart < 0 {
				t.Fatal("structured history absent")
			}
			var history []ChatMessage
			if err := json.NewDecoder(strings.NewReader(prompt[historyStart:])).Decode(&history); err != nil {
				t.Fatal(err)
			}
			if len(history) != 5 || history[0].Content != "instructions-"+label || history[1].Content != "fix main.txt for "+label {
				t.Fatalf("unexpected history: %+v", history)
			}
			calls := history[2].ToolCalls
			if len(calls) != 2 || calls[0].ID != "call-1" || calls[1].ID != "call-2" || calls[1].Function.Arguments != `{"path":"test.txt"}` {
				t.Fatalf("unexpected calls %+v", calls)
			}
			if history[3].ToolCallID != "call-2" || !history[3].IsError || history[3].Content != "missing-"+label || history[4].ToolCallID != "call-1" || history[4].IsError || history[4].Content != "result-"+label {
				t.Fatalf("unexpected results: %+v", history[3:])
			}
		})
	}
}
