package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// newAL is a helper for turn tests that only need the AgentLoop and cleanup.
func newAL(t *testing.T) (*AgentLoop, func()) {
	t.Helper()
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled
	return al, cleanup
}

// TestGetActiveAgentIDs_Empty verifies that GetActiveAgentIDs returns an empty (or nil)
// slice when no turns are active.
// BDD: Given an AgentLoop with no active turns,
// When GetActiveAgentIDs() is called,
// Then it returns an empty (zero-length) slice.
// Traces to: vivid-roaming-planet.md line 162
func TestGetActiveAgentIDs_Empty(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	ids := al.GetActiveAgentIDs()
	assert.Empty(t, ids, "no active turns must yield an empty slice")
}

// TestGetActiveAgentIDs_SingleTurn verifies that GetActiveAgentIDs returns the agent ID
// of the one active turn.
// BDD: Given an AgentLoop with one active turn for agentID "test-agent",
// When GetActiveAgentIDs() is called,
// Then it returns a slice containing "test-agent".
// Traces to: vivid-roaming-planet.md line 163
func TestGetActiveAgentIDs_SingleTurn(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	ts := &turnState{agentID: "test-agent"}
	al.activeTurnStates.Store("session-key-1", ts)
	defer al.activeTurnStates.Delete("session-key-1")

	ids := al.GetActiveAgentIDs()
	assert.Contains(t, ids, "test-agent", "active turn agent must be in the returned slice")
	assert.Len(t, ids, 1)
}

// TestGetActiveAgentIDs_MultipleTurnsSameAgent verifies that two turns with the same
// agentID are deduplicated in the result.
// BDD: Given an AgentLoop with two active turns both having agentID "shared-agent",
// When GetActiveAgentIDs() is called,
// Then it returns a slice with exactly one entry "shared-agent".
// Traces to: vivid-roaming-planet.md line 164
func TestGetActiveAgentIDs_MultipleTurnsSameAgent(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	ts1 := &turnState{agentID: "shared-agent"}
	ts2 := &turnState{agentID: "shared-agent"}
	al.activeTurnStates.Store("session-key-a", ts1)
	al.activeTurnStates.Store("session-key-b", ts2)
	defer al.activeTurnStates.Delete("session-key-a")
	defer al.activeTurnStates.Delete("session-key-b")

	ids := al.GetActiveAgentIDs()
	assert.Contains(t, ids, "shared-agent")
	// Must deduplicate — exactly one entry for the shared agent ID.
	count := 0
	for _, id := range ids {
		if id == "shared-agent" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate agentID across turns must be deduplicated")
}

// TestGetActiveAgentIDs_MultipleDifferentAgents verifies that all unique agent IDs from
// different active turns are returned.
// BDD: Given an AgentLoop with active turns for "agent-a" and "agent-b",
// When GetActiveAgentIDs() is called,
// Then it returns a slice containing both "agent-a" and "agent-b".
// Traces to: vivid-roaming-planet.md line 165
func TestGetActiveAgentIDs_MultipleDifferentAgents(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	tsA := &turnState{agentID: "agent-a"}
	tsB := &turnState{agentID: "agent-b"}
	al.activeTurnStates.Store("session-key-x", tsA)
	al.activeTurnStates.Store("session-key-y", tsB)
	defer al.activeTurnStates.Delete("session-key-x")
	defer al.activeTurnStates.Delete("session-key-y")

	ids := al.GetActiveAgentIDs()
	assert.Contains(t, ids, "agent-a", "agent-a must be in the result")
	assert.Contains(t, ids, "agent-b", "agent-b must be in the result")
	assert.Len(t, ids, 2, "must return exactly two distinct agent IDs")
}

// --- Suite 4: finalizeStreamer unit tests ---

// mockStreamer is a bus.Streamer used in turn finalize tests.
// It counts Finalize calls so tests can assert idempotency.
type mockStreamer struct {
	finalizeCalled int
}

func (m *mockStreamer) Update(_ context.Context, _ string) error { return nil }
func (m *mockStreamer) Finalize(_ context.Context, _ string) error {
	m.finalizeCalled++
	return nil
}
func (m *mockStreamer) Cancel(_ context.Context) {}

// Compile-time assertion that mockStreamer satisfies bus.Streamer.
var _ bus.Streamer = (*mockStreamer)(nil)

// TestTurnState_FinalizeStreamer verifies that finalizeStreamer calls Finalize exactly once
// on the active streamer and clears it so a second call is a no-op.
// BDD: Given a turnState with an active streamer,
// When finalizeStreamer is called twice,
// Then Finalize is invoked exactly once (second call is a no-op because lastStreamer is nil).
// Traces to: pkg/agent/turn.go — turnState.finalizeStreamer
func TestTurnState_FinalizeStreamer(t *testing.T) {
	ts := &turnState{}
	ms := &mockStreamer{}

	ts.setLastStreamer(ms)
	ts.finalizeStreamer(context.Background())

	require.Equal(t, 1, ms.finalizeCalled, "Finalize must be called exactly once after setLastStreamer")

	// Second call must be a no-op — lastStreamer was cleared on first finalize.
	ts.finalizeStreamer(context.Background())
	assert.Equal(t, 1, ms.finalizeCalled, "second finalizeStreamer call must not invoke Finalize again")
}

// TestTurnState_FinalizeStreamer_Nil verifies that finalizeStreamer is safe to call
// when no streamer has been set (lastStreamer is nil).
// BDD: Given a turnState with no active streamer,
// When finalizeStreamer is called,
// Then no panic occurs.
// Traces to: pkg/agent/turn.go — turnState.finalizeStreamer nil guard
func TestTurnState_FinalizeStreamer_Nil(t *testing.T) {
	ts := &turnState{}

	// Must not panic when lastStreamer is nil.
	require.NotPanics(t, func() {
		ts.finalizeStreamer(context.Background())
	}, "finalizeStreamer must not panic when no streamer is set")
}

// --- Suite 5: B4 abandoned-write suppression tests ---

// TestTurnState_Abandoned_SuppressesAppendToolCallTranscript verifies that
// appendToolCallTranscript is a no-op when the turn is marked abandoned, and
// that the global abandonedWritesSuppressed counter is incremented.
//
// BDD: Given a turnState with an active transcriptStore and abandoned==true,
// When appendToolCallTranscript is called,
// Then no entry is appended to the transcript,
// AND AbandonedWritesSuppressed() increments by 1.
//
// Traces to: pkg/agent/turn.go — appendToolCallTranscript B4 guard
func TestTurnState_Abandoned_SuppressesAppendToolCallTranscript(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewUnifiedStore(dir)
	require.NoError(t, err)

	meta, err := store.NewSession(session.SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sid := meta.ID

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: sid,
	}
	ts.abandoned.Store(true)

	before := AbandonedWritesSuppressed()

	ts.appendToolCallTranscript(session.ToolCall{
		ID:   "tc-001",
		Tool: "some.tool",
	})

	// Counter must have incremented.
	assert.Equal(t, before+1, AbandonedWritesSuppressed(), "suppressed counter must increment")

	// No entry must have been written.
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	assert.Empty(t, entries, "no transcript entry must be written when abandoned")
}

// TestTurnState_Abandoned_SuppressesAddTurnStats verifies that AddTurnStats
// is a no-op when the turn is marked abandoned, and that the counter increments.
//
// BDD: Given a turnState with abandoned==true,
// When AddTurnStats is called,
// Then turnTokens and turnCostUSD remain zero,
// AND AbandonedWritesSuppressed() increments by 1.
//
// Traces to: pkg/agent/turn.go — AddTurnStats B4 guard
func TestTurnState_Abandoned_SuppressesAddTurnStats(t *testing.T) {
	ts := &turnState{}
	ts.abandoned.Store(true)

	before := AbandonedWritesSuppressed()
	ts.AddTurnStats(1000, 0.05)

	assert.Equal(t, before+1, AbandonedWritesSuppressed(), "suppressed counter must increment")

	tokens, cost := ts.GetTurnStats()
	assert.Equal(t, int64(0), tokens, "turnTokens must remain zero when abandoned")
	assert.Equal(t, float64(0), cost, "turnCostUSD must remain zero when abandoned")
}

// TestTurnState_Abandoned_SuppressesFinalizeStreamer verifies that finalizeStreamer
// does not call Finalize on the streamer when the turn is marked abandoned, and
// that the counter increments.
//
// BDD: Given a turnState with an active streamer and abandoned==true,
// When finalizeStreamer is called,
// Then the streamer's Finalize method is NOT called,
// AND AbandonedWritesSuppressed() increments by 1.
//
// Traces to: pkg/agent/turn.go — finalizeStreamer B4 guard
func TestTurnState_Abandoned_SuppressesFinalizeStreamer(t *testing.T) {
	ts := &turnState{}
	ms := &mockStreamer{}
	ts.setLastStreamer(ms)
	ts.abandoned.Store(true)

	before := AbandonedWritesSuppressed()
	ts.finalizeStreamer(context.Background())

	assert.Equal(t, before+1, AbandonedWritesSuppressed(), "suppressed counter must increment")
	assert.Equal(t, 0, ms.finalizeCalled, "Finalize must NOT be called when abandoned")
}
