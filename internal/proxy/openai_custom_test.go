package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesCustomTextToolRoundTrip(t *testing.T) {
	var req OpenAIResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"sonnet-4.6","input":[{"type":"custom_tool_call","call_id":"call_1","name":"edit","input":"line1\nline2"},{"type":"custom_tool_call_output","call_id":"call_1","output":"done"}],"tools":[{"type":"custom","name":"edit","description":"Edit fixture"}],"tool_choice":{"type":"custom","name":"edit"}}`), &req); err != nil {
		t.Fatal(err)
	}
	anth, aliases, err := convertOpenAIResponsesRequestWithToolAliases(&req)
	if err != nil {
		t.Fatal(err)
	}
	if len(anth.Tools) != 1 || anth.Tools[0].Name != "edit" || aliases["edit"].Name != "edit" {
		t.Fatalf("tool lost: %+v %#v", anth.Tools, aliases)
	}
	if choice, ok := anth.ToolChoice.(map[string]interface{}); !ok || choice["type"] != "tool" || choice["name"] != "edit" {
		t.Fatalf("choice: %#v", anth.ToolChoice)
	}
	encoded, _ := json.Marshal(anth.Messages)
	if !strings.Contains(string(encoded), `line1\nline2`) || !strings.Contains(string(encoded), `call_1`) || !strings.Contains(string(encoded), `done`) {
		t.Fatalf("history did not roundtrip: %s", encoded)
	}
	resp := buildOpenAIResponsesResponse("fixture", 1, "m", &AnthropicResponse{Content: []AnthropicContentBlock{{Type: "tool_use", ID: "call_1", Name: "edit", Input: json.RawMessage(`{"input":"line1\nline2"}`)}}}, aliases)
	output := resp["output"].([]map[string]interface{})
	if len(output) != 1 || output[0]["type"] != "custom_tool_call" || output[0]["input"] != "line1\nline2" {
		t.Fatalf("wrong output: %#v", output)
	}
}

func TestResponsesCustomGrammarRejected(t *testing.T) {
	var req OpenAIResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"m","input":"hello","tools":[{"type":"custom","name":"edit","format":{"type":"grammar","syntax":"lark","definition":"start: X"}}]}`), &req); err != nil {
		t.Fatal(err)
	}
	if _, _, err := convertOpenAIResponsesRequestWithToolAliases(&req); err == nil {
		t.Fatal("unsupported grammar accepted")
	}
}

func TestResponsesCustomStreamUsesInputEvents(t *testing.T) {
	w := httptest.NewRecorder()
	aliases := map[string]openAIToolIdentity{"edit": {Name: "edit", Kind: "custom"}}
	tr := newOpenAIResponsesStreamTranscoder(w, w, "resp", "m", 1, aliases)
	if err := customFrame(tr, "content_block_start", map[string]interface{}{"index": 0, "content_block": map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "edit"}}); err != nil {
		t.Fatal(err)
	}
	if err := customFrame(tr, "content_block_delta", map[string]interface{}{"index": 0, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": `{"input":"a\nb"}`}}); err != nil {
		t.Fatal(err)
	}
	if err := customFrame(tr, "content_block_stop", map[string]interface{}{"index": 0}); err != nil {
		t.Fatal(err)
	}
	s := w.Body.String()
	if !strings.Contains(s, "response.custom_tool_call_input.done") || !strings.Contains(s, `"type":"custom_tool_call"`) || strings.Contains(s, "response.function_call_arguments") {
		t.Fatalf("wrong events: %s", s)
	}
}

func customFrame(tr *openAIResponsesStreamTranscoder, event string, data map[string]interface{}) error {
	b, _ := json.Marshal(data)
	return tr.HandleFrame(anthropicSSEFrame{Event: event, Data: b})
}

func TestResponsesCustomNamespaceAndInvalidChoices(t *testing.T) {
	for _, tc := range []struct {
		choice string
		ok     bool
	}{
		{`{"type":"custom","namespace":"fs","name":"edit"}`, true},
		{`{"type":"function","namespace":"fs","name":"edit"}`, false},
		{`{"type":"custom","name":"missing"}`, false},
		{`{"type":"allowed_tools","mode":"auto","tools":[]}`, false},
	} {
		var req OpenAIResponsesRequest
		raw := `{"model":"m","input":"hi","tools":[{"type":"namespace","name":"fs","tools":[{"type":"custom","name":"edit","format":{"type":"text"}}]}],"tool_choice":` + tc.choice + `}`
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		anth, aliases, err := convertOpenAIResponsesRequestWithToolAliases(&req)
		if (err == nil) != tc.ok {
			t.Fatalf("choice %s error %v", tc.choice, err)
		}
		if tc.ok {
			choice := anth.ToolChoice.(map[string]interface{})
			identity := aliases[choice["name"].(string)]
			if identity.Namespace != "fs" || identity.Name != "edit" || identity.Kind != "custom" {
				t.Fatalf("identity %#v", identity)
			}
		}
	}
}
func TestCustomInputRejectsMalformedWrapper(t *testing.T) {
	for _, value := range []string{`{}`, `{"input":null}`, `{"input":1}`, `{"input":"a","input":"b"}`, `{"input":"a","extra":1}`} {
		if _, err := decodeCustomToolInput(value); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestResponsesToolHistoryRequiresMatchingDeclaredIdentity(t *testing.T) {
	tools := `[{"type":"function","name":"fn","parameters":{"type":"object"}},{"type":"custom","name":"edit"},{"type":"namespace","name":"fs","tools":[{"type":"custom","name":"write"},{"type":"function","name":"read"}]}]`
	for _, tc := range []struct {
		item string
		ok   bool
	}{
		{`{"type":"custom_tool_call","name":"edit","call_id":"c","input":"abc"}`, true},
		{`{"type":"function_call","name":"fn","call_id":"c","arguments":"{}"}`, true},
		{`{"type":"custom_tool_call","namespace":"fs","name":"write","call_id":"c","input":"abc"}`, true},
		{`{"type":"function_call","namespace":"fs","name":"read","call_id":"c","arguments":"{}"}`, true},
		{`{"type":"custom_tool_call","name":"unknown","call_id":"c","input":"abc"}`, true},
		{`{"type":"function_call","name":"unknown","call_id":"c","arguments":"{}"}`, true},
		{`{"type":"custom_tool_call","name":"fn","call_id":"c","input":"abc"}`, false},
		{`{"type":"function_call","name":"edit","call_id":"c","arguments":"{}"}`, false},
		{`{"type":"custom_tool_call","namespace":"fs","name":"read","call_id":"c","input":"abc"}`, false},
		{`{"type":"function_call","namespace":"fs","name":"write","call_id":"c","arguments":"{}"}`, false},
		{`{"type":"custom_tool_call","namespace":"other","name":"write","call_id":"c","input":"abc"}`, true},
		{`{"type":"custom_tool_call","name":"write","call_id":"c","input":"abc"}`, true},
	} {
		var req OpenAIResponsesRequest
		if err := json.Unmarshal([]byte(`{"model":"m","tools":`+tools+`,"input":[`+tc.item+`]}`), &req); err != nil {
			t.Fatal(err)
		}
		_, _, err := convertOpenAIResponsesRequestWithToolAliases(&req)
		if (err == nil) != tc.ok {
			t.Errorf("%s: got error %v", tc.item, err)
		}
	}
}

func TestResponsesStreamFinalOutputFollowsEventOrder(t *testing.T) {
	w := httptest.NewRecorder()
	aliases := map[string]openAIToolIdentity{"edit": {Name: "edit", Kind: "custom"}}
	tr := newOpenAIResponsesStreamTranscoder(w, w, "resp", "m", 1, aliases)
	for _, event := range []struct {
		name string
		data map[string]interface{}
	}{
		{"content_block_start", map[string]interface{}{"index": 0, "content_block": map[string]interface{}{"type": "tool_use", "id": "call", "name": "edit"}}},
		{"content_block_delta", map[string]interface{}{"index": 0, "delta": map[string]interface{}{"type": "input_json_delta", "partial_json": `{"input":"abc"}`}}},
		{"content_block_stop", map[string]interface{}{"index": 0}},
		{"content_block_start", map[string]interface{}{"index": 1, "content_block": map[string]interface{}{"type": "text", "text": ""}}},
		{"content_block_delta", map[string]interface{}{"index": 1, "delta": map[string]interface{}{"type": "text_delta", "text": "done"}}},
	} {
		if err := customFrame(tr, event.name, event.data); err != nil {
			t.Fatal(err)
		}
	}
	output := tr.buildFinalResponseObject()["output"].([]map[string]interface{})
	if len(output) != 2 || output[0]["type"] != "custom_tool_call" || output[1]["type"] != "message" {
		t.Fatalf("output order %#v", output)
	}
}

func TestResponsesHistoryToolsNotExposedForExecution(t *testing.T) {
	for _, input := range []string{
		`[{"type":"function_call","name":"old","call_id":"call_a","arguments":"{}"},{"type":"function_call_output","call_id":"call_a","output":"ok"}]`,
		`[{"type":"custom_tool_call","namespace":"fs","name":"old","call_id":"call_b","input":"a\nb"},{"type":"custom_tool_call_output","call_id":"call_b","output":"ok"}]`,
	} {
		var req OpenAIResponsesRequest
		if err := json.Unmarshal([]byte(`{"model":"m","input":`+input+`}`), &req); err != nil {
			t.Fatal(err)
		}
		anth, aliases, err := convertOpenAIResponsesRequestWithToolAliases(&req)
		if err != nil {
			t.Fatal(err)
		}
		if len(anth.Tools) != 0 || len(aliases) != 0 {
			t.Fatalf("history leaked to current declarations: %#v %#v", anth.Tools, aliases)
		}
		block := anth.Messages[0].Content.([]interface{})[0].(map[string]interface{})
		historyAlias := block["name"].(string)
		prepared := preparedToolBridgeResponse{ToolCalls: []ToolCall{{Function: ToolCallFunction{Name: historyAlias}}}}
		if declaredToolCalls(prepared, map[string]bool{}) {
			t.Fatalf("history alias became executable: %s", historyAlias)
		}
		repeated, _, err := convertOpenAIResponsesRequestWithToolAliases(&req)
		if err != nil {
			t.Fatal(err)
		}
		repeatedAlias := repeated.Messages[0].Content.([]interface{})[0].(map[string]interface{})["name"]
		if repeatedAlias != historyAlias {
			t.Fatalf("unstable historical alias %s != %s", historyAlias, repeatedAlias)
		}
		data, _ := json.Marshal(anth.Messages)
		if !strings.Contains(string(data), `"tool_use_id":"call_`) {
			t.Fatalf("lost call_id: %s", data)
		}
	}
}

func TestResponsesHistoryCallIDsAndAliasCollision(t *testing.T) {
	for _, tc := range []struct {
		input string
		ok    bool
	}{
		{`[{"type":"custom_tool_call","namespace":"old","name":"write","call_id":"x","input":"text"},{"type":"custom_tool_call_output","call_id":"x","output":"ok"}]`, true},
		{`[{"type":"custom_tool_call","name":"old","call_id":"x","input":"text"},{"type":"function_call_output","call_id":"x","output":"ok"}]`, false},
		{`[{"type":"custom_tool_call","name":"old","call_id":"x","input":"text"},{"type":"custom_tool_call","name":"old","call_id":"x","input":"text"}]`, false},
	} {
		var req OpenAIResponsesRequest
		raw := `{"model":"m","tools":[{"type":"function","name":"write"}],"input":` + tc.input + `}`
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		anth, _, err := convertOpenAIResponsesRequestWithToolAliases(&req)
		if (err == nil) != tc.ok {
			t.Errorf("input %s: %v", tc.input, err)
			continue
		}
		if tc.ok {
			block := anth.Messages[0].Content.([]interface{})[0].(map[string]interface{})
			if block["name"] == "write" {
				t.Fatal("history alias collided with current tool")
			}
			if len(anth.Tools) != 1 || anth.Tools[0].Name != "write" {
				t.Fatalf("declarations changed: %#v", anth.Tools)
			}
		}
	}
}

func TestResponsesRejectDuplicateOutputsButPreserveStandaloneResult(t *testing.T) {
	for _, tc := range []struct {
		input string
		ok    bool
	}{
		{`[{"type":"function_call_output","call_id":"x","output":"ok"}]`, true},
		{`[{"type":"custom_tool_call_output","call_id":"x","output":"ok"}]`, true},
		{`[{"type":"function_call_output","call_id":"x","output":"ok"},{"type":"function_call_output","call_id":"x","output":"again"}]`, false},
		{`[{"type":"custom_tool_call_output","call_id":"x","output":"ok"},{"type":"function_call_output","call_id":"x","output":"again"}]`, false},
		{`[{"type":"custom_tool_call","name":"old","call_id":"x","input":"a"},{"type":"custom_tool_call_output","call_id":"x","output":"ok"},{"type":"custom_tool_call_output","call_id":"x","output":"again"}]`, false},
		{`[{"type":"function_call_output","call_id":"x","output":"a"},{"type":"function_call_output","call_id":"y","output":"b"}]`, true},
		{`[{"type":"function_call_output","tool_call_id":"x","output":"a"},{"type":"function_call_output","call_id":"x","output":"b"}]`, false},
	} {
		var req OpenAIResponsesRequest
		if err := json.Unmarshal([]byte(`{"model":"m","metadata":{"session_id":"existing-session"},"input":`+tc.input+`}`), &req); err != nil {
			t.Fatal(err)
		}
		_, _, err := convertOpenAIResponsesRequestWithToolAliases(&req)
		if (err == nil) != tc.ok {
			t.Errorf("input %s: %v", tc.input, err)
		}
	}
}

func TestResponsesHistoryCallIDWhitespaceConsistent(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"type":"function_call","name":"old","call_id":"  x  ","arguments":"{}"}`, `{"type":"function_call_output","call_id":"  x  ","output":"ok"}`},
		{`{"type":"custom_tool_call","name":"old","call_id":"  x  ","input":"abc"}`, `{"type":"custom_tool_call_output","tool_call_id":"  x  ","output":"ok"}`},
	} {
		var req OpenAIResponsesRequest
		if err := json.Unmarshal([]byte(`{"model":"m","input":[`+pair[0]+`,`+pair[1]+`]}`), &req); err != nil {
			t.Fatal(err)
		}
		anth, _, err := convertOpenAIResponsesRequestWithToolAliases(&req)
		if err != nil {
			t.Fatal(err)
		}
		call := anth.Messages[0].Content.([]interface{})[0].(map[string]interface{})
		result := anth.Messages[1].Content.([]interface{})[0].(map[string]interface{})
		if call["id"] != "x" || result["tool_use_id"] != "x" {
			t.Fatalf("inconsistent IDs: %#v %#v", call, result)
		}
	}
}
