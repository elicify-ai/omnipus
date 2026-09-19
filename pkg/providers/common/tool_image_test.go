package common

import (
	"encoding/json"
	"strings"
	"testing"
)

const toolImageDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestSerializeMessages_ToolImagePreservesResultThenSupplement(t *testing.T) {
	got := SerializeMessages([]Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}}, {Role: "tool", ToolCallID: "call-1", Content: "marker", Media: []string{toolImageDataURL}}, {Role: "tool", ToolCallID: "call-2", Content: "plain"}})
	if len(got) != 4 {
		t.Fatalf("messages=%d want 4", len(got))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"tool_call_id":"call-1"`) || !strings.Contains(s, toolImageDataURL) || strings.Index(s, `"tool_call_id":"call-1"`) > strings.Index(s, toolImageDataURL) {
		t.Fatalf("request=%s", s)
	}
	if strings.Index(s, `"tool_call_id":"call-2"`) > strings.Index(s, toolImageDataURL) {
		t.Fatalf("supplement split consecutive tool results: %s", s)
	}
}
