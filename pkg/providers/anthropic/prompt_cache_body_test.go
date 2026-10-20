package anthropicprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func captureBody(t *testing.T, catalogBase string, messages []Message, tools []ToolDefinition, model string) (map[string]any, []byte) {
	t.Helper()
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","stop_sequence":null,"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := NewProviderWithBaseURL("token", catalogBase)
	client := anthropic.NewClient(
		option.WithAuthToken("token"),
		option.WithBaseURL(p.BaseURL()),
		option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Transport: rewriteHost{base: base}}),
	)
	p.client = &client
	if _, err := p.Chat(t.Context(), messages, tools, model, map[string]any{"max_tokens": 64}); err != nil {
		t.Fatalf("official connector Chat: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("official connector sent no body")
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, raw)
	}
	return body, raw
}

type rewriteHost struct{ base *url.URL }

func (h rewriteHost) RoundTrip(r *http.Request) (*http.Response, error) {
	cloned := r.Clone(r.Context())
	cloned.URL.Scheme = h.base.Scheme
	cloned.URL.Host = h.base.Host
	return http.DefaultTransport.RoundTrip(cloned)
}

// cacheMarks walks Anthropic's documented render order, NOT JSON object-member
// order (and never Go's randomized map order). Arrays retain their wire order.
func cacheMarks(body cacheBody) []cacheMark {
	var marks []cacheMark
	add := func(section string, message, block int, cc *bodyCacheControl) {
		if cc != nil {
			marks = append(marks, cacheMark{section, message, block, *cc})
		}
	}
	for i, tool := range body.Tools {
		add("tools", -1, i, tool.CacheControl)
	}
	for i, block := range body.System {
		add("system", -1, i, block.CacheControl)
	}
	for i, msg := range body.Messages {
		add("message", i, -1, msg.CacheControl) // illegal level, still counted
		for j, block := range msg.Content {
			add("messages", i, j, block.CacheControl)
		}
	}
	add("request", -1, -1, body.CacheControl)
	return marks
}

func nativeMessages() ([]Message, []ToolDefinition) {
	tools := []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "read",
			Description: "read a file",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}}
	messages := []Message{
		{Role: "system", SystemParts: []protocoltypes.ContentBlock{{Type: "text", Text: "STABLE-INSTRUCTIONS"}}},
		{Role: "user", Content: "PRIOR-USER"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "cache_call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
			{ID: "cache_call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
		}},
		{Role: "tool", ToolCallID: "cache_call_1", Content: "result-a"},
		{Role: "tool", ToolCallID: "cache_call_2", Content: "result-b"},
		{Role: "assistant", Content: "PRIOR-ASSISTANT"},
		{Role: "user", Content: "latest question"},
		{Role: "user", Content: "## Current Time\n2026-10-16 12:00:00Z\nSCRATCHPAD-NOTE"},
	}
	return messages, tools
}

// TestAnthropicBody_MarksAndLifetimes is TDD row 11.
func TestAnthropicBody_MarksAndLifetimes(t *testing.T) {
	messages, tools := nativeMessages()
	_, raw := captureBody(t, "https://api.anthropic.com", messages, tools, "claude-sonnet-4-5")
	body := parseCacheBody(t, raw)
	if err := nativeCacheBodyError(body); err != nil {
		t.Fatalf("FR-005/007: %v\nbody:\n%s", err, raw)
	}
}

// TestDialectBodies_MoonshotAndMiniMax is TDD row 24.
func TestDialectBodies_MoonshotAndMiniMax(t *testing.T) {
	messages, tools := nativeMessages()
	// The agent marks the stable block ephemeral today. Moonshot must not keep that mark.
	messages[0].SystemParts[0].CacheControl = &protocoltypes.CacheControl{Type: "ephemeral"}

	t.Run("moonshot", func(t *testing.T) {
		_, raw := captureBody(t, "https://api.moonshot.ai/anthropic", messages, tools, "kimi-k2")
		if err := moonshotCacheBodyError(parseCacheBody(t, raw)); err != nil {
			t.Fatalf("Moonshot FR-005/028: %v\n%s", err, raw)
		}
	})
	t.Run("minimax", func(t *testing.T) {
		_, raw := captureBody(t, "https://api.minimax.io/anthropic/v1", messages, tools, "MiniMax-M2.7")
		if err := miniMaxCacheBodyError(parseCacheBody(t, raw)); err != nil {
			t.Fatalf("MiniMax FR-005: %v\n%s", err, raw)
		}
	})
}

// TestConnector_ReconstructedCallAndVerbatimModel is TDD row 23.
func TestConnector_ReconstructedCallAndVerbatimModel(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "use the tool"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "toolu_1",
			Function: &FunctionCall{
				Name:      "only_canonical",
				Arguments: `{"city":"SF"}`,
			},
		}}},
		{Role: "user", ToolCallID: "toolu_1", Content: "result-text"},
	}
	_, raw := captureBody(t, "https://api.minimax.io/anthropic/v1", messages, nil, "MiniMax-M2.7")
	text := string(raw)
	if !strings.Contains(text, "MiniMax-M2.7") {
		t.Errorf("model id was rewritten; body does not contain MiniMax-M2.7:\n%s", text)
	}
	if strings.Contains(text, "MiniMax-M2-7") {
		t.Errorf("MiniMax-M2.7 was rewritten to MiniMax-M2-7:\n%s", text)
	}
	for _, want := range []string{"only_canonical", "toolu_1", "result-text", "SF"} {
		if !strings.Contains(text, want) {
			t.Errorf("body missing %q (canonical tool call must be sent intact):\n%s", want, text)
		}
	}
}

// TestAnthropicStream_ReadsCacheUsage is TDD row 12. The cache read is only
// on the opening message_start event.
func TestAnthropicStream_ReadsCacheUsage(t *testing.T) {
	const wantRead = 800
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-5\",\"stop_reason\":null,\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"cache_read_input_tokens\":%d}}}\n\n", wantRead)
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1,\"cache_read_input_tokens\":1}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)
	p := NewProviderWithTokenSourceAndBaseURL("stale", func() (string, error) { return "tok", nil }, srv.URL)
	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-5", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Usage == nil || resp.Usage.CacheReadTokens != wantRead {
		got := 0
		if resp.Usage != nil {
			got = resp.Usage.CacheReadTokens
		}
		t.Fatalf("cache read = %d, want %d from the opening stream event", got, wantRead)
	}
}

// TestSecondCall_CostsLessNotZero is TDD row 19. Prices are the priced-call
// scenario. The first call writes 2,000 tokens. The second reads 8,000.
func TestSecondCall_CostsLessNotZero(t *testing.T) {
	var call int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		read, write := 0, 2000
		uncached := 1000
		if call > 1 {
			read, write, uncached = 8000, 0, 1000
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_%d\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"stop_reason\":null,\"usage\":{\"input_tokens\":%d,\"output_tokens\":0,\"cache_read_input_tokens\":%d,\"cache_creation_input_tokens\":%d}}}\n\n", call, uncached, read, write)
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":500}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)
	p := NewProviderWithTokenSourceAndBaseURL("stale", func() (string, error) { return "tok", nil }, srv.URL)
	price := func(u *UsageInfo) promptcache.PriceResult {
		t.Helper()
		if u == nil {
			t.Fatal("usage is nil")
		}
		return promptcache.Price(promptcache.PriceInput{
			ProviderID: "anthropic",
			Cost:       pricedCallCatalog(),
			Usage: protocoltypes.UsageInfo{
				PromptTokens:     u.PromptTokens,
				CacheReadTokens:  u.CacheReadTokens,
				CacheWriteTokens: u.CacheWriteTokens,
				CompletionTokens: u.CompletionTokens,
			},
		})
	}
	first, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "same"}}, nil, "claude-sonnet-4-5", nil)
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	second, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "same"}}, nil, "claude-sonnet-4-5", nil)
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if second.Usage == nil || second.Usage.CacheReadTokens != 8000 {
		t.Fatalf("second cache read = %v, want 8000", usageRead(second))
	}
	a := price(first.Usage)
	b := price(second.Usage)
	// First: 0.003 + 0 + 0.0075 + 0.0075 = 0.018
	// Second: 0.003 + 0.0024 + 0 + 0.0075 = 0.0129
	if !(b.Cost < a.Cost && b.Cost > 0) {
		t.Fatalf("second cost %v, first cost %v; want the second lower and not zero", b.Cost, a.Cost)
	}
}

func usageRead(resp *LLMResponse) int {
	if resp == nil || resp.Usage == nil {
		return -1
	}
	return resp.Usage.CacheReadTokens
}

func pricedCallCatalog() *catalog.ModelCost {
	f := func(v float64) *float64 { return &v }
	return &catalog.ModelCost{
		Input:      f(3),
		CacheRead:  f(0.30),
		CacheWrite: f(3.75),
		Output:     f(15),
	}
}
