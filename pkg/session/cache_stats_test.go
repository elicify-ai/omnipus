// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 1 token-usage-tracking: SessionStats cache + ByModel accumulation tests.
//
// BDD scenarios:
//
//	Scenario: assistant entry with cache tokens accumulates TokensCacheRead/Write
//	Scenario: two models accumulate into separate ByModel buckets
//	Scenario: external CLI (subagent_3p) turn contributes zero cache tokens
//
// Guards against:
//   - Cache tokens silently ignored on transcript append
//   - ByModel not populated when model is set on the entry
//   - External CLI turns inflating SessionStats
//
// Traces to: docs/internal/specs/token-usage-tracking-2026-06.md §Wave1 items 2, 3

package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonUnmarshal is an alias for json.Unmarshal used in tests for legibility.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// TestAppendTranscript_CacheTokens_AccumulatesInStats verifies that an assistant
// TranscriptEntry with CacheReadTokens and CacheWriteTokens populated correctly
// updates SessionStats.TokensCacheRead and TokensCacheWrite.
//
// BDD:
//
//	Given a fresh session,
//	When an assistant entry with Tokens=200, CacheReadTokens=50, CacheWriteTokens=20
//	  is appended,
//	Then Stats.TokensCacheRead==50 and Stats.TokensCacheWrite==20.
func TestAppendTranscript_CacheTokens_AccumulatesInStats(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	entry := TranscriptEntry{
		ID:               "cache-entry-001",
		Role:             "assistant",
		Content:          "Cached response",
		Tokens:           200,
		CacheReadTokens:  50,
		CacheWriteTokens: 20,
		Timestamp:        time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, entry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 50, got.Stats.TokensCacheRead,
		"TokensCacheRead must equal the entry's CacheReadTokens")
	assert.Equal(t, 20, got.Stats.TokensCacheWrite,
		"TokensCacheWrite must equal the entry's CacheWriteTokens")
	assert.Equal(t, 200, got.Stats.TokensOut,
		"TokensOut must equal the full turn total (Tokens field)")
}

// TestAppendTranscript_ByModel_AccumulatesPerModel verifies that two assistant
// entries with different Model fields produce separate entries in ByModel.
//
// BDD:
//
//	Given two assistant entries using model "claude-sonnet-4-6" and "glm-5.2",
//	When both are appended,
//	Then Stats.ByModel["claude-sonnet-4-6"].Total==150 and
//	     Stats.ByModel["glm-5.2"].Total==80.
func TestAppendTranscript_ByModel_AccumulatesPerModel(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	entry1 := TranscriptEntry{
		ID:               "bymodel-001",
		Role:             "assistant",
		Content:          "Claude response",
		Model:            "claude-sonnet-4-6",
		Tokens:           150,
		CacheReadTokens:  30,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}
	entry2 := TranscriptEntry{
		ID:               "bymodel-002",
		Role:             "assistant",
		Content:          "GLM response",
		Model:            "glm-5.2",
		Tokens:           80,
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, entry1))
	require.NoError(t, store.AppendTranscript(meta.ID, entry2))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	require.NotNil(t, got.Stats.ByModel, "ByModel must be non-nil after entries with Model set")
	require.Contains(t, got.Stats.ByModel, "claude-sonnet-4-6",
		"ByModel must contain an entry for claude-sonnet-4-6")
	require.Contains(t, got.Stats.ByModel, "glm-5.2",
		"ByModel must contain an entry for glm-5.2")

	claudeEntry := got.Stats.ByModel["claude-sonnet-4-6"]
	assert.Equal(t, 150, claudeEntry.Total,
		"claude-sonnet-4-6 total must equal Tokens from entry1")
	assert.Equal(t, 30, claudeEntry.CacheRead,
		"claude-sonnet-4-6 CacheRead must equal CacheReadTokens from entry1")

	glmEntry := got.Stats.ByModel["glm-5.2"]
	assert.Equal(t, 80, glmEntry.Total,
		"glm-5.2 total must equal Tokens from entry2")
}

// TestAppendTranscript_ExternalCLITurn_ZeroTokenContribution verifies the scope
// guard: an assistant entry produced by an external CLI sub-agent (which never
// calls AddTurnStats/AddTurnCacheStats) has all zero token fields, and appending
// it does NOT inflate SessionStats beyond zero for token-related fields.
//
// BDD:
//
//	Given an assistant TranscriptEntry with Tokens==0 and all cache fields==0
//	  (simulating a subagent_3p turn that produced text but no native LLM usage),
//	When the entry is appended,
//	Then Stats.TokensTotal==0, TokensCacheRead==0, TokensCacheWrite==0.
//
// Guards against: external CLI turns accidentally inflating token counts.
// Traces to: token-usage-tracking-2026-06.md §Wave1 item 3 (scope guard)
func TestAppendTranscript_ExternalCLITurn_ZeroTokenContribution(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	// An external CLI sub-agent always writes entries with Tokens=0
	// because AddTurnStats is never called for external-cli dispatch.
	extEntry := TranscriptEntry{
		ID:               "ext-cli-001",
		Role:             "assistant",
		Content:          "Result from external CLI agent",
		Model:            "claude-code", // model label from the external CLI
		Tokens:           0,             // zero — no native engine was used
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, extEntry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 0, got.Stats.TokensTotal,
		"TokensTotal must be 0 — external CLI turn contributes no usage")
	assert.Equal(t, 0, got.Stats.TokensCacheRead,
		"TokensCacheRead must be 0 — external CLI turn contributes no cache")
	assert.Equal(t, 0, got.Stats.TokensCacheWrite,
		"TokensCacheWrite must be 0 — external CLI turn contributes no cache")
}

// TestAppendTranscript_TwoCacheAppendsSum verifies that two sequential assistant
// entries with cache tokens sum their cache counts (not replace/reset).
//
// BDD:
//
//	Given two assistant entries, both with CacheReadTokens,
//	When both are appended,
//	Then Stats.TokensCacheRead equals the sum of both entries' CacheReadTokens.
func TestAppendTranscript_TwoCacheAppendsSum(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	ts := time.Now().UTC()

	e1 := TranscriptEntry{
		ID:              "cache-sum-001",
		Role:            "assistant",
		Content:         "First cached",
		Tokens:          100,
		CacheReadTokens: 40,
		Timestamp:       ts,
	}
	e2 := TranscriptEntry{
		ID:              "cache-sum-002",
		Role:            "assistant",
		Content:         "Second cached",
		Tokens:          90,
		CacheReadTokens: 35,
		Timestamp:       ts,
	}

	require.NoError(t, store.AppendTranscript(meta.ID, e1))
	require.NoError(t, store.AppendTranscript(meta.ID, e2))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 75, got.Stats.TokensCacheRead,
		"TokensCacheRead must sum both entries (40+35)")
	assert.Equal(t, 190, got.Stats.TokensTotal,
		"TokensTotal must sum both entries (100+90)")
}

// TestSessionStats_BackwardCompat_LegacyMetaLoadsWithZeroCacheFields verifies
// that a SessionStats without by_model/cache fields (old JSON) is treated as zero
// and doesn't panic.
//
// BDD:
//
//	Given a SessionStats JSON without tokens_cache_read, tokens_cache_write, by_model,
//	When it is unmarshalled,
//	Then TokensCacheRead==0, TokensCacheWrite==0, ByModel==nil.
func TestSessionStats_BackwardCompat_LegacyMetaLoadsWithZeroCacheFields(t *testing.T) {
	legacyJSON := []byte(`{"tokens_in":500,"tokens_out":200,"tokens_total":700,"cost":0.01,"tool_calls":2,"message_count":5}`)
	var stats SessionStats
	require.NoError(t, jsonUnmarshal(legacyJSON, &stats))
	assert.Equal(t, 500, stats.TokensIn)
	assert.Equal(t, 200, stats.TokensOut)
	assert.Equal(t, 0, stats.TokensCacheRead, "legacy JSON without cache fields must load as 0")
	assert.Equal(t, 0, stats.TokensCacheWrite, "legacy JSON without cache fields must load as 0")
	assert.Nil(t, stats.ByModel, "legacy JSON without by_model must load as nil")
}
