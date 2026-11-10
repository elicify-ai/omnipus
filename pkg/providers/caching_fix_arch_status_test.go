package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ARCH-01 / CR-01: exact production factory→SDK→fallback behavior, not a
// classifier helper fed an invented status. A healthy primary never falls back.
func TestCachingFixFactoryHTTPStatusReachesFallback(t *testing.T) {
	withFixtureCatalog(t)
	for _, status := range []int{200, 404, 503} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("status%d/stream%t", status, streaming), func(t *testing.T) {
				var primaryCalls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					primaryCalls.Add(1)
					w.Header().Set("Request-Id", "factory-status-fixture")
					if status != 200 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						// Neutral body: neither auth/billing nor a retry keyword can
						// substitute for reading the structured HTTP status.
						_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"fixture rejected"}}`)
						return
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"MiniMax-M2.7\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"PRIMARY\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"fixture","type":"message","role":"assistant","model":"MiniMax-M2.7","content":[{"type":"text","text":"PRIMARY"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
				}))
				t.Cleanup(srv.Close)
				p, model, err := CreateProviderFromConfig(&config.ModelConfig{Provider: "minimax", Model: "MiniMax-M2.7", APIBase: srv.URL + "/v1", APIKeyRef: keyRef(t, "ARCH_STATUS_KEY")})
				if err != nil {
					t.Fatal(err)
				}
				fallbackCalls := 0
				result, err := NewFallbackChain(NewCooldownTracker()).Execute(t.Context(), []FallbackCandidate{{Provider: "minimax", Model: model}, {Provider: "fallback-fixture", Model: "fallback-model"}}, func(ctx context.Context, provider, model string) (*LLMResponse, error) {
					if provider == "fallback-fixture" {
						fallbackCalls++
						return &LLMResponse{Content: "FALLBACK", FinishReason: "stop"}, nil
					}
					if streaming {
						sp, ok := p.(StreamingProvider)
						if !ok {
							t.Fatal("factory connector lost streaming capability")
						}
						return sp.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, model, map[string]any{"max_tokens": 32}, nil, nil)
					}
					return p.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil, model, map[string]any{"max_tokens": 32})
				})
				if err != nil || result == nil || result.Response == nil {
					t.Fatalf("status%d should resolve through real chain: result=%+v err=%v", status, result, err)
				}
				want, wantFallback := "PRIMARY", 0
				if status != 200 {
					want, wantFallback = "FALLBACK", 1
				}
				if result.Response.Content != want || fallbackCalls != wantFallback || primaryCalls.Load() != 1 {
					t.Fatalf("status%d response=%q primarycalls=%d fallbackcalls=%d, want%s/1/%d", status, result.Response.Content, primaryCalls.Load(), fallbackCalls, want, wantFallback)
				}
			})
		}
	}
}
