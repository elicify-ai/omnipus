package providers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func TestCodexProvider_ChatTransportsCorrelatedToolImagesInOrder(t *testing.T) {
	colors := []color.RGBA{{R: 201, A: 255}, {B: 173, A: 255}}
	urls := make([]string, 2)
	for i, fill := range colors {
		img := image.NewRGBA(image.Rect(0, 0, i+2, i+3))
		for y := 0; y < i+3; y++ {
			for x := 0; x < i+2; x++ {
				img.SetRGBA(x, y, fill)
			}
		}
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		urls[i] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())
	}
	captured := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read emitted request: %v", err)
		}
		captured <- data
		writeCompletedSSE(w, map[string]any{"id": "resp", "object": "response", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}})
	}))
	defer server.Close()
	p := NewCodexProvider("token", "account")
	p.client = createOpenAITestClient(server.URL, "token", "account")
	messages := []protocoltypes.Message{
		{Role: "assistant", ToolCalls: []protocoltypes.ToolCall{{ID: "call-1", Name: "read_file", Arguments: map[string]any{}}, {ID: "call-2", Name: "read_file", Arguments: map[string]any{}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "first", Media: []string{urls[0]}},
		{Role: "tool", ToolCallID: "call-2", Content: "second", Media: []string{urls[1]}},
	}
	if _, err := p.Chat(t.Context(), messages, nil, "gpt-5", nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(<-captured, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Input) != 4 {
		t.Fatalf("input count=%d, want two calls and two results", len(body.Input))
	}
	for i, item := range body.Input {
		var kind, callID string
		if err := json.Unmarshal(item["type"], &kind); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(item["call_id"], &callID); err != nil {
			t.Fatal(err)
		}
		wantKind := "function_call"
		if i >= 2 {
			wantKind = "function_call_output"
		}
		if kind != wantKind || callID != fmt.Sprintf("call-%d", i%2+1) {
			t.Fatalf("input[%d] type=%q call_id=%q", i, kind, callID)
		}
		if i < 2 {
			continue
		}
		index := i - 2
		var output []map[string]any
		if err := json.Unmarshal(item["output"], &output); err != nil {
			t.Fatal(err)
		}
		wantText := []string{"first", "second"}[index]
		want := []map[string]any{{"type": "input_text", "text": wantText}, {"type": "input_image", "image_url": urls[index], "detail": "auto"}}
		if !reflect.DeepEqual(output, want) {
			t.Fatalf("call-%d output=%#v, want %#v", index+1, output, want)
		}
		imageURL, ok := output[1]["image_url"].(string)
		if !ok {
			t.Fatalf("call-%d image_url=%#v, want string", index+1, output[1]["image_url"])
		}
		encoded := imageURL[len("data:image/png;base64,"):]
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds() != image.Rect(0, 0, index+2, index+3) || color.RGBAModel.Convert(img.At(0, 0)) != colors[index] {
			t.Fatalf("call-%d PNG has wrong dimensions or pixel", index+1)
		}
	}
}
