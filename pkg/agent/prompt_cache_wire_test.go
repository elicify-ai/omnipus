package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/elicify-ai/omnipus/pkg/providers"
	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
)

// Test-only provider decoding; these are not gateway/SPA wire-format types.
type cacheWireBlock struct {
	Type         string                  `json:"type"`
	Text         string                  `json:"text,omitempty"`
	ID           string                  `json:"id,omitempty"`
	Name         string                  `json:"name,omitempty"`
	Input        json.RawMessage         `json:"input,omitempty"`
	ToolUseID    string                  `json:"tool_use_id,omitempty"`
	Content      json.RawMessage         `json:"content,omitempty"`
	CacheControl *providers.CacheControl `json:"cache_control,omitempty"`
}

type cacheWireMessage struct {
	Role    string           `json:"role"`
	Content []cacheWireBlock `json:"content"`
}

type cacheWireBody struct {
	Tools    []json.RawMessage  `json:"tools,omitempty"`
	System   []cacheWireBlock   `json:"system"`
	Messages []cacheWireMessage `json:"messages"`
}

func captureCacheWire(t *testing.T, messages []providers.Message) cacheWireBody {
	t.Helper()
	type capture struct {
		raw []byte
		err error
	}
	captured := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		captured <- capture{raw, err}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_cache_test","type":"message","role":"assistant","model":"claude-sonnet-4-5","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	// Keep native-Anthropic dialect selection; only the network destination is
	// replaced. A localhost provider id must not silently disable native marks.
	client := anthropic.NewClient(option.WithAPIKey("test-token"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	p := anthropicprovider.NewProviderWithClient(&client)
	if _, err := p.Chat(t.Context(), messages, nil, "claude-sonnet-4-5", map[string]any{"max_tokens": 64}); err != nil {
		t.Fatalf("real adapter to fake HTTP server: %v", err)
	}
	got := <-captured
	if got.err != nil {
		t.Fatalf("read provider request: %v", got.err)
	}
	var body cacheWireBody
	if err := json.Unmarshal(got.raw, &body); err != nil {
		t.Fatalf("decode provider request: %v\n%s", err, got.raw)
	}
	return body
}

func cacheWirePrefix(t *testing.T, body cacheWireBody, historyText string) string {
	t.Helper()
	prefix := cacheWireBody{Tools: body.Tools, System: append([]cacheWireBlock(nil), body.System...)}
	for i := range prefix.System {
		prefix.System[i].CacheControl = nil
	}
	for _, msg := range body.Messages {
		copyMessage := cacheWireMessage{Role: msg.Role}
		for _, block := range msg.Content {
			block.CacheControl = nil
			copyMessage.Content = append(copyMessage.Content, block)
			if msg.Role == "assistant" && block.Type == "text" && block.Text == historyText {
				prefix.Messages = append(prefix.Messages, copyMessage)
				raw, err := json.Marshal(prefix)
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
		}
		prefix.Messages = append(prefix.Messages, copyMessage)
	}
	t.Fatalf("adapted history boundary %q is missing", historyText)
	return ""
}

func cacheWireTailError(body cacheWireBody) error {
	for _, block := range body.System {
		if secondsInPrefix.MatchString(block.Text) || strings.Contains(block.Text, "Current Time") {
			return fmt.Errorf("after normalization and adaptation the clock is still in the system prefix")
		}
	}
	pending := map[string]bool{}
	var previous *cacheWireBlock
	seenTail := false
	for _, msg := range body.Messages {
		for i := range msg.Content {
			block := &msg.Content[i]
			if seenTail {
				return fmt.Errorf("content appears after the volatile tail")
			}
			if block.Type == "tool_use" {
				pending[block.ID] = true
			}
			if block.Type == "tool_result" {
				delete(pending, block.ToolUseID)
			}
			if block.CacheControl != nil && len(pending) != 0 {
				return fmt.Errorf("cache mark splits a tool-call/result group")
			}
			if strings.Contains(block.Text, "Current Time") {
				if msg.Role != "user" || block.Type != "text" || len(pending) != 0 {
					return fmt.Errorf("volatile tail is not user text after all paired tool results")
				}
				if previous == nil || previous.CacheControl == nil || previous.CacheControl.Type != "ephemeral" ||
					(previous.CacheControl.TTL != "" && previous.CacheControl.TTL != "5m") {
					return fmt.Errorf("last history content before the tail has no 5-minute cache mark")
				}
				if block.CacheControl != nil {
					return fmt.Errorf("volatile tail is inside the history cache mark")
				}
				seenTail = true
			}
			previous = block
		}
	}
	if !seenTail {
		return fmt.Errorf("adapted request has no user-role volatile tail")
	}
	return nil
}

func assertCacheWireToolResults(t *testing.T, body cacheWireBody, want map[string]string) {
	t.Helper()
	calls, results := map[string]bool{}, map[string]string{}
	pending := map[string]bool{}
	for _, msg := range body.Messages {
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				if msg.Role != "assistant" || calls[block.ID] {
					t.Fatalf("invalid or duplicate adapted tool call %q", block.ID)
				}
				calls[block.ID], pending[block.ID] = true, true
			case "tool_result":
				if msg.Role != "user" || !pending[block.ToolUseID] {
					t.Fatalf("invalid or unpaired adapted result %q", block.ToolUseID)
				}
				delete(pending, block.ToolUseID)
				var text string
				if err := json.Unmarshal(block.Content, &text); err != nil {
					var parts []cacheWireBlock
					if err := json.Unmarshal(block.Content, &parts); err != nil {
						t.Fatalf("decode tool result %q: %v", block.ToolUseID, err)
					}
					for _, part := range parts {
						text += part.Text
					}
				}
				results[block.ToolUseID] = text
			default:
				if len(pending) != 0 {
					t.Fatal("text inserted between a tool call and its complete set of results")
				}
			}
		}
	}
	if len(pending) != 0 || len(calls) != len(want) || !reflect.DeepEqual(results, want) {
		t.Fatalf("adapted tool results = %v, pending = %v, calls = %v; want paired results %v", results, pending, calls, want)
	}
}
