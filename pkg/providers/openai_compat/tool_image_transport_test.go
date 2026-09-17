package openai_compat

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

func TestToolImagesReachChatAndChatStreamTransportsInOrder(t *testing.T) {
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
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()

			p := mustNewProvider(t, "key", server.URL, "")
			var err error
			if streaming {
				_, err = p.ChatStream(t.Context(), messages, nil, "model", nil, nil, nil)
			} else {
				_, err = p.Chat(t.Context(), messages, nil, "model", nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertOpenAIToolImageRequest(t, body, png)
		})
	}
}

func assertOpenAIToolImageRequest(t *testing.T, body map[string]any, wantBase64 string) {
	t.Helper()
	rawMessages, ok := body["messages"].([]any)
	if !ok || len(rawMessages) != 5 {
		t.Fatalf("messages=%#v, want assistant, two tool results, two image supplements", body["messages"])
	}
	gotRoles := make([]string, 0, len(rawMessages))
	gotCallIDs := make([]string, 0, 2)
	imageCount := 0
	for _, raw := range rawMessages {
		msg := raw.(map[string]any)
		gotRoles = append(gotRoles, msg["role"].(string))
		if msg["role"] == "tool" {
			gotCallIDs = append(gotCallIDs, msg["tool_call_id"].(string))
		}
		parts, _ := msg["content"].([]any)
		for _, rawPart := range parts {
			part := rawPart.(map[string]any)
			if part["type"] != "image_url" {
				continue
			}
			url := part["image_url"].(map[string]any)["url"].(string)
			encoded := strings.TrimPrefix(url, "data:image/png;base64,")
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || !reflect.DeepEqual(decoded, mustDecodeBase64(t, wantBase64)) {
				t.Fatalf("image payload corrupt: err=%v", err)
			}
			imageCount++
		}
	}
	if !reflect.DeepEqual(gotRoles, []string{"assistant", "tool", "tool", "user", "user"}) || !reflect.DeepEqual(gotCallIDs, []string{"call-1", "call-2"}) || imageCount != 2 {
		t.Fatalf("roles=%v callIDs=%v images=%d", gotRoles, gotCallIDs, imageCount)
	}
}

func mustDecodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
