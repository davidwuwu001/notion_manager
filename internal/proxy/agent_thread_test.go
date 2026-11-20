package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAutoModelIsExposedToScopedAndGlobalCatalogs(t *testing.T) {
	pool := NewAccountPool()
	a := &Account{UserID: "a", SpaceID: "s", Models: []ModelEntry{{ID: "alpha", Name: "Alpha"}}}
	pool.AddAccount(a)

	if !modelListContainsID(pool.AllModels(), autoModelID) {
		t.Fatal("global catalog missing auto")
	}
	if !modelListContainsID(pool.ScopedModels(map[string]bool{a.AccountID: true}), autoModelID) {
		t.Fatal("scoped catalog missing auto")
	}
	if modelListContainsID(pool.ScopedModels(map[string]bool{}), autoModelID) {
		t.Fatal("empty scoped catalog unexpectedly exposed auto")
	}
	if modelListContainsID(a.modelsSnapshot(), autoModelID) {
		t.Fatal("auto was persisted into an account model snapshot")
	}
}

func TestAutoModelRejectsAttachments(t *testing.T) {
	acc := &Account{UserID: "user", SpaceID: "space", TokenV2: "token", ClientVersion: DefaultClientVersion}
	attachments := []UploadedAttachment{{AttachmentURL: "attachment:test:file", FileName: "file", ContentType: "text/plain"}}
	err := CallInference(acc, []ChatMessage{{Role: "user", Content: "hello"}}, autoModelID, true, func(string, bool, *UsageInfo) {}, CallOptions{Context: context.Background(), Attachments: attachments})
	if err == nil || !strings.Contains(err.Error(), "does not support attachments") {
		t.Fatalf("expected attachment rejection, got %v", err)
	}
}

func modelListContainsID(models []ModelEntry, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func TestRoutingAllowsAutoAcrossAccountModelSets(t *testing.T) {
	pool := routingPool(2)
	accounts := pool.AccountsSnapshot()
	accounts[0].setModels([]ModelEntry{{ID: "alpha", Name: "Alpha"}})
	accounts[1].setModels([]ModelEntry{{ID: "bravo", Name: "Bravo"}})
	acc, release, err := pool.SelectForRequest(autoModelID, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if acc == nil {
		t.Fatal("auto selected no account")
	}
}

func TestCallInferenceAutoUsesAgentThreadAndOmitsModel(t *testing.T) {
	originalConfig, originalBase, originalClient := AppConfig, NotionAPIBase, agentThreadHTTPClient
	AppConfig = DefaultConfig()
	var createPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/createAgentThread":
			if err := json.NewDecoder(r.Body).Decode(&createPayload); err != nil {
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			if _, exists := createPayload["model"]; exists {
				http.Error(w, "auto request must omit model", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/listenToThreadTranscript":
			w.Header().Set("Content-Type", "application/x-ndjson")
			stream := strings.Join([]string{
				`{"type":"agent_service_transcript_delta","patches":[{"op":"put","entity":{"kind":"thinking","source":"provisional","id":"think-1","content_text":"reasoning"}}]}`,
				`{"type":"agent_service_transcript_delta","patches":[{"op":"put","entity":{"kind":"assistant_message","source":"provisional","id":"reply-1","content":[{"type":"text","text":"Hello"}]}}]}`,
				`{"type":"agent_service_transcript_delta","patches":[{"op":"put","entity":{"kind":"assistant_message","source":"committed","id":"reply-1","content":[{"type":"text","text":"Hello from Auto"}]}}]}`,
				`{"type":"agent_service_transcript_delta","patches":[{"op":"put","entity":{"kind":"turn_completed"}}]}`,
			}, "\n")
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			_, _ = writer.Write([]byte(stream))
			_ = writer.Close()
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(compressed.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	NotionAPIBase = server.URL
	agentThreadHTTPClient = func(time.Duration) *http.Client { return server.Client() }
	t.Cleanup(func() {
		AppConfig, NotionAPIBase, agentThreadHTTPClient = originalConfig, originalBase, originalClient
	})

	acc := &Account{UserID: "user", SpaceID: "space", TokenV2: "token", ClientVersion: DefaultClientVersion}
	var output strings.Builder
	var thinkingBlocks []ThinkingBlock
	err := CallInference(acc, []ChatMessage{{Role: "user", Content: "hello"}}, autoModelID, true, func(delta string, done bool, usage *UsageInfo) {
		if delta != "" {
			output.WriteString(delta)
		}
	}, CallOptions{Context: context.Background(), RequestID: "auto-test", ThinkingBlocks: &thinkingBlocks})
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "Hello from Auto" {
		t.Fatalf("unexpected auto output %q", output.String())
	}
	if len(thinkingBlocks) != 1 || thinkingBlocks[0].Content != "reasoning" {
		t.Fatalf("unexpected thinking blocks %+v", thinkingBlocks)
	}
	if _, exists := createPayload["model"]; exists {
		t.Fatal("createAgentThread request unexpectedly carried model")
	}
}

func TestOpenAIAndAnthropicEntrypointsAcceptAuto(t *testing.T) {
	fixture := newUpgradeProtocolFixture(t, 1)
	var seenModels []string
	inferenceCaller = func(_ *Account, _ []ChatMessage, model string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		seenModels = append(seenModels, model)
		cb("ok", true, nil)
		return nil
	}
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
		request := map[string]interface{}{"model": autoModelID, "stream": false, "max_tokens": 32}
		if path == "/v1/responses" {
			request["input"] = []map[string]string{{"role": "user", "content": "hello"}}
		} else {
			request["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body))).WithContext(context.Background())
		w := httptest.NewRecorder()
		RequestLogMiddleware(fixture.store, handlerForPath(path, fixture.pool)).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, w.Code, w.Body.String())
		}
	}
	if len(seenModels) != 3 {
		t.Fatalf("expected auto to reach three protocols, saw %v", seenModels)
	}
	for _, model := range seenModels {
		if model != autoModelID {
			t.Fatalf("unexpected model %q", model)
		}
	}
}

func TestAnthropicAutoKeepsToolBridge(t *testing.T) {
	fixture := newUpgradeProtocolFixture(t, 1)
	var prompt string
	inferenceCaller = func(_ *Account, messages []ChatMessage, model string, _ bool, cb StreamCallback, _ ...CallOptions) error {
		if model != autoModelID {
			t.Fatalf("unexpected model %q", model)
		}
		for _, message := range messages {
			prompt += message.Content + "\n"
		}
		cb("ok", true, nil)
		return nil
	}
	body := map[string]interface{}{
		"model":       autoModelID,
		"max_tokens":  32,
		"messages":    []map[string]string{{"role": "user", "content": "hello"}},
		"tools":       []map[string]interface{}{{"name": "Read", "input_schema": map[string]string{"type": "object"}}},
		"tool_choice": map[string]string{"type": "auto"},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(encoded))).WithContext(context.Background())
	w := httptest.NewRecorder()
	RequestLogMiddleware(fixture.store, HandleAnthropicMessages(fixture.pool)).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("auto with tools returned %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(prompt, "Read") {
		t.Fatalf("tool bridge was not injected: %q", prompt)
	}
}

func handlerForPath(path string, pool *AccountPool) http.HandlerFunc {
	switch path {
	case "/v1/messages":
		return HandleAnthropicMessages(pool)
	case "/v1/chat/completions":
		return HandleOpenAIChatCompletions(pool)
	case "/v1/responses":
		return HandleOpenAIResponses(pool)
	default:
		panic("unknown protocol path")
	}
}
