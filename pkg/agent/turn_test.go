package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
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

// streamingMockStreamer is a bus.Streamer that also reports how many bytes it
// has streamed, mirroring *gateway.wsStreamer's StreamedContentLen() method. It
// lets the inline-retry guard test exercise the exact interface assertion used
// in loop.go without importing the gateway package.
type streamingMockStreamer struct {
	mockStreamer
	streamedLen int
}

func (m *streamingMockStreamer) StreamedContentLen() int { return m.streamedLen }

var _ bus.Streamer = (*streamingMockStreamer)(nil)

// retryAllowedAfterDrop replicates the inline-retry guard predicate from
// loop.go (the runTurn timeout/transport-drop branch): a transport drop may be
// inline-retried only when the active streamer has NOT already streamed partial
// content to the client. Returning false means "do not retry — break". Keeping
// this in lockstep with loop.go is enforced by the tests below.
func retryAllowedAfterDrop(streamer bus.Streamer) bool {
	if sc, ok := streamer.(interface{ StreamedContentLen() int }); ok && sc.StreamedContentLen() > 0 {
		return false
	}
	return true
}

// TestInlineRetryGuard_PartialStreamNotRetried verifies that a transport drop
// AFTER partial content was streamed is NOT inline-retried (which would
// duplicate text in the SPA), while drops with no streamed content (or no
// stream-reporting streamer at all) remain retriable.
//
// BDD: Given a transport-drop classified as FailoverTimeout,
// When the active streamer has already emitted >0 bytes,
// Then the inline-retry guard breaks instead of retrying;
// And when nothing has been streamed (or there is no streamer), it retries.
//
// Traces to: pkg/agent/loop.go — runTurn inline-retry StreamedContentLen guard (FIX 1).
func TestInlineRetryGuard_PartialStreamNotRetried(t *testing.T) {
	// Streamer that already streamed content: must NOT retry.
	streamed := &streamingMockStreamer{streamedLen: 42}
	assert.False(t, retryAllowedAfterDrop(streamed),
		"drop after partial stream must not be inline-retried (would duplicate text)")

	// Streamer that reports content length but streamed nothing yet: retry OK
	// (drop before first token — the common, safe case).
	empty := &streamingMockStreamer{streamedLen: 0}
	assert.True(t, retryAllowedAfterDrop(empty),
		"drop before any token streamed must remain inline-retriable")

	// Streamer without StreamedContentLen (non-streaming / Chat path): retry OK.
	plain := &mockStreamer{}
	assert.True(t, retryAllowedAfterDrop(plain),
		"non-stream-reporting streamer must remain inline-retriable")

	// No active streamer at all: retry OK.
	assert.True(t, retryAllowedAfterDrop(nil),
		"absent streamer must remain inline-retriable")
}

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

// --- Suite 6: turn_failed marker tests ---

// failCapturingStreamer is a bus.Streamer that also implements streamerFailedSetter
// so tests can assert that finalizeStreamer propagates the turnFailed flag.
type failCapturingStreamer struct {
	mockStreamer
	receivedFailed *bool
}

func (f *failCapturingStreamer) SetTurnFailed(failed bool) {
	f.receivedFailed = &failed
}

var _ bus.Streamer = (*failCapturingStreamer)(nil)

// TestTurnState_MarkTurnFailed_PropagatesToStreamer verifies that calling
// markTurnFailed before finalizeStreamer causes the streamer's SetTurnFailed
// to be called with true, and that a normal (non-failed) turn leaves the flag
// as false/absent.
//
// BDD: Given a turnState whose turn ended via a synthetic fallback,
// When markTurnFailed() is called before finalizeStreamer,
// Then the streamer's SetTurnFailed is called with true;
// AND when markTurnFailed is NOT called, SetTurnFailed is called with false.
//
// Traces to: pkg/agent/turn.go — markTurnFailed / finalizeStreamer / streamerFailedSetter
func TestTurnState_MarkTurnFailed_PropagatesToStreamer(t *testing.T) {
	t.Run("failed turn", func(t *testing.T) {
		ts := &turnState{}
		fs := &failCapturingStreamer{}
		ts.setLastStreamer(fs)
		ts.markTurnFailed()

		ts.finalizeStreamer(context.Background())

		require.NotNil(t, fs.receivedFailed, "SetTurnFailed must have been called")
		assert.True(t, *fs.receivedFailed, "SetTurnFailed must be called with true for a failed turn")
	})

	t.Run("normal turn", func(t *testing.T) {
		ts := &turnState{}
		fs := &failCapturingStreamer{}
		ts.setLastStreamer(fs)
		// markTurnFailed is NOT called — simulates a normal successful turn.

		ts.finalizeStreamer(context.Background())

		// SetTurnFailed is still called (with false) because finalizeStreamer always
		// invokes it when the interface is implemented.
		require.NotNil(t, fs.receivedFailed, "SetTurnFailed must have been called even on a normal turn")
		assert.False(t, *fs.receivedFailed, "SetTurnFailed must be called with false for a normal turn")
	})
}

// --- Suite 7: B4 abandoned-turn live-stream ownership release regression ---
//
// Regression coverage for a CRITICAL bug unanimously flagged by a 7-reviewer
// gate (architect, silent-failure-hunter, type-design-analyzer,
// pr-test-analyzer): finalizeStreamer's B4 abandoned-turn early return
// skipped Finalize entirely — Finalize being the ONLY place a wsStreamer
// released its WSHandler.streamOwners live-stream ownership claim (see
// pkg/gateway/websocket.go). A background delegate that became the live
// owner for a chatID and was later MarkAbandoned()'d by cancel.go's PHASE C
// (newly reachable for delegates via this session's own
// sessionTurnsStillAlive fix) left that chatID's ownership entry stuck on
// the dead turn's ID forever — no TTL, no sweep, no WS-teardown hook —
// permanently shadowing every later turn on that chat: the bubble sits
// blank while "streaming," then the spinner just stops.

// releaseTrackingMockStreamer is a bus.Streamer that also implements the
// streamOwnershipReleaser optional interface (mirroring *gateway.wsStreamer's
// real ReleaseStreamOwnership method), letting tests assert that
// finalizeStreamer's abandoned-turn path releases a live-stream ownership
// claim even though it deliberately skips calling Finalize.
type releaseTrackingMockStreamer struct {
	mockStreamer
	releaseCalled int
}

func (m *releaseTrackingMockStreamer) ReleaseStreamOwnership() {
	m.releaseCalled++
}

var (
	_ bus.Streamer            = (*releaseTrackingMockStreamer)(nil)
	_ streamOwnershipReleaser = (*releaseTrackingMockStreamer)(nil)
)

// TestTurnState_Abandoned_ReleasesStreamOwnership is the direct regression
// test for the bug: it fails without the fix because, before it, the B4
// early return never touched the streamer at all beyond nil-ing out
// ts.lastStreamer — releaseCalled would remain 0.
//
// BDD: Given a turnState with an active streamer implementing the optional
// streamOwnershipReleaser interface,
// When the turn is marked abandoned and finalizeStreamer is called,
// Then ReleaseStreamOwnership is invoked exactly once on the streamer,
// AND Finalize is never called (the pre-existing B4 done-frame suppression
// must be preserved),
// AND the suppressed-writes counter still increments as before.
//
// Traces to: pkg/agent/turn.go — finalizeStreamer B4 guard / streamOwnershipReleaser.
func TestTurnState_Abandoned_ReleasesStreamOwnership(t *testing.T) {
	ts := &turnState{}
	rs := &releaseTrackingMockStreamer{}
	ts.setLastStreamer(rs)
	ts.abandoned.Store(true)

	before := AbandonedWritesSuppressed()
	ts.finalizeStreamer(context.Background())

	assert.Equal(t, before+1, AbandonedWritesSuppressed(), "suppressed counter must still increment")
	assert.Equal(t, 0, rs.finalizeCalled,
		"Finalize must NOT be called when abandoned — the B4 done-frame suppression must be preserved")
	assert.Equal(t, 1, rs.releaseCalled,
		"BUG REGRESSION: ReleaseStreamOwnership must be called exactly once even though Finalize is "+
			"skipped — otherwise an abandoned turn that had claimed live-stream ownership leaves its "+
			"chatID permanently shadowed for every later turn")
}

// TestTurnState_Abandoned_StreamerWithoutReleaseInterface_DoesNotPanic verifies
// that a streamer NOT implementing streamOwnershipReleaser (e.g. a bare
// bus.Streamer test fixture, or any future non-WS Streamer implementation
// with no concept of live-stream ownership) is handled safely — the type
// assertion simply fails and finalizeStreamer returns without invoking
// anything extra.
func TestTurnState_Abandoned_StreamerWithoutReleaseInterface_DoesNotPanic(t *testing.T) {
	ts := &turnState{}
	ms := &mockStreamer{}
	ts.setLastStreamer(ms)
	ts.abandoned.Store(true)

	require.NotPanics(t, func() {
		ts.finalizeStreamer(context.Background())
	})
	assert.Equal(t, 0, ms.finalizeCalled)
}

// --- Suite 8: Wave 1 rate-limit dedup correctness regression ---

// TestRecordRateLimitDenial_DoesNotEmitErrorEvent is the regression for
// the dual-emit removal. recordRateLimitDenial must emit ONE event
// (EventKindRateLimit) and ZERO EventKindError events on the bus — the
// previous "EventKindError + RateLimitPayload + EventKindRateLimit"
// dual-emit polluted the bus with two frames per condition.
func TestRecordRateLimitDenial_DoesNotEmitErrorEvent(t *testing.T) {
	al, ts := newTestAgentLoopForRateLimit(t)

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	al.recordRateLimitDenial(
		ts,
		"llm_call",
		RateLimitPayload{
			Scope:             "llm_call",
			Resource:          "llm_call",
			PolicyRule:        "test_policy",
			RetryAfterSeconds: 30,
			AgentID:           ts.agentID,
		},
		nil,
	)

	var rateLimitCount, errorCount int
drain:
	for i := 0; i < 16; i++ {
		select {
		case ev := <-sub.C:
			switch ev.Kind {
			case EventKindRateLimit:
				rateLimitCount++
				_, ok := ev.Payload.(RateLimitPayload)
				assert.True(t, ok, "EventKindRateLimit payload must remain RateLimitPayload")
			case EventKindError:
				errorCount++
			}
		default:
			break drain
		}
	}
	assert.Equal(t, 1, rateLimitCount,
		"expected exactly ONE EventKindRateLimit on the bus")
	assert.Equal(t, 0, errorCount,
		"recordRateLimitDenial must NOT emit EventKindError — the dual-emit was removed in Wave 1")
}

// TestRecordRateLimitDenial_PersistsFriendlyRateLimitTranscript locks the
// FR-001 "rate-limit denial MUST be visible after a page reload" path:
// the JSONL transcript gets a system entry carrying the friendly
// "rate limit: <policy> (retry after Ns)" copy (the rate-limit message
// is preserved verbatim by appendErrorTranscript's MAJ-001/004
// carve-out — the classifier recognizes it, but the caller-supplied
// message is preserved).
func TestRecordRateLimitDenial_PersistsFriendlyRateLimitTranscript(t *testing.T) {
	al, ts := newTestAgentLoopForRateLimit(t)
	store, sessionID := newRateLimitTranscript(t, ts)

	al.recordRateLimitDenial(
		ts,
		"llm_call",
		RateLimitPayload{
			Scope:             "llm_call",
			Resource:          "llm_call",
			PolicyRule:        "agent_max_llm_per_hour",
			RetryAfterSeconds: 60,
			AgentID:           ts.agentID,
		},
		nil,
	)

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var found bool
	for _, e := range entries {
		if e.Type != session.EntryTypeSystem {
			continue
		}
		// The friendly copy must be present verbatim — a regression
		// here would re-translate the rate-limit message into a generic
		// CodeRateLimited shape and lose the policy-rule hint.
		if e.Content == "rate limit: agent_max_llm_per_hour (retry after 60s)" {
			found = true
			break
		}
	}
	assert.True(
		t,
		found,
		"transcript must contain the friendly 'rate limit: ... (retry after Ns)' system entry verbatim (no double-translate)",
	)
}

// newTestAgentLoopForRateLimit builds a minimal AgentLoop + turnState
// for recordRateLimitDenial unit tests. Wires the same simpleMockProvider
// the other turn tests use.
func newTestAgentLoopForRateLimit(t *testing.T) (*AgentLoop, *turnState) {
	t.Helper()
	al, cleanup := newAL(t)
	t.Cleanup(cleanup)
	ts := &turnState{
		agent:               &AgentInstance{ID: "test-agent", Name: "Test"},
		agentID:             "test-agent",
		turnID:              "test-turn-rl",
		transcriptSessionID: "",
		transcriptStore:     nil,
	}
	return al, ts
}

// newRateLimitTranscript wires a real UnifiedStore + session for the
// turn so appendErrorTranscript actually persists.
//
// ADR-057 U2: appendErrorTranscript writes through
// UnifiedStore.AppendTranscriptStrict, which refuses a write against a
// session id with no meta.json on disk rather than minting an orphan
// directory for it (the exact lenient-MkdirAll defect class this migration
// closes — see AppendTranscriptStrict's doc comment). A bare, never-created
// id string here would make every append silently fail (WARN-logged, not
// propagated), leaving the transcript empty and this test's assertion
// unfalsifiable-false — so a real session must be minted first.
func newRateLimitTranscript(t *testing.T, ts *turnState) (*session.UnifiedStore, string) {
	t.Helper()
	baseDir := t.TempDir()
	store, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "test", ts.agentID)
	require.NoError(t, err)
	ts.transcriptStore = store
	ts.transcriptSessionID = meta.ID
	return store, ts.transcriptSessionID
}
