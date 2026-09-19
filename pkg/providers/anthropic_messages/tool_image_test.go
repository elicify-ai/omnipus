package anthropicmessages

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestChatTransportsCorrelatedToolImagesInOrder(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"msg","type":"message","role":"assistant","model":"model","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	p := NewProvider("key", server.URL)
	messages := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}, {ID: "call-2", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "first", Media: []string{"data:image/png;base64," + png}},
		{Role: "tool", ToolCallID: "call-2", Content: "second", Media: []string{"data:image/png;base64," + png}},
	}
	if _, err := p.Chat(t.Context(), messages, nil, "model", map[string]any{"max_tokens": 10}); err != nil {
		t.Fatal(err)
	}
	apiMessages, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages=%#v, want array", body["messages"])
	}
	var ids []string
	var images [][]byte
	for _, rawMessage := range apiMessages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			t.Fatalf("message=%#v, want object", rawMessage)
		}
		content, ok := message["content"].([]any)
		if !ok {
			t.Fatalf("message content=%#v, want array", message["content"])
		}
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				t.Fatalf("content block=%#v, want object", rawBlock)
			}
			if block["type"] != "tool_result" {
				continue
			}
			toolUseID, ok := block["tool_use_id"].(string)
			if !ok {
				t.Fatalf("tool_use_id=%#v, want string", block["tool_use_id"])
			}
			ids = append(ids, toolUseID)
			blockContent, ok := block["content"].([]any)
			if !ok {
				t.Fatalf("tool_result content=%#v, want array", block["content"])
			}
			for _, rawChild := range blockContent {
				child, ok := rawChild.(map[string]any)
				if !ok {
					t.Fatalf("result child=%#v, want object", rawChild)
				}
				if child["type"] != "image" {
					continue
				}
				source, ok := child["source"].(map[string]any)
				if !ok {
					t.Fatalf("image source=%#v, want object", child["source"])
				}
				data, ok := source["data"].(string)
				if !ok {
					t.Fatalf("image data=%#v, want string", source["data"])
				}
				decoded, err := base64.StdEncoding.DecodeString(data)
				if err != nil {
					t.Fatal(err)
				}
				images = append(images, decoded)
			}
		}
	}
	want, _ := base64.StdEncoding.DecodeString(png)
	if !reflect.DeepEqual(ids, []string{"call-1", "call-2"}) || len(images) != 2 || !reflect.DeepEqual(images[0], want) || !reflect.DeepEqual(images[1], want) {
		t.Fatalf("ids=%v images=%d", ids, len(images))
	}
}
