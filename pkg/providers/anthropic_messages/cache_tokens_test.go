// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 1 token-usage-tracking: anthropic_messages provider cache token parsing tests.
//
// BDD scenario:
//
//	Given an Anthropic Messages API response with cache_creation_input_tokens and
//	  cache_read_input_tokens populated,
//	When parseResponseBody is called,
//	Then PromptTokens holds plain (uncached) input, CacheWriteTokens holds creation
//	  tokens, CacheReadTokens holds read tokens, and TotalTokens is their sum.
//
// Guards against: cache fields being silently ignored in parseResponseBody.
// Traces to: docs/internal/specs/token-usage-tracking-2026-06.md §Wave1 item 1

package anthropicmessages

import (
	"encoding/json"
	"testing"
)

// TestParseResponseBody_CacheTokens_SeparatedFromPrompt verifies that
// cache_creation_input_tokens and cache_read_input_tokens populate the
// CacheWriteTokens and CacheReadTokens fields respectively, and that
// PromptTokens holds only the plain (uncached) input_tokens.
//
// BDD:
//
//	Given an Anthropic API response with input_tokens=100,
//	  cache_creation_input_tokens=60, cache_read_input_tokens=40, output_tokens=50,
//	When parseResponseBody is called,
//	Then PromptTokens==100, CacheWriteTokens==60, CacheReadTokens==40,
//	     CompletionTokens==50, TotalTokens==250.
func TestParseResponseBody_CacheTokens_SeparatedFromPrompt(t *testing.T) {
	apiResp := anthropicMessageResponse{
		ID:         "msg-cache-test",
		Type:       "message",
		Role:       "assistant",
		StopReason: "end_turn",
		Content: []contentBlock{
			{Type: "text", Text: "Hello with cache"},
		},
		Usage: usageInfo{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 60,
			CacheReadInputTokens:     40,
		},
	}

	body, err := json.Marshal(apiResp)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	resp, err := parseResponseBody(body)
	if err != nil {
		t.Fatalf("parseResponseBody() error = %v", err)
	}

	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil")
	}

	cases := []struct {
		name string
		got  int
		want int
	}{
		{"PromptTokens", resp.Usage.PromptTokens, 100},
		{"CompletionTokens", resp.Usage.CompletionTokens, 50},
		{"CacheWriteTokens", resp.Usage.CacheWriteTokens, 60},
		{"CacheReadTokens", resp.Usage.CacheReadTokens, 40},
		{"TotalTokens", resp.Usage.TotalTokens, 250},
	}

	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// TestParseResponseBody_NoCacheTokens_ZeroCacheFields verifies that a normal
// response without cache fields produces zero CacheReadTokens and CacheWriteTokens.
func TestParseResponseBody_NoCacheTokens_ZeroCacheFields(t *testing.T) {
	apiResp := anthropicMessageResponse{
		ID:         "msg-no-cache",
		Type:       "message",
		Role:       "assistant",
		StopReason: "end_turn",
		Content:    []contentBlock{{Type: "text", Text: "No cache"}},
		Usage: usageInfo{
			InputTokens:  200,
			OutputTokens: 80,
		},
	}

	body, _ := json.Marshal(apiResp)
	resp, err := parseResponseBody(body)
	if err != nil {
		t.Fatalf("parseResponseBody() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil")
	}
	if resp.Usage.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens = %d, want 0", resp.Usage.CacheWriteTokens)
	}
	if resp.Usage.TotalTokens != 280 {
		t.Errorf("TotalTokens = %d, want 280 (200+80)", resp.Usage.TotalTokens)
	}
}
