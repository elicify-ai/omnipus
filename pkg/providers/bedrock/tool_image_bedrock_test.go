//go:build bedrock

package bedrock

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestConvertMessages_ToolImageNativeBlock(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	messages, _ := convertMessages([]Message{{Role: "tool", ToolCallID: "call-bed", Content: "marker", Media: []string{dataURL}}})
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("messages=%#v", messages)
	}
	tool, ok := messages[0].Content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("block=%T", messages[0].Content[0])
	}
	if tool.Value.ToolUseId == nil || *tool.Value.ToolUseId != "call-bed" || len(tool.Value.Content) != 2 {
		t.Fatalf("tool=%#v", tool.Value)
	}
	imageBlock, ok := tool.Value.Content[1].(*types.ToolResultContentBlockMemberImage)
	if !ok {
		t.Fatalf("image block=%T", tool.Value.Content[1])
	}
	bytesSource, ok := imageBlock.Value.Source.(*types.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("source=%T", imageBlock.Value.Source)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(bytesSource.Value))
	if err != nil || cfg.Width != 1 || cfg.Height != 1 {
		t.Fatalf("config=%#v err=%v", cfg, err)
	}
	for _, block := range tool.Value.Content {
		if text, ok := block.(*types.ToolResultContentBlockMemberText); ok && strings.Contains(text.Value, "Tool-result media omitted") {
			t.Fatalf("supported PNG produced an omission warning: %q", text.Value)
		}
	}
}

func TestConvertMessages_ToolMediaOmissionIsVisible(t *testing.T) {
	const warning = "[Tool-result media omitted for Bedrock: unsupported image format (2), malformed image data (1), image exceeds 10 MiB (1). Re-read the attachment in a supported image format.]"
	oversize := "data:image/png;base64," + strings.Repeat("A", 14*1024*1024)
	messages, _ := convertMessages([]Message{{
		Role:       "tool",
		ToolCallID: "call-warning",
		Content:    "inspection completed",
		Media: []string{
			"data:image/bmp;base64,AA==",
			"data:image/tiff;base64,AA==",
			"data:image/png;base64,not-valid!!!",
			oversize,
		},
	}})
	tool := messages[0].Content[0].(*types.ContentBlockMemberToolResult)
	if len(tool.Value.Content) != 2 {
		t.Fatalf("tool result content=%#v, want original text plus one warning", tool.Value.Content)
	}
	text, ok := tool.Value.Content[1].(*types.ToolResultContentBlockMemberText)
	if !ok || text.Value != warning {
		t.Fatalf("warning block=%#v, want exact %q", tool.Value.Content[1], warning)
	}
}

func TestChatTransportsCorrelatedToolImagesInOrder(t *testing.T) {
	const encodedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`))
	}))
	defer server.Close()
	client := bedrockruntime.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: testCredentials{}}, func(o *bedrockruntime.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.Retryer = aws.NopRetryer{}
	})
	p := &Provider{client: client}
	messages := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}, {ID: "call-2", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "first", Media: []string{"data:image/png;base64," + encodedPNG}},
		{Role: "tool", ToolCallID: "call-2", Content: "second", Media: []string{"data:image/png;base64," + encodedPNG}},
	}
	if _, err := p.Chat(context.Background(), messages, nil, "model", nil); err != nil {
		t.Fatal(err)
	}
	apiMessages := body["messages"].([]any)
	var ids []string
	var images [][]byte
	for _, rawMessage := range apiMessages {
		for _, rawContent := range rawMessage.(map[string]any)["content"].([]any) {
			result, ok := rawContent.(map[string]any)["toolResult"].(map[string]any)
			if !ok {
				continue
			}
			ids = append(ids, result["toolUseId"].(string))
			for _, rawResultContent := range result["content"].([]any) {
				image, ok := rawResultContent.(map[string]any)["image"].(map[string]any)
				if !ok {
					continue
				}
				encoded := image["source"].(map[string]any)["bytes"].(string)
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Fatal(err)
				}
				images = append(images, decoded)
			}
		}
	}
	want, _ := base64.StdEncoding.DecodeString(encodedPNG)
	if !reflect.DeepEqual(ids, []string{"call-1", "call-2"}) || len(images) != 2 || !reflect.DeepEqual(images[0], want) || !reflect.DeepEqual(images[1], want) {
		t.Fatalf("ids=%v images=%d body=%#v", ids, len(images), body)
	}
}
