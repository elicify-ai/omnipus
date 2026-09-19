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
	if strings.Contains(s, "Tool-result media omitted") {
		t.Fatalf("supported PNG produced an omission warning: %s", s)
	}
}

func TestBuildParams_ToolImagePreservesAnthropicSupportedFormats(t *testing.T) {
	for _, mediaType := range []string{"image/jpeg", "image/png", "image/gif", "image/webp"} {
		t.Run(mediaType, func(t *testing.T) {
			const encoded = "AA=="
			params, err := buildParams([]Message{{Role: "tool", ToolCallID: "call-format", Content: "marker", Media: []string{"data:" + mediaType + ";base64," + encoded}}}, nil, "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			s := string(raw)
			if !strings.Contains(s, `"media_type":"`+mediaType+`"`) || strings.Contains(s, "Tool-result media omitted") {
				t.Fatalf("supported format was not preserved: %s", s)
			}
		})
	}
}

func TestBuildParams_ToolMediaOmissionIsVisible(t *testing.T) {
	const warning = "[Tool-result media omitted for Anthropic: unsupported media type (1), malformed image data (1). Re-read the attachment in a supported image format.]"
	params, err := buildParams([]Message{{
		Role:       "tool",
		ToolCallID: "call-warning",
		Content:    "inspection completed",
		Media: []string{
			"data:application/pdf;base64,JVBERi0xLjQ=",
			"data:image/png;base64-without-comma",
		},
	}}, nil, "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), warning) {
		t.Fatalf("wire payload did not disclose omitted media; want %q in %s", warning, raw)
	}
}

func TestBuildParams_ToolBMPAndTIFFOmissionIsVisible(t *testing.T) {
	const warning = "[Tool-result media omitted for Anthropic: unsupported image format (2). Re-read the attachment in a supported image format.]"
	params, err := buildParams([]Message{{
		Role: "tool", ToolCallID: "call-formats", Content: "inspection completed",
		Media: []string{"data:image/bmp;base64,Qk0=", "data:image/tiff;base64,TU0AKg=="},
	}}, nil, "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, warning) {
		t.Fatalf("wire payload did not disclose unsupported formats: %s", s)
	}
	if strings.Contains(s, "Qk0=") || strings.Contains(s, "TU0AKg==") {
		t.Fatalf("unsupported image bytes leaked into wire payload: %s", s)
	}
}
