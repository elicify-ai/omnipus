// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 1 token-usage-tracking: per-provider cache token parsing tests.
//
// BDD scenarios:
//
//	Scenario: ClaudeCliProvider separates cache tokens from plain prompt tokens
//	Scenario: CodexCliProvider separates cached input tokens from plain input tokens
//	Scenario: anthropic_messages parseResponseBody captures cache fields
//
// Guards against: providers collapsing cache tokens into PromptTokens (old behaviour).
// Traces to: docs/internal/specs/token-usage-tracking-2026-06.md §Wave1 item 1

package providers

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/providers/protocoltypes"
)

// TestClaudeCliProvider_CacheTokens_SeparatedFromPrompt verifies that the
// ClaudeCliProvider correctly separates CacheCreationInputTokens and
// CacheReadInputTokens from plain InputTokens, never collapsing them into
// PromptTokens.
//
// BDD:
//
//	Given a claude CLI JSON response with input_tokens=100, cache_creation_input_tokens=50,
//	  cache_read_input_tokens=30, and output_tokens=40,
//	When parseClaudeCliResponse is called,
//	Then PromptTokens==100, CacheWriteTokens==50, CacheReadTokens==30,
//	     CompletionTokens==40, TotalTokens==220.
func TestClaudeCliProvider_CacheTokens_SeparatedFromPrompt(t *testing.T) {
	p := &ClaudeCliProvider{command: "claude", workspace: ""}

	// Construct a claude CLI JSON response with cache fields populated.
	raw := `{
		"type": "result",
		"subtype": "success",
		"is_error": false,
		"result": "Hello world",
		"session_id": "session-abc",
		"total_cost_usd": 0.003,
		"duration_ms": 1000,
		"duration_api_ms": 900,
		"num_turns": 1,
		"usage": {
			"input_tokens": 100,
			"output_tokens": 40,
			"cache_creation_input_tokens": 50,
			"cache_read_input_tokens": 30
		}
	}`

	resp, err := p.parseClaudeCliResponse(raw)
	if err != nil {
		t.Fatalf("parseClaudeCliResponse() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil when the response has token counts")
	}

	// Verify each field against the expected cache convention.
	assertUsage(t, resp.Usage, protocoltypes.UsageInfo{
		PromptTokens:     100,
		CompletionTokens: 40,
		CacheWriteTokens: 50,
		CacheReadTokens:  30,
		TotalTokens:      220, // 100 + 50 + 30 + 40
	})
}

// TestClaudeCliProvider_NoCacheTokens_ZeroCacheFields verifies that a response
// without cache fields produces zero CacheReadTokens and CacheWriteTokens.
//
// BDD:
//
//	Given a claude CLI response with only input_tokens and output_tokens,
//	When parseClaudeCliResponse is called,
//	Then CacheReadTokens==0 and CacheWriteTokens==0.
func TestClaudeCliProvider_NoCacheTokens_ZeroCacheFields(t *testing.T) {
	p := &ClaudeCliProvider{command: "claude", workspace: ""}

	raw := `{
		"type": "result",
		"subtype": "success",
		"is_error": false,
		"result": "No cache here",
		"usage": {
			"input_tokens": 200,
			"output_tokens": 80
		}
	}`

	resp, err := p.parseClaudeCliResponse(raw)
	if err != nil {
		t.Fatalf("parseClaudeCliResponse() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil")
	}

	if resp.Usage.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0 (no cache in response)", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens = %d, want 0 (no cache in response)", resp.Usage.CacheWriteTokens)
	}
	if resp.Usage.PromptTokens != 200 {
		t.Errorf("PromptTokens = %d, want 200", resp.Usage.PromptTokens)
	}
	if resp.Usage.TotalTokens != 280 {
		t.Errorf("TotalTokens = %d, want 280 (200+80)", resp.Usage.TotalTokens)
	}
}

// TestCodexCliProvider_CacheTokens_SeparatedFromPrompt verifies that the
// CodexCliProvider separates CachedInputTokens from plain InputTokens.
//
// BDD:
//
//	Given a codex turn.completed event with input_tokens=150, cached_input_tokens=60,
//	  output_tokens=35,
//	When parseJSONLEvents is called,
//	Then PromptTokens==150, CacheReadTokens==60, CacheWriteTokens==0,
//	     TotalTokens==245.
func TestCodexCliProvider_CacheTokens_SeparatedFromPrompt(t *testing.T) {
	p := &CodexCliProvider{command: "codex", workspace: ""}

	jsonlOutput := `{"type":"item.completed","item":{"id":"msg-1","type":"agent_message","text":"Cached result"}}
{"type":"turn.completed","usage":{"input_tokens":150,"cached_input_tokens":60,"output_tokens":35}}`

	resp, err := p.parseJSONLEvents(jsonlOutput)
	if err != nil {
		t.Fatalf("parseJSONLEvents() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil when turn.completed has usage")
	}

	assertUsage(t, resp.Usage, protocoltypes.UsageInfo{
		PromptTokens:     150,
		CompletionTokens: 35,
		CacheReadTokens:  60,
		CacheWriteTokens: 0,   // codex doesn't report cache writes
		TotalTokens:      245, // 150 + 60 + 35
	})
}

// TestCodexCliProvider_NoCacheTokens_ZeroCacheFields verifies that a codex
// response without cache produces zero CacheReadTokens.
func TestCodexCliProvider_NoCacheTokens_ZeroCacheFields(t *testing.T) {
	p := &CodexCliProvider{command: "codex", workspace: ""}

	jsonlOutput := `{"type":"item.completed","item":{"id":"msg-1","type":"agent_message","text":"Plain"}}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":25}}`

	resp, err := p.parseJSONLEvents(jsonlOutput)
	if err != nil {
		t.Fatalf("parseJSONLEvents() error = %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage must be non-nil")
	}

	if resp.Usage.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.TotalTokens != 125 {
		t.Errorf("TotalTokens = %d, want 125 (100+25)", resp.Usage.TotalTokens)
	}
}

// assertUsage compares each field of a UsageInfo against expected values.
func assertUsage(t *testing.T, got *protocoltypes.UsageInfo, want protocoltypes.UsageInfo) {
	t.Helper()
	if got.PromptTokens != want.PromptTokens {
		t.Errorf("PromptTokens = %d, want %d", got.PromptTokens, want.PromptTokens)
	}
	if got.CompletionTokens != want.CompletionTokens {
		t.Errorf("CompletionTokens = %d, want %d", got.CompletionTokens, want.CompletionTokens)
	}
	if got.CacheReadTokens != want.CacheReadTokens {
		t.Errorf("CacheReadTokens = %d, want %d", got.CacheReadTokens, want.CacheReadTokens)
	}
	if got.CacheWriteTokens != want.CacheWriteTokens {
		t.Errorf("CacheWriteTokens = %d, want %d", got.CacheWriteTokens, want.CacheWriteTokens)
	}
	if got.TotalTokens != want.TotalTokens {
		t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, want.TotalTokens)
	}
}
