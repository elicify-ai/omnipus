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
	_, _ = p.Chat(t.Context(), messages, tools, model, map[string]any{"max_tokens": 64})
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

func cacheMarks(v any) []map[string]any {
	var out []map[string]any
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if cc, ok := n["cache_control"].(map[string]any); ok {
				out = append(out, cc)
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(v)
	return out
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
		{Role: "assistant", Content: "PRIOR-ASSISTANT"},
		{Role: "user", Content: "latest question"},
	}
	return messages, tools
}

// TestAnthropicBody_MarksAndLifetimes is TDD row 11.
func TestAnthropicBody_MarksAndLifetimes(t *testing.T) {
	messages, tools := nativeMessages()
	body, _ := captureBody(t, "https://api.anthropic.com", messages, tools, "claude-sonnet-4-5")
	marks := cacheMarks(body)
	if len(marks) == 0 || len(marks) > 4 {
		t.Fatalf("cache marks = %d (%v), want 1 to 4", len(marks), marks)
	}
	var saw5m bool
	for _, mark := range marks {
		ttl, _ := mark["ttl"].(string)
		if ttl == "5m" {
			saw5m = true
			continue
		}
		if saw5m && ttl == "1h" {
			t.Fatalf("a 1-hour mark appears after a 5-minute mark: %v", marks)
		}
		if ttl != "1h" {
			t.Fatalf("mark = %v, want ttl 1h on tools and instructions, then 5m on the tail", mark)
		}
	}
	raw, _ := json.Marshal(body)
	text := string(raw)
	if !strings.Contains(text, `"ttl":"1h"`) && !strings.Contains(text, `"ttl": "1h"`) {
		t.Fatalf("body has no 1-hour mark:\n%s", text)
	}
	if !strings.Contains(text, `"ttl":"5m"`) && !strings.Contains(text, `"ttl": "5m"`) {
		t.Fatalf("body has no 5-minute tail mark:\n%s", text)
	}
}

// TestDialectBodies_MoonshotAndMiniMax is TDD row 24.
func TestDialectBodies_MoonshotAndMiniMax(t *testing.T) {
	messages, tools := nativeMessages()
	// The agent marks the stable block ephemeral today. Moonshot must not keep that mark.
	messages[0].SystemParts[0].CacheControl = &protocoltypes.CacheControl{Type: "ephemeral"}

	t.Run("moonshot", func(t *testing.T) {
		_, raw := captureBody(t, "https://api.moonshot.ai/anthropic", messages, tools, "kimi-k2")
		text := string(raw)
		if strings.Contains(text, "cache_control") || strings.Contains(text, `"1h"`) || strings.Contains(text, "ttl") {
			t.Fatalf("Moonshot body has a cache mark or a 1-hour option:\n%s", text)
		}
	})
	t.Run("minimax", func(t *testing.T) {
		_, raw := captureBody(t, "https://api.minimax.io/anthropic/v1", messages, tools, "MiniMax-M2.7")
		text := string(raw)
		if strings.Contains(text, `"1h"`) {
			t.Fatalf("MiniMax body has a 1-hour mark:\n%s", text)
		}
		if strings.Contains(text, "cache_control") && !strings.Contains(text, `"5m"`) {
			t.Fatalf("MiniMax mark is not its documented 5-minute mode and not automatic (no mark):\n%s", text)
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
