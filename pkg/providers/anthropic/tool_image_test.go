package anthropicprovider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildParams_ToolImageNativeBlock(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	params, err := buildParams([]Message{{Role: "tool", ToolCallID: "call-b", Content: "marker", Media: []string{dataURL}}}, nil, "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(params)
	s := string(raw)
	if !strings.Contains(s, `"tool_use_id":"call-b"`) || !strings.Contains(s, `"type":"image"`) || !strings.Contains(s, `"media_type":"image/png"`) || !strings.Contains(s, strings.TrimPrefix(dataURL, "data:image/png;base64,")) {
		t.Fatalf("params=%s", s)
	}
}
