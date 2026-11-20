package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const autoModelID = "auto"

var agentThreadHTTPClient = getChromeHTTPClient

func IsAutoModel(model string) bool {
	trimmed, _ := StripAskModeSuffix(strings.TrimSpace(model))
	return strings.EqualFold(trimmed, autoModelID)
}

type agentThreadContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type agentThreadEntity struct {
	Kind        string                    `json:"kind"`
	Source      string                    `json:"source"`
	ID          string                    `json:"id"`
	Content     []agentThreadContentBlock `json:"content"`
	ContentText string                    `json:"content_text"`
	Message     string                    `json:"message"`
	Error       string                    `json:"error"`
	Text        string                    `json:"text"`
}

type agentThreadPatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

type agentThreadPatch struct {
	Op     string               `json:"op"`
	ID     string               `json:"id"`
	Entity *agentThreadEntity   `json:"entity"`
	Ops    []agentThreadPatchOp `json:"ops"`
}

type agentThreadStreamEvent struct {
	Type    string             `json:"type"`
	Patches []agentThreadPatch `json:"patches"`
}

type agentThreadCreateRequest struct {
	Type            string                   `json:"type"`
	SpaceID         string                   `json:"spaceId"`
	ThreadID        string                   `json:"threadId"`
	CreatedSource   string                   `json:"createdSource"`
	Content         []agentThreadTextContent `json:"content"`
	ClientMessageID string                   `json:"clientMessageId"`
}

type agentThreadTextContent struct {
	Type string     `json:"type"`
	Text [][]string `json:"text"`
}

type agentThreadListenRequest struct {
	SpaceID                        string `json:"spaceId"`
	ThreadID                       string `json:"threadId"`
	IncludeProvisionalUserMessages bool   `json:"includeProvisionalUserMessages"`
	IncludeQueueChanges            bool   `json:"includeQueueChanges"`
}

type agentThreadReadCloser struct {
	io.Reader
	cleanup func()
}

func (body *agentThreadReadCloser) Close() error {
	if body.cleanup != nil {
		body.cleanup()
	}
	return nil
}

func buildAgentThreadPrompt(messages []ChatMessage) string {
	promptMessages := buildAgentThreadPromptMessages(messages)
	if len(promptMessages) == 0 {
		return ""
	}
	return promptMessages[0].Content
}

func callAgentThreadInference(acc *Account, messages []ChatMessage, cb StreamCallback, opt *CallOptions) error {
	if acc == nil {
		return fmt.Errorf("account unavailable")
	}
	if opt != nil && len(opt.Attachments) > 0 {
		return fmt.Errorf("auto model does not support attachments")
	}
	prompt := buildAgentThreadPrompt(messages)
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("empty agent thread prompt")
	}

	threadID := generateUUIDv4()
	createReq := agentThreadCreateRequest{
		Type:            "personal_agent",
		SpaceID:         acc.SpaceID,
		ThreadID:        threadID,
		CreatedSource:   "full_page_chat",
		Content:         []agentThreadTextContent{{Type: "text", Text: [][]string{{prompt}}}},
		ClientMessageID: generateUUIDv4(),
	}
	LogNotionRequestJSON(opt.RequestID, fmt.Sprintf("POST /createAgentThread account=%s model=auto", acc.UserEmail), createReq)
	if err := postAgentThreadJSON(opt.Context, acc, "/createAgentThread", createReq, "application/json", opt.RequestID); err != nil {
		return err
	}

	listenReq := agentThreadListenRequest{
		SpaceID:                        acc.SpaceID,
		ThreadID:                       threadID,
		IncludeProvisionalUserMessages: true,
		IncludeQueueChanges:            false,
	}
	body, err := postAgentThreadStream(opt.Context, acc, "/listenToThreadTranscript", listenReq, opt.RequestID)
	if err != nil {
		return err
	}
	defer body.Close()
	return parseAgentThreadStream(body, opt, cb)
}

func postAgentThreadJSON(ctx context.Context, acc *Account, path string, payload interface{}, accept, requestID string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, NotionAPIBase+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	setNotionHeaders(req, acc)
	req.Header.Set("Accept", accept)
	client := agentThreadHTTPClient(AppConfig.InferenceTimeoutDuration())
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		LogNotionResponseText(requestID, "POST "+path+" error body", string(body))
		return &notionHTTPError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func postAgentThreadStream(ctx context.Context, acc *Account, path string, payload interface{}, requestID string) (io.ReadCloser, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, NotionAPIBase+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	setNotionHeaders(req, acc)
	req.Header.Set("Accept", "application/x-ndjson")
	client := agentThreadHTTPClient(AppConfig.InferenceTimeoutDuration())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		LogNotionResponseText(requestID, "POST "+path+" error body", string(body))
		return nil, &notionHTTPError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	reader, cleanup, err := decompressBody(resp)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	return &agentThreadReadCloser{Reader: reader, cleanup: func() {
		if cleanup != nil {
			cleanup()
		}
		resp.Body.Close()
	}}, nil
}

func parseAgentThreadStream(reader io.Reader, opt *CallOptions, cb StreamCallback) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	assistantText := make(map[string]string)
	assistantOrder := make([]string, 0, 4)
	committed := make([]string, 0, 4)
	thinkingText := make(map[string]string)
	thinkingOrder := make([]string, 0, 4)
	turnCompleted := false

	appendAssistant := func(id, text string) {
		if id == "" || text == "" {
			return
		}
		if _, exists := assistantText[id]; !exists {
			assistantOrder = append(assistantOrder, id)
		}
		assistantText[id] = text
	}
	appendThinking := func(id, text string) {
		if id == "" || text == "" {
			return
		}
		if _, exists := thinkingText[id]; !exists {
			thinkingOrder = append(thinkingOrder, id)
		}
		thinkingText[id] = text
	}

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event agentThreadStreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Type != "agent_service_transcript_delta" {
			continue
		}
		for _, patch := range event.Patches {
			switch patch.Op {
			case "session", "session_status":
				continue
			case "patch":
				for _, op := range patch.Ops {
					if (op.Op == "append" || op.Op == "replace") && strings.HasSuffix(op.Path, "/text") {
						if value, ok := op.Value.(string); ok {
							if op.Op == "append" {
								appendAssistant(patch.ID, assistantText[patch.ID]+value)
							} else {
								appendAssistant(patch.ID, value)
							}
						}
					}
				}
			case "put":
				if patch.Entity == nil {
					continue
				}
				entity := patch.Entity
				switch entity.Kind {
				case "assistant_message":
					var parts []string
					for _, block := range entity.Content {
						if block.Type == "text" {
							parts = append(parts, block.Text)
						}
					}
					text := strings.Join(parts, "")
					if strings.EqualFold(entity.Source, "provisional") {
						appendAssistant(entity.ID, text)
					} else if text != "" {
						committed = append(committed, text)
					}
				case "thinking":
					if strings.EqualFold(entity.Source, "provisional") {
						appendThinking(entity.ID, entity.ContentText)
					}
				case "turn_completed":
					turnCompleted = true
				default:
					kind := strings.ToLower(entity.Kind)
					if strings.Contains(kind, "error") || strings.Contains(kind, "failed") {
						message := entity.Message
						if message == "" {
							message = entity.Error
						}
						if message == "" {
							message = entity.Text
						}
						if message == "" {
							message = entity.Kind
						}
						return fmt.Errorf("notion agent thread: %s", message)
					}
				}
			}
			if turnCompleted {
				break
			}
		}
		if turnCompleted {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	var final []string
	if len(committed) > 0 {
		final = committed
	} else {
		for _, id := range assistantOrder {
			if text := assistantText[id]; text != "" {
				final = append(final, text)
			}
		}
	}
	if len(final) == 0 {
		return ErrEmptyResponse
	}
	if len(thinkingOrder) > 0 {
		var thinking strings.Builder
		for _, id := range thinkingOrder {
			if text := thinkingText[id]; text != "" {
				if thinking.Len() > 0 {
					thinking.WriteString("\n\n")
				}
				thinking.WriteString(text)
			}
		}
		if thinking.Len() > 0 {
			if opt != nil && opt.ThinkingCallback != nil {
				opt.ThinkingCallback(thinking.String(), true, "")
			}
			if opt != nil && opt.ThinkingBlocks != nil {
				*opt.ThinkingBlocks = append(*opt.ThinkingBlocks, ThinkingBlock{Content: thinking.String()})
			}
		}
	}
	cb(strings.Join(final, "\n\n"), true, nil)
	return nil
}
