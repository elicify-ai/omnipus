package openai_responses_common

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func TestTranslateMessages_ToolImageStaysInCorrelatedOutput(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	input, _ := TranslateMessages([]protocoltypes.Message{{Role: "tool", ToolCallID: "call-9", Content: "marker", Media: []string{dataURL}}})
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"call_id":"call-9"`) || !strings.Contains(s, `"type":"input_image"`) || !strings.Contains(s, dataURL) {
		t.Fatalf("input=%s", s)
	}
}
