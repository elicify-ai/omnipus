package providers

// T6 — the historical factory→official-SDK parity probe, promoted from the
// scratch artifact (backend-green/anthropic-factory-parity.go, receipt
// dc05a3a5e) into a permanent committed test.
//
// Install target: pkg/providers/ (package providers).
//
// Oracles (prompt-caching-spec.md):
//   - FR-004: Anthropic-protocol calls are built by the OFFICIAL Anthropic
//     connector; the constructor keeps the configured timeout and sends the
//     catalog model id verbatim ("MiniMax-M2.7" is not rewritten).
//   - US-2 Independent Test: a reconstructed tool call whose name exists only
//     in the canonical JSON is sent with that name, its arguments, its id,
//     and its paired result; media parts are still sent; parallel results
//     stay correlated.
//   - FR-005: the history mark sits before the volatile tail; MiniMax
//     documents the 5-minute mode (no 1-hour mark).
//   - Captionless user media is this promotion's addition beyond the probe:
//     anthropicUserContent must send image/PDF parts without inventing a
//     text block for an empty caption.
//
// Boundary: only the HTTP transport is fake. The real factory dispatch, the
// official SDK client, buildParams, anthropicUserContent, appendToolResult
// and markCache all run for real. The type assertion to the official
// provider is this file's "old connector stays absent" guarantee; the
// package-level absence check remains TestConnector_OneAnthropicPackage.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	officialanthropic "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
)

type expandedMediaWireSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type expandedMediaWireCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl"`
}

type expandedMediaWireBlock struct {
	Type         string                         `json:"type"`
	Text         string                         `json:"text"`
	Name         string                         `json:"name"`
	ID           string                         `json:"id"`
	ToolUseID    string                         `json:"tool_use_id"`
	Input        json.RawMessage                `json:"input"`
	Source       *expandedMediaWireSource       `json:"source"`
	CacheControl *expandedMediaWireCacheControl `json:"cache_control"`
	// Content carries a tool_result block's inner items (the documented SDK
	// shape: NewToolResultBlock wraps one text item). Absent on other kinds.
	Content []expandedMediaWireResultContent `json:"content"`
}

// expandedMediaWireResultContent is one item of a tool_result's content
// array. anthropic-sdk-go v1.48.0 TextBlockParam marshals Type "text" from
// its declared default (`default:"text"` in message.go), so the exact item
// shape {"type":"text","text":...} is a documented expectation.
type expandedMediaWireResultContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type expandedMediaWireMessage struct {
	Role    string                   `json:"role"`
	Content []expandedMediaWireBlock `json:"content"`
}

func expandedMediaPNGDataURL(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode fixture png: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func expandedMediaPDFDataURL() string {
	return "data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n%%EOF"))
}

func expandedMediaParityMessages(t *testing.T) []Message {
	t.Helper()
	img := expandedMediaPNGDataURL(t)
	pdf := expandedMediaPDFDataURL()
	return []Message{
		// Captioned user media: text + image + document, in that order.
		{Role: "user", Content: "EXPANDED-MEDIA-CAPTION", Media: []string{img, pdf}},
		// Captionless user media: image + document and NO invented text block.
		{Role: "user", Media: []string{img, pdf}},
		// One canonical call (name only in Function) and one parallel call
		// (convenience Name + map arguments) must both reach the wire intact.
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "expanded_media_one", Function: &FunctionCall{Name: "expanded_media_canonical_tool", Arguments: `{"x":1}`}},
			{ID: "expanded_media_two", Name: "expanded_media_parallel_tool", Arguments: map[string]any{"x": 2}},
		}},
		{Role: "tool", ToolCallID: "expanded_media_one", Content: "EXPANDED-FIRST-RESULT"},
		{Role: "tool", ToolCallID: "expanded_media_two", Content: "EXPANDED-SECOND-RESULT"},
		// The volatile tail: the history mark must stay before it (FR-005).
		{Role: "user", Content: "## Current Time\n2026-10-16T12:00:00Z EXPANDED-MEDIA-TAIL", Volatile: true},
	}
}

func TestExpandedMedia_FactoryToOfficialSDKWireParity(t *testing.T) {
	withFixtureCatalog(t)

	t.Run("chat", func(t *testing.T) { expandedMediaDriveParity(t, false) })
	t.Run("stream", func(t *testing.T) { expandedMediaDriveParity(t, true) })
}

func expandedMediaDriveParity(t *testing.T, stream bool) {
	t.Helper()

	var mu sync.Mutex
	capture := struct {
		path, timeout, apiKey string
		raw                   []byte
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		capture.path = r.URL.Path
		capture.timeout = r.Header.Get("X-Stainless-Timeout")
		capture.apiKey = r.Header.Get("X-Api-Key")
		capture.raw = raw
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: "+
				`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"model":"MiniMax-M2.7","usage":{"input_tokens":1,"output_tokens":0}}}`+"\n\n")
			_, _ = io.WriteString(w, "event: content_block_start\ndata: "+
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\ndata: "+
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`+"\n\n")
			_, _ = io.WriteString(w, "event: content_block_stop\ndata: "+
				`{"type":"content_block_stop","index":0}`+"\n\n")
			_, _ = io.WriteString(w, "event: message_delta\ndata: "+
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`+"\n\n")
			_, _ = io.WriteString(w, "event: message_stop\ndata: "+`{"type":"message_stop"}`+"\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"MiniMax-M2.7","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)

	// The catalog row's api_base is overridden by the capture server URL; the
	// configured catalog id ("minimax") must still decide the dialect.
	cfg := &config.ModelConfig{
		Provider:       "minimax",
		Model:          "MiniMax-M2.7",
		APIBase:        srv.URL,
		APIKeyRef:      keyRef(t, "EXPANDED_MEDIA_PARITY_KEY"),
		RequestTimeout: 7,
	}
	p, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if modelID != "MiniMax-M2.7" {
		t.Errorf("modelID = %q, want the catalog model id verbatim (FR-004)", modelID)
	}
	ap, ok := p.(*officialanthropic.Provider)
	if !ok {
		t.Fatalf("factory returned %T, want the official *anthropic.Provider (FR-004)", p)
	}

	messages := expandedMediaParityMessages(t)
	var resp *officialanthropic.LLMResponse
	if stream {
		resp, err = ap.ChatStream(context.Background(), messages, nil, modelID, nil, nil, nil)
	} else {
		resp, err = ap.Chat(context.Background(), messages, nil, modelID, nil)
	}
	if err != nil {
		t.Fatalf("call error = %v", err)
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("response = %+v, want content %q", resp, "ok")
	}
	// 1 uncached input + 1 output is the fixture's usage; the exact split
	// shapes are T2's business, this is the sanity that usage still crosses.
	if resp.Usage == nil || resp.Usage.TotalTokens != 2 {
		t.Fatalf("usage = %+v, want a parsed usage totalling 2", resp.Usage)
	}

	mu.Lock()
	path, timeout, apiKey, raw := capture.path, capture.timeout, capture.apiKey, capture.raw
	mu.Unlock()
	expandedMediaAssertWire(t, raw, path, timeout, apiKey, stream)
}

func expandedMediaAssertWire(t *testing.T, raw []byte, path, timeout, apiKey string, stream bool) {
	t.Helper()

	if want := "/v1/messages"; path != want {
		t.Errorf("request path = %q, want %q (the canonical Messages endpoint)", path, want)
	}
	if want := "test-key"; apiKey != want {
		t.Errorf("X-Api-Key = %q, want the configured credential %q", apiKey, want)
	}
	// FR-004: the configured timeout is kept. Asserted on the non-streaming
	// leg only: the streaming path deliberately drops the wall-clock request
	// timeout (its guard is the connection/stall pair, not a deadline).
	if !stream && timeout != "7" {
		t.Errorf("configured request timeout on the wire = %q, want %q", timeout, "7")
	}

	var body struct {
		Model    string                     `json:"model"`
		Messages []expandedMediaWireMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("captured body is not JSON: %v\n%s", err, raw)
	}
	if body.Model != "MiniMax-M2.7" {
		t.Errorf("model id = %q, want MiniMax-M2.7 verbatim (FR-004)", body.Model)
	}
	if len(body.Messages) != 5 {
		t.Fatalf("wire messages = %d, want 5 (captioned user, captionless user, assistant calls, correlated results, volatile tail)\n%s", len(body.Messages), raw)
	}

	// Captioned user media: text 1, image 1, document 1.
	expandedMediaAssertKinds(t, "captioned", body.Messages[0], map[string]int{"text": 1, "image": 1, "document": 1}, raw)
	if img := body.Messages[0].Content[1]; img.Source == nil || img.Source.MediaType != "image/png" || img.Source.Data == "" {
		t.Errorf("captioned image part = %+v, want a base64 png source", img)
	}
	if doc := body.Messages[0].Content[2]; doc.Source == nil || doc.Source.Type != "base64" || doc.Source.Data == "" {
		t.Errorf("captioned document part = %+v, want a base64 pdf source", doc)
	}

	// Captionless user media: image 1, document 1, and NO invented text.
	expandedMediaAssertKinds(t, "captionless", body.Messages[1], map[string]int{"text": 0, "image": 1, "document": 1}, raw)
	if img := body.Messages[1].Content[0]; img.Type != "image" || img.Source == nil || img.Source.MediaType != "image/png" {
		t.Errorf("captionless first part = %+v, want the png image", img)
	}
	if doc := body.Messages[1].Content[1]; doc.Type != "document" || doc.Source == nil || doc.Source.Data == "" {
		t.Errorf("captionless second part = %+v, want the pdf document", doc)
	}

	// Tool calls: canonical name from Function, parallel convenience Name,
	// ids and arguments all on the wire.
	calls := body.Messages[2].Content
	if len(calls) != 2 {
		t.Fatalf("assistant blocks = %d, want the 2 parallel tool calls\n%s", len(calls), raw)
	}
	if calls[0].Type != "tool_use" || calls[0].Name != "expanded_media_canonical_tool" || calls[0].ID != "expanded_media_one" {
		t.Errorf("first call = %+v, want canonical name with its id preserved", calls[0])
	}
	expandedMediaAssertArgs(t, calls[0], map[string]any{"x": float64(1)}, "canonical call")
	if calls[1].Type != "tool_use" || calls[1].Name != "expanded_media_parallel_tool" || calls[1].ID != "expanded_media_two" {
		t.Errorf("second call = %+v, want parallel convenience name with its id preserved", calls[1])
	}
	expandedMediaAssertArgs(t, calls[1], map[string]any{"x": float64(2)}, "parallel call")

	// Results: one correlated user-role group, order preserved, ids paired,
	// and each result's CONTENT sent intact — exactly the documented single
	// text item (SDK NewToolResultBlock → one TextBlockParam per result),
	// not just the id pairing.
	results := body.Messages[3].Content
	if body.Messages[3].Role != "user" || len(results) != 2 {
		t.Fatalf("result group = %+v, want one user message with 2 tool_result blocks\n%s", body.Messages[3], raw)
	}
	if results[0].Type != "tool_result" || results[1].Type != "tool_result" {
		t.Errorf("result block types = %s/%s, want tool_result", results[0].Type, results[1].Type)
	}
	if results[0].ToolUseID != "expanded_media_one" || results[1].ToolUseID != "expanded_media_two" {
		t.Errorf("result ids = %s/%s, want the calls correlated in order", results[0].ToolUseID, results[1].ToolUseID)
	}
	if want := []expandedMediaWireResultContent{{Type: "text", Text: "EXPANDED-FIRST-RESULT"}}; !reflect.DeepEqual(results[0].Content, want) {
		t.Errorf("first result content = %+v, want exactly %+v (the tool's own text, uncorrupted)", results[0].Content, want)
	}
	if want := []expandedMediaWireResultContent{{Type: "text", Text: "EXPANDED-SECOND-RESULT"}}; !reflect.DeepEqual(results[1].Content, want) {
		t.Errorf("second result content = %+v, want exactly %+v (the tool's own text, uncorrupted)", results[1].Content, want)
	}

	// The MiniMax history mark: ephemeral 5m on the last result, no 1-hour
	// mark anywhere, and before the volatile tail (FR-005, D19).
	if results[0].CacheControl != nil {
		t.Errorf("first result carries %+v; only the LAST history content carries the mark", results[0].CacheControl)
	}
	if cc := results[1].CacheControl; cc == nil || cc.Type != "ephemeral" || cc.TTL != "5m" {
		t.Errorf("history mark = %+v, want ephemeral 5m on the last tool result", cc)
	}

	tail := body.Messages[4]
	if tail.Role != "user" || len(tail.Content) != 1 || tail.Content[0].Type != "text" {
		t.Fatalf("volatile tail = %+v, want one user text block\n%s", tail, raw)
	}
	if tail.Content[0].CacheControl != nil {
		t.Errorf("volatile tail carries a mark; the history mark must sit before it (FR-005)")
	}
}

func expandedMediaAssertKinds(t *testing.T, label string, msg expandedMediaWireMessage, want map[string]int, raw []byte) {
	t.Helper()
	got := make(map[string]int, len(want)+1)
	// Seed every wanted kind at zero BEFORE counting, so an explicit 0 in
	// want is a real expectation (the captionless control demands text:0)
	// instead of an always-false missing key. Kinds outside want still gain
	// their own key and fail the comparison, so extra blocks stay detected.
	for kind := range want {
		got[kind] = 0
	}
	for _, block := range msg.Content {
		got[block.Type]++
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s message parts = %v, want %v\n%s", label, got, want, raw)
	}
}

func expandedMediaAssertArgs(t *testing.T, block expandedMediaWireBlock, want map[string]any, label string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(block.Input, &got); err != nil {
		t.Fatalf("%s input is not JSON: %v (%s)", label, err, block.Input)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s arguments = %v, want %v", label, got, want)
	}
}
