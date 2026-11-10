package anthropicprovider

// T2 (success-stream control) — the split 5-minute/1-hour write must survive
// a SUCCESSFUL stream into the usage record that accounting stores.
//
// Install target: pkg/providers/anthropic/ (package anthropicprovider).
//
// Oracles (prompt-caching-spec.md):
//   - BDD usage-shape row "Anthropic exclusive input": uncached prompt is
//     input_tokens as reported (Anthropic's prompt total EXCLUDES cache
//     tokens), so input 100 stays 100.
//   - BDD usage-shape row "Anthropic 5-minute and 1-hour writes": 40 and 60
//     stored separately, write total 100 (FR-027).
//   - Data constraint "Normalized total tokens equal uncached input plus
//     cache read plus cache write plus output": 100 + 800 + 100 + 10 = 1,010.
//   - US-2 acceptance scenario 3: a streamed call still records cache
//     tokens — the recorded record is the exact UsageInfo the agent loop
//     persists, produced by the real ChatStream adapter, not assembled by a
//     test helper.
//
// This is the SUCCESS counterpart of the parent's failure-path split-write
// coverage (TestCachingFixFailedStreamKeepsUsage): that test pins the same
// usage assembly on failed exits; this one pins it when the stream completes.
// Only the SSE transport is fake; the SDK accumulator, restoreOpeningUsage,
// messageUsage and normalization all run for real.

import (
	"context"
	"reflect"
	"testing"
)

func TestExpandedResponses_AnthropicSuccessStream_SplitWritesReachStoredUsage(t *testing.T) {
	// Opening carries the full split: 100 uncached, 800 read, 100 written
	// (40 five-minute + 60 one-hour). The final delta carries output 10 and
	// no cache counters — the stream must not lose the split to the delta.
	const opening = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"expanded\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"fixture\",\"usage\":{\"input_tokens\":100,\"output_tokens\":0,\"cache_read_input_tokens\":800,\"cache_creation_input_tokens\":100,\"cache_creation\":{\"ephemeral_5m_input_tokens\":40,\"ephemeral_1h_input_tokens\":60}}}}\n\n"

	p := anthropicScriptedStream(t,
		opening,
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":10}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	)

	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "fixture", nil, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v (a complete success stream must be an answer)", err)
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("response = %+v, want content %q", resp, "ok")
	}
	if resp.Usage == nil {
		t.Fatal("usage is nil: a successful stream must still record cache tokens (US-2 acceptance scenario 3)")
	}

	// The exact record accounting stores. Every field is asserted by whole-
	// struct equality: a lost split (5m/1h zeroed), a swallowed read, a
	// prompt that grew by the cache tokens, or a lost output all fail here
	// with the full expected-vs-actual visible.
	want := UsageInfo{
		PromptTokens:       100,
		CompletionTokens:   10,
		CacheReadTokens:    800,
		CacheWriteTokens:   100, // 40 + 60 summed, per the documented split row
		CacheWrite5mTokens: 40,
		CacheWrite1hTokens: 60,
		TotalTokens:        1010,
	}
	if !reflect.DeepEqual(*resp.Usage, want) {
		t.Fatalf("stored usage = %+v, want exactly %+v (input 100 uncached, read 800, writes 40+60, output 10)", *resp.Usage, want)
	}
}
