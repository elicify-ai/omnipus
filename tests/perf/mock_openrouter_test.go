// Mock OpenAI-compatible LLM provider for perf tests.
//
// Replaces the real OpenRouter dependency so the load test and benchmarks
// measure the gateway / channels / WS pipeline rather than network latency
// to a third-party API. Returns deterministic, low-latency responses so
// p95 numbers reflect gateway code paths, not provider rate limits.
//
// Wire-format compatibility: openai_compat/provider.go and common/common.go
// drive the request/response shape. The mock implements both the
// non-streaming JSON path (POST /chat/completions) and the SSE streaming
// path (same endpoint, stream:true) so the gateway's provider stack
// (request marshal, response decode, streaming-chunk parser) is fully
// exercised — only the network and the model latency are removed.

package perf

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// mockOpenRouterServer returns an httptest.Server that responds to
// POST /chat/completions with a canned OpenAI-compatible response.
// Caller is responsible for calling Close() on the returned server
// (the helper registers t.Cleanup so callers don't have to).
//
// The fixed reply is short and deterministic; tests that need a
// specific token count can pass a custom replyText.
func mockOpenRouterServer(tb testing.TB, replyText string) *httptest.Server {
	tb.Helper()

	if replyText == "" {
		replyText = "Mock perf-test reply; deterministic and bounded so p95 reflects gateway latency only."
	}

	var requests atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		// Only POST /chat/completions is honoured. Other endpoints
		// (e.g., /models for capability probes) return 404 — the
		// gateway tolerates this and continues.
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}

		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		if body.Stream {
			writeMockStream(w, replyText)
			return
		}
		writeMockJSON(w, replyText)
	}))

	tb.Cleanup(srv.Close)
	return srv
}

// writeMockJSON returns a single-shot non-streaming response in the
// OpenAI-compatible shape consumed by pkg/providers/common.ReadAndParseResponse.
func writeMockJSON(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"id":      "mock-cmpl-1",
		"object":  "chat.completion",
		"model":   "mock-model",
		"created": 1700000000,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     1,
			"completion_tokens": len(strings.Fields(content)),
			"total_tokens":      1 + len(strings.Fields(content)),
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeMockStream emits an SSE stream that the gateway's
// openai_compat/provider.go ChatStream loop consumes. The stream
// uses ~16-char chunks so streaming code paths see multiple deltas.
func writeMockStream(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	const chunkSize = 16
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}
		chunk := map[string]any{
			"id":      "mock-cmpl-1",
			"object":  "chat.completion.chunk",
			"model":   "mock-model",
			"created": 1700000000,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{
					"content": content[i:end],
				},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Terminator chunk with finish_reason + final usage.
	finalChunk := map[string]any{
		"id":      "mock-cmpl-1",
		"object":  "chat.completion.chunk",
		"model":   "mock-model",
		"created": 1700000000,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     1,
			"completion_tokens": len(strings.Fields(content)),
			"total_tokens":      1 + len(strings.Fields(content)),
		},
	}
	data, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
