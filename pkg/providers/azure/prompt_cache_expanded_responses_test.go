package azure

// T2 (HTTP half) — nonzero cache usage across the real plain-HTTP Responses
// adapter boundary.
//
// Install target: pkg/providers/azure/ (package azure).
//
// Oracles (prompt-caching-spec.md):
//   - BDD usage-shape row "OpenAI responses details, read and write inside
//     input total" — uncached 100 / read 700 / write 200 — with output 10.
//   - Data constraint "Normalized total tokens equal uncached input plus
//     cache read plus cache write plus output" → 1,010.
//   - Error flow: a missing usage object completes the call and stays
//     distinct from a known zero split (nil usage, not zeros).
//
// Boundary: only the HTTP transport is fake. Provider.Chat's URL join, body
// marshal, response read and openai_responses_common.ParseResponseBody are
// real. Existing azure fixtures all report cached_tokens: 0
// (writeValidResponse), which is the gap this test closes. Helpers are
// reused, not edited.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func expandedResponsesAzureBody(usage map[string]any) []byte {
	body := map[string]any{
		"id":     "resp_expanded_azure",
		"object": "response",
		"status": "completed",
		"output": []map[string]any{
			{
				"type": "message",
				"content": []map[string]any{
					{"type": "output_text", "text": "expanded-azure-ok"},
				},
			},
		},
	}
	if usage != nil {
		body["usage"] = usage
	}
	data, err := json.Marshal(body)
	if err != nil {
		// The fixture is built entirely from strings and integers; Marshal
		// cannot fail for this shape. Panic rather than serve a silently
		// empty response the test would misread as a parser finding.
		panic(err)
	}
	return data
}

func TestExpandedResponses_HTTPBoundary_NonZeroCacheUsageReachesNormalization(t *testing.T) {
	// The path capture crosses from the server's handler goroutine to this
	// test goroutine under a mutex (race-proof plumbing; no client or
	// transport override that could bypass the real Chat path).
	var (
		mu           sync.Mutex
		capturedPath string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(expandedResponsesAzureBody(map[string]any{
			"input_tokens":          1000,
			"output_tokens":         10,
			"total_tokens":          1010,
			"input_tokens_details":  map[string]any{"cached_tokens": 700, "cache_write_tokens": 200},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		}))
	}))
	defer server.Close()

	p := mustNewProvider(t, "expanded-key", server.URL, "")
	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-5.1", nil)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	mu.Lock()
	path := capturedPath
	mu.Unlock()
	if want := "/openai/v1/responses"; path != want {
		t.Errorf("request path = %q, want %q (the Responses endpoint this adapter owns)", path, want)
	}
	if resp.Content != "expanded-azure-ok" {
		t.Errorf("Content = %q, want %q (the call itself must stay intact)", resp.Content, "expanded-azure-ok")
	}
	if resp.Usage == nil {
		t.Fatal("usage is nil: the HTTP Responses boundary lost the usage object entirely")
	}

	// Spec arithmetic: 1,000 inclusive input − 700 read − 200 write = 100
	// uncached; total = 100 + 700 + 200 + 10 = 1,010.
	if resp.Usage.PromptTokens != 100 {
		t.Errorf("uncached prompt = %d, want 100", resp.Usage.PromptTokens)
	}
	if resp.Usage.CacheReadTokens != 700 {
		t.Errorf("cache read = %d, want 700 from input_tokens_details.cached_tokens", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.CacheWriteTokens != 200 {
		t.Errorf("cache write = %d, want 200 from input_tokens_details.cache_write_tokens", resp.Usage.CacheWriteTokens)
	}
	if resp.Usage.CompletionTokens != 10 {
		t.Errorf("output = %d, want 10", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 1010 {
		t.Errorf("total = %d, want 1010 (uncached + read + write + output)", resp.Usage.TotalTokens)
	}
}

// The distinct control for the HTTP boundary: no usage object at all.
func TestExpandedResponses_HTTPBoundary_MissingUsageStaysNilNotZeroSplit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(expandedResponsesAzureBody(nil))
	}))
	defer server.Close()

	p := mustNewProvider(t, "expanded-key", server.URL, "")
	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-5.1", nil)
	if err != nil {
		t.Fatalf("Chat() error: %v (a missing usage object must not fail the call)", err)
	}
	if resp.Content != "expanded-azure-ok" {
		t.Errorf("Content = %q, want %q (the call still completes)", resp.Content, "expanded-azure-ok")
	}
	if resp.Usage != nil {
		t.Fatalf("usage = %+v, want nil: a missing usage object must not be backfilled into a zero split", *resp.Usage)
	}
}
