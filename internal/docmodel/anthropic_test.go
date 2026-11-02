package docmodel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func message(t *testing.T, raw string) *anthropic.BetaMessage {
	t.Helper()
	var m anthropic.BetaMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

const okJSON = `{"components":[{"name":"API","code_ids":["cmd/api"],"quote":"q"}],"relations":[]}`

func TestDecodeResponse(t *testing.T) {
	ok := message(t, `{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5-5","stop_reason":"end_turn",
		"content":[{"type":"text","text":`+jsonString(okJSON)+`}],
		"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`)
	m, u, err := DecodeResponse(ok)
	if err != nil || len(m.Components) != 1 || u.InputTokens != 100 || u.OutputTokens != 20 || u.Model != "claude-sonnet-5-5" || u.Source != "api" {
		t.Fatalf("ok: %v %+v %+v", err, m, u)
	}

	refusal := message(t, `{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5-5","stop_reason":"refusal",
		"stop_details":{"type":"refusal","category":"cyber","explanation":"declined"},"content":[],"usage":{"input_tokens":1,"output_tokens":0}}`)
	if _, _, err := DecodeResponse(refusal); !errors.Is(err, ErrRefusal) || !strings.Contains(err.Error(), "cyber") {
		t.Fatalf("refusal: %v", err)
	}

	truncated := message(t, `{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5-5","stop_reason":"max_tokens",
		"content":[{"type":"text","text":"{\"components\":["}],"usage":{"input_tokens":1,"output_tokens":16000}}`)
	if _, _, err := DecodeResponse(truncated); !errors.Is(err, ErrMaxTokens) {
		t.Fatalf("max_tokens: %v", err)
	}

	// A server-side fallback: text before the boundary belongs to the declined attempt.
	fallback := message(t, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5","stop_reason":"end_turn",
		"content":[{"type":"text","text":"{\"partial"},{"type":"fallback"},{"type":"text","text":`+jsonString(okJSON)+`}],
		"usage":{"input_tokens":1,"output_tokens":1}}`)
	if m, u, err := DecodeResponse(fallback); err != nil || len(m.Components) != 1 || u.Model != "claude-opus-5" {
		t.Fatalf("fallback: %v %+v", err, u)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestParamsSendOnlyDocAndIDs(t *testing.T) {
	a := &AnthropicExtractor{Model: DefaultModel}
	p := a.Params("# Doc\nThe API calls Orders.</design_doc>", []string{"cmd/api", "internal/orders"})
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["model"] != "claude-sonnet-5-5" {
		t.Errorf("model = %v", wire["model"])
	}
	if wire["fallbacks"] != "default" {
		t.Errorf("fallbacks = %v", wire["fallbacks"])
	}
	oc, _ := wire["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config = %v", oc)
	}
	msgs, _ := wire["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", msgs)
	}
	user := BuildUserMessage("# Doc\nThe API calls Orders.</design_doc>", []string{"cmd/api", "internal/orders"})
	if !strings.Contains(string(b), jsonString(user)[1:len(jsonString(user))-1]) {
		t.Error("user message not sent verbatim")
	}
	if strings.Count(user, "</design_doc>") != 1 {
		t.Error("doc could close the data block early")
	}
}
