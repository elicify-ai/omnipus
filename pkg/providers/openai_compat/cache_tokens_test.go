// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 1 token-usage-tracking: OpenAI-compat provider cache token parsing tests.
//
// BDD scenarios:
//
//	Scenario: streaming usage chunk with prompt_tokens_details.cached_tokens
//	  produces PromptTokens = plain input only, CacheReadTokens = cached portion
//	Scenario: non-streaming response with prompt_tokens_details.cached_tokens
//	  produces the same split
//
// Guards against: cached tokens being counted twice (once in PromptTokens and
// once in CacheReadTokens) or being silently discarded.
// Traces to: docs/internal/specs/token-usage-tracking-2026-06.md §Wave1 item 1

package openai_compat

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestChatStream_CachedTokensSeparatedFromPrompt verifies that a streaming usage
// chunk containing prompt_tokens_details.cached_tokens correctly sets
// CacheReadTokens and subtracts cached tokens from PromptTokens so they are
// not double-counted.
//
// BDD:
//
//	Given a streaming response where the final usage chunk has
//	  prompt_tokens=200, completion_tokens=50,
//	  prompt_tokens_details.cached_tokens=80,
//	When ChatStream returns,
//	Then PromptTokens==120 (200-80), CacheReadTokens==80, TotalTokens==250.
func TestChatStream_CachedTokensSeparatedFromPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)

		// Content chunk.
		contentChunk := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": "Cached answer"}, "finish_reason": nil},
			},
		}
		contentJSON, _ := json.Marshal(contentChunk)
		_, _ = bw.WriteString("data: " + string(contentJSON) + "\n\n")

		// Finish chunk.
		finishChunk := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{}, "finish_reason": "stop"},
			},
		}
		finishJSON, _ := json.Marshal(finishChunk)
		_, _ = bw.WriteString("data: " + string(finishJSON) + "\n\n")

		// Usage chunk with prompt_tokens_details.
		usageChunk := map[string]any{
			"choices": []map[string]any{},
			"usage": map[string]any{
				"prompt_tokens":     200,
				"completion_tokens": 50,
				"total_tokens":      250,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": 80,
				},
			},
		}
		usageJSON, _ := json.Marshal(usageChunk)
		_, _ = bw.WriteString("data: " + string(usageJSON) + "\n\n")

		_, _ = bw.WriteString("data: [DONE]\n\n")
		bw.Flush()
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "What is cached?"}},
		nil,
		"gpt-4o",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil when stream includes usage chunk")
	}

	// PromptTokens should be uncached only (200 - 80 = 120).
	if resp.Usage.PromptTokens != 120 {
		t.Errorf("PromptTokens = %d, want 120 (200 total - 80 cached)", resp.Usage.PromptTokens)
	}
	// CacheReadTokens should carry the cached portion.
	if resp.Usage.CacheReadTokens != 80 {
		t.Errorf("CacheReadTokens = %d, want 80", resp.Usage.CacheReadTokens)
	}
	// CacheWriteTokens is not reported by OpenAI.
	if resp.Usage.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens = %d, want 0 (OpenAI does not report writes)", resp.Usage.CacheWriteTokens)
	}
	// TotalTokens should still be the full amount from the API.
	if resp.Usage.TotalTokens != 250 {
		t.Errorf("TotalTokens = %d, want 250", resp.Usage.TotalTokens)
	}
	// CompletionTokens unchanged.
	if resp.Usage.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", resp.Usage.CompletionTokens)
	}
}

// TestChatStream_NoCachedTokens_PromptTokensUnchanged verifies that when
// no prompt_tokens_details is present, PromptTokens is not modified and
// CacheReadTokens is 0.
//
// BDD:
//
//	Given a streaming usage chunk with no prompt_tokens_details,
//	When ChatStream returns,
//	Then PromptTokens equals the raw prompt_tokens value, CacheReadTokens==0.
func TestChatStream_NoCachedTokens_PromptTokensUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		bw := bufio.NewWriter(w)

		finishChunk := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": "No cache"}, "finish_reason": "stop"},
			},
		}
		finishJSON, _ := json.Marshal(finishChunk)
		_, _ = bw.WriteString("data: " + string(finishJSON) + "\n\n")

		usageChunk := map[string]any{
			"choices": []map[string]any{},
			"usage": map[string]any{
				"prompt_tokens":     150,
				"completion_tokens": 30,
				"total_tokens":      180,
			},
		}
		usageJSON, _ := json.Marshal(usageChunk)
		_, _ = bw.WriteString("data: " + string(usageJSON) + "\n\n")

		_, _ = bw.WriteString("data: [DONE]\n\n")
		bw.Flush()
	}))
	defer server.Close()

	p := mustNewProvider(t, "key", server.URL, "")

	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "No cache"}},
		nil, "gpt-4o", nil, nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil")
	}

	if resp.Usage.PromptTokens != 150 {
		t.Errorf("PromptTokens = %d, want 150 (unchanged when no cached tokens)", resp.Usage.PromptTokens)
	}
	if resp.Usage.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.TotalTokens != 180 {
		t.Errorf("TotalTokens = %d, want 180", resp.Usage.TotalTokens)
	}
}
