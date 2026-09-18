package anthropicprovider

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestToolImagesReachAnthropicChatAndChatStreamTransportsInOrder(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	messages := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}, {ID: "call-2", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "call-1", Content: "first", Media: []string{"data:image/png;base64," + png}},
		{Role: "tool", ToolCallID: "call-2", Content: "second", Media: []string{"data:image/png;base64," + png}},
	}

	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%t", streaming), func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"model\",\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"id":"msg","type":"message","role":"assistant","model":"model","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
			}))
			defer server.Close()

			p := NewProviderWithClient(createAnthropicTestClient(server.URL, "token"))
			var err error
			if streaming {
				_, err = p.ChatStream(t.Context(), messages, nil, "model", nil, nil, nil)
			} else {
				_, err = p.Chat(t.Context(), messages, nil, "model", nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertAnthropicToolImageRequest(t, body, png)
		})
	}
}

func assertAnthropicToolImageRequest(t *testing.T, body map[string]any, wantBase64 string) {
	t.Helper()
	messages, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages=%#v, want correlated tool results", body["messages"])
	}
	var gotIDs []string
	var gotImages [][]byte
	for _, rawMessage := range messages {
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
			gotIDs = append(gotIDs, toolUseID)
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
					t.Fatalf("decode image: %v", err)
				}
				gotImages = append(gotImages, decoded)
			}
		}
	}
	wantImage, _ := base64.StdEncoding.DecodeString(wantBase64)
	if !reflect.DeepEqual(gotIDs, []string{"call-1", "call-2"}) || len(gotImages) != 2 || !reflect.DeepEqual(gotImages[0], wantImage) || !reflect.DeepEqual(gotImages[1], wantImage) {
		t.Fatalf("tool IDs=%v image count=%d", gotIDs, len(gotImages))
	}
}
