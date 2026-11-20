package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

// explicitTextToolCalls recognizes only complete envelopes with both a name
// and an explicit arguments field. Ordinary JSON without that tool-call shape
// remains ordinary content, even if it happens to contain a name field.
func explicitTextToolCalls(content string) ([]ToolCall, bool) {
	parse := func(raw string) (*ToolCall, bool) {
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(strings.TrimSpace(raw)), &envelope) != nil {
			return nil, false
		}
		outer := envelope
		if nested, ok := envelope["tool_call"]; ok {
			var inner map[string]json.RawMessage
			if json.Unmarshal(nested, &inner) != nil {
				return nil, false
			}
			envelope = inner
		}
		_, hasName := envelope["name"]
		args, hasArgs := envelope["arguments"]
		if !hasName || !hasArgs {
			return nil, false
		}
		// A tool-shaped envelope with duplicate keys is ambiguous regardless
		// of which value a JSON parser happens to keep.
		if toolEnvelopeHasAliases(outer) || toolEnvelopeHasAliases(envelope) {
			return nil, true
		}
		if _, err := decodeUniqueToolJSON(raw); err != nil {
			return nil, true
		}
		var name string
		if json.Unmarshal(envelope["name"], &name) != nil || strings.TrimSpace(name) == "" {
			return nil, false
		}
		if !json.Valid(args) {
			return nil, false
		}
		return &ToolCall{Function: ToolCallFunction{Name: name, Arguments: string(args)}}, false
	}
	if c, ambiguous := parse(content); c != nil || ambiguous {
		if ambiguous {
			return nil, true
		}
		return []ToolCall{*c}, false
	}
	// Compare every independently encoded representation before selecting one.
	var groups [][]ToolCall
	remaining := content
	for _, matches := range [][][]string{toolCallXMLRegex.FindAllStringSubmatch(content, -1), mdFenceRegex.FindAllStringSubmatch(content, -1)} {
		var calls []ToolCall
		for _, m := range matches {
			if c, ambiguous := parse(m[1]); ambiguous {
				return nil, true
			} else if c != nil {
				calls = append(calls, *c)
			}
			remaining = strings.Replace(remaining, m[0], "", 1)
		}
		if len(calls) > 0 {
			groups = append(groups, calls)
		}
	}
	var lines []ToolCall
	for _, line := range strings.Split(remaining, "\n") {
		if c, ambiguous := parse(line); ambiguous {
			return nil, true
		} else if c != nil {
			lines = append(lines, *c)
		}
	}
	if len(lines) > 0 {
		groups = append(groups, lines)
	}
	if len(groups) == 0 {
		return nil, false
	}
	for _, group := range groups[1:] {
		if !toolCallListsAgree(groups[0], group) {
			return nil, true
		}
	}
	return groups[0], false
}

// mixedToolRepresentationsAgree allows a second representation only when its
// full multiset of name/arguments matches the native one. IDs differ between
// the two formats, so equality is based on decoded JSON rather than IDs.
func mixedToolRepresentationsAgree(content string, native []AgentValueEntry) bool {
	text, ambiguous := explicitTextToolCalls(content)
	if ambiguous {
		return false
	}
	actual := nativeToolUseToOpenAI(native)
	if len(actual) == 0 {
		return true
	}
	if len(text) == 0 {
		return true
	}
	return toolCallListsAgree(text, actual)
}

func toolEnvelopeHasAliases(envelope map[string]json.RawMessage) bool {
	seen := map[string]bool{}
	for key := range envelope {
		lower := strings.ToLower(key)
		if lower != "name" && lower != "arguments" && lower != "tool_call" {
			continue
		}
		if seen[lower] {
			return true
		}
		seen[lower] = true
	}
	return false
}

func toolCallListsAgree(text, actual []ToolCall) bool {
	if len(text) != len(actual) {
		return false
	}
	used := make([]bool, len(actual))

	for _, t := range text {
		want, err := decodeUniqueToolJSON(t.Function.Arguments)
		if err != nil {
			return false
		}
		found := false
		for i, n := range actual {
			if used[i] || t.Function.Name != n.Function.Name {
				continue
			}
			got, err := decodeUniqueToolJSON(n.Function.Arguments)
			if err == nil && reflect.DeepEqual(want, got) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func decodeUniqueToolJSON(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		if depth > 1000 {
			return nil, errors.New("tool argument nesting limit exceeded")
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return token, nil
		}
		switch delim {
		case '{':
			value := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("invalid object key")
				}
				if _, exists := value[key]; exists {
					return nil, errors.New("duplicate object key")
				}
				child, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				value[key] = child
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errors.New("invalid object end")
			}
			return value, nil
		case '[':
			var value []any
			for decoder.More() {
				child, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				value = append(value, child)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errors.New("invalid array end")
			}
			return value, nil
		default:
			return nil, errors.New("invalid argument delimiter")
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing argument data")
	}
	return value, nil
}
