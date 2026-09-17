package anthropicmessages

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildRequestBody_ToolImageNativeBlock(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	body, err := buildRequestBody([]Message{{Role: "tool", ToolCallID: "call-a", Content: "marker", Media: []string{dataURL}}}, nil, "model", map[string]any{"max_tokens": 128})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	s := string(raw)
	if !strings.Contains(s, `"tool_use_id":"call-a"`) || !strings.Contains(s, `"type":"image"`) || !strings.Contains(s, `"media_type":"image/png"`) || !strings.Contains(s, strings.TrimPrefix(dataURL, "data:image/png;base64,")) {
		t.Fatalf("body=%s", s)
	}
}
