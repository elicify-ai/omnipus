package providers

// T2 — nonzero cache usage across the real Responses adapter boundary.
//
// Install target: pkg/providers/ (package providers).
//
// Oracles (prompt-caching-spec.md):
//   - BDD "Each published usage shape", row "OpenAI responses details, read
//     and write inside input total": uncached 100 / read 700 / write 200.
//     The fixture's input_tokens 1,000 is INCLUSIVE of the 700 read and the
//     200 write; output 10 is the same row's completion count.
//   - Machine-verifiable data constraint: "Normalized total tokens equal
//     uncached input plus cache read plus cache write plus output" →
//     100 + 700 + 200 + 10 = 1,010.
//   - Error flow: "The documented counters are absent" — a missing usage
//     object is not a known zero split (ParseUsage returns nil usage; the
//     call still completes). This control is asserted as nil, never as a
//     zero-valued UsageInfo.
//
// Boundary: only the HTTP/SSE transport is fake (an httptest server playing a
// recorded response.completed event). The SDK stream decoder, CodexProvider,
// openai_responses_common.parseResponse and promptcache normalization are all
// real. Existing round-trip fixtures (TestCodexProvider_ChatRoundTrip and
// TestParseResponseBody_TextOutput) all supply cached_tokens: 0, which is the
// gap this test closes; helpers are reused, not edited.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// expandedResponsesUsageFixture is the spec's Responses usage shape. Values
// trace to the BDD row named in the header comment, not to observed output.
func expandedResponsesUsageFixture() map[string]any {
	return map[string]any{
		"input_tokens":          1000,
		"output_tokens":         10,
		"total_tokens":          1010,
		"input_tokens_details":  map[string]any{"cached_tokens": 700, "cache_write_tokens": 200},
		"output_tokens_details": map[string]any{"reasoning_tokens": 0},
	}
}

func expandedResponsesCompletedBody(usage map[string]any) map[string]any {
	body := map[string]any{
		"id":     "resp_expanded_boundary",
		"object": "response",
		"status": "completed",
		"output": []map[string]any{
			{
				"id":     "msg_expanded_boundary",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{
					{"type": "output_text", "text": "expanded-boundary-ok"},
				},
			},
		},
	}
	if usage != nil {
		body["usage"] = usage
	}
	return body
}

func expandedResponsesCodexProvider(t *testing.T, serverURL string) *CodexProvider {
	t.Helper()
	provider := NewCodexProvider("expanded-token", "acc-expanded")
	provider.client = createOpenAITestClient(serverURL, "expanded-token", "acc-expanded")
	return provider
}

func TestExpandedResponses_CodexSSEBoundary_NonZeroCacheUsageReachesNormalization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
			return
		}
		writeCompletedSSE(w, expandedResponsesCompletedBody(expandedResponsesUsageFixture()))
	}))
	defer server.Close()

	provider := expandedResponsesCodexProvider(t, server.URL)
	resp, err := provider.Chat(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-5.3",
		nil,
	)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "expanded-boundary-ok" {
		t.Errorf("Content = %q, want %q (the call itself must stay intact)", resp.Content, "expanded-boundary-ok")
	}
	if resp.Usage == nil {
		t.Fatal("usage is nil: the Responses SDK-to-normalizer handoff lost the usage object entirely")
	}

	// Spec arithmetic, asserted per field so a failure names the lost counter:
	// 1,000 inclusive input − 700 read − 200 write = 100 uncached;
	// total = 100 + 700 + 200 + 10 = 1,010.
	const (
		wantPrompt    = 100
		wantRead      = 700
		wantWrite     = 200
		wantWriteSum  = 200
		want5m        = 0 // Responses shapes carry no Anthropic lifetime split.
		want1h        = 0
		wantCompleted = 10
		wantTotal     = 1010
	)
	if resp.Usage.PromptTokens != wantPrompt {
		t.Errorf("uncached prompt = %d, want %d (input_tokens 1000 minus the read and the write)", resp.Usage.PromptTokens, wantPrompt)
	}
	if resp.Usage.CacheReadTokens != wantRead {
		t.Errorf("cache read = %d, want %d from input_tokens_details.cached_tokens", resp.Usage.CacheReadTokens, wantRead)
	}
	if resp.Usage.CacheWriteTokens != wantWrite {
		t.Errorf("cache write = %d, want %d from input_tokens_details.cache_write_tokens", resp.Usage.CacheWriteTokens, wantWrite)
	}
	if resp.Usage.CacheWrite5mTokens != want5m || resp.Usage.CacheWrite1hTokens != want1h {
		t.Errorf("lifetime split = %d/%d, want 0/0 (a Responses shape has no Anthropic split)", resp.Usage.CacheWrite5mTokens, resp.Usage.CacheWrite1hTokens)
	}
	if resp.Usage.CompletionTokens != wantCompleted {
		t.Errorf("output = %d, want %d", resp.Usage.CompletionTokens, wantCompleted)
	}
	if resp.Usage.TotalTokens != wantTotal {
		t.Errorf("total = %d, want %d (uncached + read + write + output)", resp.Usage.TotalTokens, wantTotal)
	}
}

// The distinct control: a completed response that carries NO usage object at
// all. The call still completes and the usage stays nil — a missing usage
// object must not be backfilled into a known zero split (spec error flow and
// FR-011's "distinct from a missing usage object").
func TestExpandedResponses_CodexSSEBoundary_MissingUsageStaysNilNotZeroSplit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
			return
		}
		writeCompletedSSE(w, expandedResponsesCompletedBody(nil))
	}))
	defer server.Close()

	provider := expandedResponsesCodexProvider(t, server.URL)
	resp, err := provider.Chat(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-5.3",
		nil,
	)
	if err != nil {
		t.Fatalf("Chat() error: %v (a missing usage object must not fail the call)", err)
	}
	if resp.Content != "expanded-boundary-ok" {
		t.Errorf("Content = %q, want %q (the call still completes)", resp.Content, "expanded-boundary-ok")
	}
	if resp.Usage != nil {
		t.Fatalf("usage = %+v, want nil: a missing usage object is distinct from a known zero split and must not be backfilled", *resp.Usage)
	}
}
