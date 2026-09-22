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
	"testing"
)

func TestConvertMessages_ToolImageNativeBlock(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	messages, _ := convertMessages([]Message{{Role: "tool", ToolCallID: "call-bed", Content: "marker", Media: []string{dataURL}}})
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("messages=%#v", messages)
	}
	tool := messages[0].Content[0].ToolResult
	if tool == nil || tool.ToolUseID != "call-bed" || len(tool.Content) != 2 {
		t.Fatalf("tool=%#v", tool)
	}
	image := tool.Content[1].Image
	if image == nil {
		t.Fatal("tool-result image block is nil")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(image.Source.Bytes))
	if err != nil || cfg.Width != 1 || cfg.Height != 1 {
		t.Fatalf("config=%#v err=%v", cfg, err)
	}
}

func TestConvertMessages_ToolMediaOmissionIsVisible(t *testing.T) {
	const warning = "[Tool-result media omitted for Bedrock: unsupported image format (2), malformed image data (1), image exceeds 10 MiB (1). Re-read the attachment in a supported image format.]"
	oversize := "data:image/png;base64," + string(bytes.Repeat([]byte{'A'}, 14*1024*1024))
	messages, _ := convertMessages([]Message{{
		Role: "tool", ToolCallID: "call-warning", Content: "inspection completed",
		Media: []string{
			"data:image/bmp;base64,AA==", "data:image/tiff;base64,AA==",
			"data:image/png;base64,not-valid!!!", oversize,
		},
	}})
	tool := messages[0].Content[0].ToolResult
	if tool == nil || len(tool.Content) != 2 || tool.Content[1].Text == nil || *tool.Content[1].Text != warning {
		t.Fatalf("tool result=%#v, want exact warning %q", tool, warning)
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
	p, err := NewProvider("fake-key", WithBaseEndpoint(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}, {ID: "call-2", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "first", Media: []string{"data:image/png;base64," + encodedPNG}},
		{Role: "tool", ToolCallID: "call-2", Content: "second", Media: []string{"data:image/png;base64," + encodedPNG}},
	}
	if _, err := p.Chat(context.Background(), messages, nil, "model", nil); err != nil {
		t.Fatal(err)
	}
	rawMessages, present := body["messages"]
	if !present {
		t.Fatalf("request body is missing messages: %#v", body)
	}
	apiMessages, ok := rawMessages.([]any)
	if !ok {
		t.Fatalf("request messages type = %T, want []any", rawMessages)
	}
	var ids []string
	var images [][]byte
	for messageIndex, rawMessage := range apiMessages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			t.Fatalf("message[%d] type = %T, want map[string]any", messageIndex, rawMessage)
		}
		rawContents, present := message["content"]
		if !present {
			t.Fatalf("message[%d] is missing content: %#v", messageIndex, message)
		}
		contents, ok := rawContents.([]any)
		if !ok {
			t.Fatalf("message[%d] content type = %T, want []any", messageIndex, rawContents)
		}
		for contentIndex, rawContent := range contents {
			content, ok := rawContent.(map[string]any)
			if !ok {
				t.Fatalf("message[%d] content[%d] type = %T, want map[string]any", messageIndex, contentIndex, rawContent)
			}
			rawResult, present := content["toolResult"]
			if !present {
				continue
			}
			result, ok := rawResult.(map[string]any)
			if !ok {
				t.Fatalf("message[%d] content[%d] toolResult type = %T, want map[string]any", messageIndex, contentIndex, rawResult)
			}
			toolUseID, ok := result["toolUseId"].(string)
			if !ok {
				t.Fatalf("message[%d] content[%d] toolUseId type = %T, want string", messageIndex, contentIndex, result["toolUseId"])
			}
			ids = append(ids, toolUseID)
			rawResultContents, present := result["content"]
			if !present {
				t.Fatalf("message[%d] content[%d] toolResult is missing content: %#v", messageIndex, contentIndex, result)
			}
			resultContents, ok := rawResultContents.([]any)
			if !ok {
				t.Fatalf("message[%d] content[%d] toolResult content type = %T, want []any", messageIndex, contentIndex, rawResultContents)
			}
			for resultIndex, rawResultContent := range resultContents {
				resultContent, ok := rawResultContent.(map[string]any)
				if !ok {
					t.Fatalf("message[%d] content[%d] toolResult content[%d] type = %T, want map[string]any", messageIndex, contentIndex, resultIndex, rawResultContent)
				}
				rawImage, present := resultContent["image"]
				if !present {
					continue
				}
				image, ok := rawImage.(map[string]any)
				if !ok {
					t.Fatalf("message[%d] content[%d] toolResult content[%d] image type = %T, want map[string]any", messageIndex, contentIndex, resultIndex, rawImage)
				}
				source, ok := image["source"].(map[string]any)
				if !ok {
					t.Fatalf("message[%d] content[%d] toolResult content[%d] image source type = %T, want map[string]any", messageIndex, contentIndex, resultIndex, image["source"])
				}
				encoded, ok := source["bytes"].(string)
				if !ok {
					t.Fatalf("message[%d] content[%d] toolResult content[%d] image bytes type = %T, want string", messageIndex, contentIndex, resultIndex, source["bytes"])
				}
				decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
				if decodeErr != nil {
					t.Fatal(decodeErr)
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
