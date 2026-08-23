// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 1 token-usage-tracking: per-provider cache token parsing tests.
//
// BDD scenarios:
//
//	Scenario: CodexCliProvider separates cached input tokens from plain input tokens
//	Scenario: anthropic_messages parseResponseBody captures cache fields
//
// Guards against: providers collapsing cache tokens into PromptTokens (old behavior).
// Traces to: docs/internal/specs/token-usage-tracking-2026-06.md §Wave1 item 1

package providers

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

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
