// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_sessions_thinking_usage_test.go is the WP-G Part 4 regression test:
// a turn's provider-reported thinking-token count (D11 usage) must survive
// the whole usage chain — assistant TranscriptEntry.ThinkingTokens, the
// real AppendTranscriptStrict accumulation into SessionStats.ByModel's
// Thinking, and the GET /sessions/{id} serialization (unifiedMetaToGenSession,
// the one converter that handler routes through) — landing on the wire as a
// non-zero "thinking" field for the model that reported it, and producing NO
// "thinking" key at all for a model that reported none (omitempty).
//
// The fixture drives the REAL production write path
// (session.UnifiedStore.AppendTranscriptStrict — the same stand-in for
// post-LLM-call entry writing the U25 tests in rest_stats_adr057_test.go
// established), never a hand-built meta.
package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestSessionByModel_ThinkingTokens_FlowsToSessionWire(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "NewUnifiedStore must succeed")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "agent-thinking-1")
	require.NoError(t, err, "NewSession must succeed")

	// Turn 1: a provider-reported thinking-token count of 20 (what
	// debitLLMUsage debits from UsageInfo.ThinkingTokens onto the entry).
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Role:             "assistant",
		Content:          "answer with thinking",
		Timestamp:        time.Now().UTC(),
		AgentID:          "agent-thinking-1",
		Model:            "test-model",
		Tokens:           150,
		PromptTokens:     100,
		CompletionTokens: 50,
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		ThinkingTokens:   20,
	}), "the thinking turn must append cleanly")

	// Turn 2 (control): zero/absent thinking tokens on another model.
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Role:             "assistant",
		Content:          "answer without thinking",
		Timestamp:        time.Now().UTC(),
		AgentID:          "agent-thinking-1",
		Model:            "no-think-model",
		Tokens:           15,
		PromptTokens:     10,
		CompletionTokens: 5,
	}), "the control turn must append cleanly")

	// The session's accumulated ByModel carries the split per model.
	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err, "GetMeta must succeed")
	withThinking := got.Stats.ByModel["test-model"]
	require.Equal(t, 20, withThinking.Thinking,
		"ByModel must accumulate the entry's ThinkingTokens for the reporting model")
	noThinking := got.Stats.ByModel["no-think-model"]
	require.Equal(t, 0, noThinking.Thinking,
		"the control model must accumulate no thinking tokens")

	// The GET /sessions/{id} wire shape: unifiedMetaToGenSession is the one
	// serializer that handler routes through (rest_sessions.go::getSession).
	wire := unifiedMetaToGenSession(got)
	body, err := json.Marshal(wire)
	require.NoError(t, err, "the generated wire type must marshal")

	var doc struct {
		Stats struct {
			ByModel map[string]struct {
				Thinking *int `json:"thinking"`
			} `json:"by_model"`
		} `json:"stats"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.NotNil(t, doc.Stats.ByModel, "by_model must be present on the wire")

	reporting := doc.Stats.ByModel["test-model"]
	require.NotNil(t, reporting.Thinking,
		"a non-zero thinking count must reach the GET /sessions/{id} body as a present thinking field")
	require.Equal(t, 20, *reporting.Thinking)

	control := doc.Stats.ByModel["no-think-model"]
	require.Nil(t, control.Thinking,
		"omitempty: a model with zero/absent thinking tokens must carry NO thinking key at all")
}
