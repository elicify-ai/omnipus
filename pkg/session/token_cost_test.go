// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for #411 token/cost accumulation in UnifiedStore.AppendTranscript.
//
// BDD scenarios:
//   Scenario: assistant entry accumulates non-zero TokensOut/TokensTotal/Cost
//   Scenario: two appends sum correctly (not double)
//
// Traces to: project-task-management-level1-spec.md — #411 token/cost fix

package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppendTranscript_AssistantEntry_StatsNonZero asserts that appending a single
// assistant TranscriptEntry with Tokens=120 and Cost=0.0034 causes the session
// Stats.TokensOut, Stats.TokensTotal, and Stats.Cost to all be non-zero.
//
// BDD:
//
//	Given a fresh session,
//	When an assistant TranscriptEntry{Tokens:120, Cost:0.0034} is appended,
//	Then Stats.TokensOut==120, Stats.TokensTotal==120, Stats.Cost≈0.0034.
//
// Guards against: AppendTranscript silently discarding token/cost fields.
// Traces to: pkg/session/unified.go AppendTranscript stats accumulation — #411
func TestAppendTranscript_AssistantEntry_StatsNonZero(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// No PromptTokens/CompletionTokens set — this is the legacy no-split
	// shape (see accumulateEntryStats in entry_stats.go). It is NOT true in
	// general that an assistant entry leaves TokensIn at 0: an entry that
	// DOES carry the provider's input/output split (PromptTokens > 0)
	// contributes to TokensIn — see TestAppendTranscript_RecordsInputOutputSplit
	// in token_split_test.go for that case.
	entry := TranscriptEntry{
		ID:        "entry-001",
		Role:      "assistant",
		Content:   "Hello, how can I help?",
		Tokens:    120,
		Cost:      0.0034,
		Timestamp: time.Now().UTC(),
	}

	err = store.AppendTranscript(sessionID, entry)
	require.NoError(t, err, "AppendTranscript must succeed")

	got, err := store.GetMeta(sessionID)
	require.NoError(t, err, "GetMeta must succeed after AppendTranscript")

	assert.Equal(t, 120, got.Stats.TokensOut,
		"TokensOut must equal the assistant entry Tokens")
	assert.Equal(t, 0, got.Stats.TokensIn,
		"TokensIn stays 0 here because this entry carries no PromptTokens/CompletionTokens split "+
			"(the legacy no-split fallback), not because assistant entries never affect TokensIn")
	assert.Equal(t, 120, got.Stats.TokensTotal,
		"TokensTotal must equal the assistant entry Tokens")
	assert.InDelta(t, 0.0034, got.Stats.Cost, 1e-9,
		"Cost must equal the assistant entry Cost")
	assert.Equal(t, 1, got.Stats.MessageCount,
		"MessageCount must be 1 after one assistant entry")
}

// TestAppendTranscript_TwoAppends_StatsSumNotDouble asserts that appending two
// separate assistant entries correctly sums their stats — proving the accumulator
// adds rather than replaces or doubles.
//
// BDD:
//
//	Given a session with Stats.TokensOut==120 from a prior append,
//	When a second assistant entry with Tokens=80, Cost=0.0021 is appended,
//	Then Stats.TokensOut==200, Stats.TokensTotal==200, Stats.Cost≈0.0055.
//
// Guards against: stats reset or double-counting on each append.
// Traces to: pkg/session/unified.go AppendTranscript stats accumulation — #411
func TestAppendTranscript_TwoAppends_StatsSumNotDouble(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	entry1 := TranscriptEntry{
		ID:        "entry-001",
		Role:      "assistant",
		Content:   "First reply",
		Tokens:    120,
		Cost:      0.0034,
		Timestamp: time.Now().UTC(),
	}
	entry2 := TranscriptEntry{
		ID:        "entry-002",
		Role:      "assistant",
		Content:   "Second reply",
		Tokens:    80,
		Cost:      0.0021,
		Timestamp: time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(sessionID, entry1))
	require.NoError(t, store.AppendTranscript(sessionID, entry2))

	got, err := store.GetMeta(sessionID)
	require.NoError(t, err)

	assert.Equal(t, 200, got.Stats.TokensOut,
		"TokensOut must equal sum of both assistant entries (120+80)")
	assert.Equal(t, 200, got.Stats.TokensTotal,
		"TokensTotal must equal sum of both entries (120+80)")
	assert.InDelta(t, 0.0055, got.Stats.Cost, 1e-9,
		"Cost must equal sum of both entries (0.0034+0.0021)")
	assert.Equal(t, 2, got.Stats.MessageCount,
		"MessageCount must be 2 after two assistant entries")
}

// TestAppendTranscript_UserEntry_TokensIn asserts that a user-role entry increments
// TokensIn (not TokensOut), providing the differentiation test proving the role
// routing is not hardcoded.
//
// BDD:
//
//	Given a fresh session,
//	When a user entry with Tokens=50 is appended,
//	Then TokensIn==50, TokensOut==0.
//
// Guards against: role-based token routing silently broken.
// Traces to: pkg/session/unified.go AppendTranscript role → TokensIn/TokensOut routing
func TestAppendTranscript_UserEntry_TokensIn(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	entry := TranscriptEntry{
		ID:        "user-001",
		Role:      "user",
		Content:   "Hi there",
		Tokens:    50,
		Cost:      0.0,
		Timestamp: time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, entry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 50, got.Stats.TokensIn,
		"TokensIn must equal user entry tokens")
	assert.Equal(t, 0, got.Stats.TokensOut,
		"TokensOut must remain 0 for a user entry")
	assert.Equal(t, 50, got.Stats.TokensTotal,
		"TokensTotal must include user entry tokens")
}
