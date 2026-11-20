package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	Kind        string          `json:"kind"`
	Source      string          `json:"source"`
	ID          string          `json:"id"`
	Content     json.RawMessage `json:"content"`
	ContentText string          `json:"content_text"`
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

func agentThreadText(blocks []agentThreadContentBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// Provisional text may be replaced, including an individual block of a
// multi-block assistant message. Preserve block indices until promotion.
func patchAgentThreadText(blocks []agentThreadContentBlock, ops []agentThreadPatchOp) {
	for _, op := range ops {
		path := strings.Split(strings.TrimPrefix(op.Path, "/"), "/")
		if len(path) != 3 || path[0] != "content" || path[2] != "text" {
			continue
		}
		index, err := strconv.Atoi(path[1])
		value, ok := op.Value.(string)
		if err != nil || !ok || index < 0 || index >= len(blocks) || blocks[index].Type != "text" {
			continue
		}
		switch op.Op {
		case "append":
			blocks[index].Text += value
		case "replace":
			blocks[index].Text = value
		}
	}
}

func parseAgentThreadStream(reader io.Reader, opt *CallOptions, cb StreamCallback) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	provisional := make(map[string][]agentThreadContentBlock)
	var provisionalOrder []string
	committed := make(map[string]string)
	lastCommittedID := ""
	emittedText := false
	thinking := make(map[string]string)
	var thinkingOrder []string

	flushThinking := func() {
		var parts []string
		for _, id := range thinkingOrder {
			if thinking[id] != "" {
				parts = append(parts, thinking[id])
			}
		}
		text := strings.Join(parts, "\n\n")
		if text != "" && opt != nil {
			if !emittedText && opt.ThinkingCallback != nil {
				opt.ThinkingCallback(text, false, "")
				opt.ThinkingCallback("", true, "")
			}
			if opt.ThinkingBlocks != nil {
				*opt.ThinkingBlocks = append(*opt.ThinkingBlocks, ThinkingBlock{Content: text})
			}
		}
		clear(thinking)
		thinkingOrder = nil
	}

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event agentThreadStreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return errors.New("invalid Notion agent transcript")
		}
		if event.Type != "agent_service_transcript_delta" {
			continue
		}
		for _, patch := range event.Patches {
			if patch.Op == "patch" {
				patchAgentThreadText(provisional[patch.ID], patch.Ops)
				continue
			}
			if patch.Op != "put" || patch.Entity == nil {
				continue
			}
			entity := patch.Entity
			switch entity.Kind {
			case "assistant_message":
				var blocks []agentThreadContentBlock
				if len(entity.Content) != 0 && json.Unmarshal(entity.Content, &blocks) != nil {
					return errors.New("invalid Notion assistant content")
				}
				if strings.EqualFold(entity.Source, "provisional") {
					if _, seen := provisional[entity.ID]; !seen {
						provisionalOrder = append(provisionalOrder, entity.ID)
					}
					provisional[entity.ID] = blocks
					continue
				}
				text := agentThreadText(blocks)
				delta := text
				if prev, seen := committed[entity.ID]; seen && entity.ID != "" {
					if text == prev {
						continue
					}
					if entity.ID != lastCommittedID || !strings.HasPrefix(text, prev) {
						return errors.New("Notion agent rewrote committed text")
					}
					delta = text[len(prev):]
				} else if text != "" && emittedText {
					delta = "\n\n" + text
				}
				if delta != "" {
					flushThinking()
					cb(delta, false, nil)
					emittedText = true
					committed[entity.ID] = text
					lastCommittedID = entity.ID
				}
			case "thinking":
				if entity.ID != "" && strings.EqualFold(entity.Source, "provisional") {
					if _, seen := thinking[entity.ID]; !seen {
						thinkingOrder = append(thinkingOrder, entity.ID)
					}
					thinking[entity.ID] = entity.ContentText
				}
			case "turn_completed":
				flushThinking()
				if !emittedText {
					var parts []string
					for _, id := range provisionalOrder {
						if text := agentThreadText(provisional[id]); text != "" {
							parts = append(parts, text)
						}
					}
					if len(parts) == 0 {
						return ErrEmptyResponse
					}
					cb(strings.Join(parts, "\n\n"), false, nil)
				}
				cb("", true, nil)
				return nil
			default:
				kind := strings.ToLower(entity.Kind)
				if strings.Contains(kind, "error") || strings.Contains(kind, "failed") {
					return errors.New("Notion agent turn failed")
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
