// approval_transcript_test.go — regression coverage for the ask-gate
// transcript records (approval_transcript.go).
//
// The defect these lock: an `ask`-policy tool call that blocked on human
// approval and was then DENIED or TIMED OUT left ZERO trace in the session
// transcript. The live tool_approval_required WS frame was the only artefact,
// and it is not persisted — so a five-minute unanswered approval rendered an
// empty thread, a mid-wait page reload lost even the modal, and non-webchat
// channels (which have no approval UI at all) stalled the full timeout with
// nothing to show live or on replay.
//
// Every assertion below reads the transcript back through
// UnifiedStore.ReadTranscript — i.e. off disk, the same path a page reload
// takes — rather than inspecting anything held in memory. "Survives a reload"
// is the property, so re-reading from the store IS the test.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// newApprovalTranscriptTS builds a minimal turnState wired to a REAL
// UnifiedStore on a temp dir. Only the transcript fields matter here — the
// ask-gate record helpers touch nothing else — which keeps this a fast unit
// test rather than a full agent-loop integration test.
func newApprovalTranscriptTS(t *testing.T) (*turnState, *session.UnifiedStore, string) {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	return &turnState{
		turnID:              "turn-ask-1",
		agentID:             "jim",
		transcriptSessionID: meta.ID,
		transcriptStore:     store,
	}, store, meta.ID
}

// toolCallsFor returns every persisted ToolCall carrying the given id, read
// back from disk.
func toolCallsFor(t *testing.T, store *session.UnifiedStore, sessionID string, id session.ToolCallID) []session.ToolCall {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var out []session.ToolCall
	for _, e := range entries {
		if e.Type != session.EntryTypeToolCall {
			continue
		}
		for _, tc := range e.ToolCalls {
			if tc.ID == id {
				out = append(out, tc)
			}
		}
	}
	return out
}

// TestAskGate_PendingPlaceholder_VisibleWhileBlocked proves the turn is no
// longer invisible while it waits. The placeholder must be on disk BEFORE the
// approval resolves — that is the whole point: during the (up to 300 s) wait a
// reader, and a reloading browser, must be able to see what the turn is parked
// on.
func TestAskGate_PendingPlaceholder_VisibleWhileBlocked(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_pending_1")

	recordAskPendingToolCall(ts, callID, "run_task", map[string]any{"task_id": "t-123"})

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1,
		"a pending placeholder must be persisted before the approval blocks — "+
			"without it the thread renders nothing for the whole wait")
	assert.Equal(t, toolCallStatusPending, calls[0].Status)
	assert.Equal(t, "run_task", calls[0].Tool)
	assert.Equal(t, "t-123", calls[0].Parameters["task_id"],
		"the placeholder must carry the call's arguments so the reader can see WHAT is being approved")
}

// TestAskGate_TimeoutAndDenial_PersistExplanations is the core outcome test the
// fix exists for: after a denial AND after a timeout, the transcript contains
// an entry explaining what happened, and it survives a reload.
//
// Timeout and user-denial are asserted as distinct outcomes on purpose. "You
// denied this" and "nobody answered for five minutes" are very different facts
// for whoever reads the thread afterwards, and the CI stall that motivated this
// fix was the second one.
func TestAskGate_TimeoutAndDenial_PersistExplanations(t *testing.T) {
	cases := []struct {
		name       string
		reason     string
		wantInText string
	}{
		{
			name:       "approval timed out with nobody watching",
			reason:     "timeout",
			wantInText: "expired before anyone answered",
		},
		{
			name:       "user explicitly denied",
			reason:     "user",
			wantInText: "was denied",
		},
		{
			name:       "turn cancelled while the approval was open",
			reason:     "cancel",
			wantInText: "turn was cancelled",
		},
		{
			name:       "registry saturated",
			reason:     "saturated",
			wantInText: "too many approval requests",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, store, sessionID := newApprovalTranscriptTS(t)
			const callID = session.ToolCallID("call_denied_1")
			args := map[string]any{"task_id": "t-123"}

			recordAskPendingToolCall(ts, callID, "run_task", args)
			settleAskToolCallTranscript(ts, callID, "run_task", args, tc.reason)

			// Read back from disk — this is the reload path.
			calls := toolCallsFor(t, store, sessionID, callID)
			require.Len(t, calls, 1,
				"the settled call must REPLACE the pending placeholder, not append a second "+
					"entry with the same tool-call id (that renders as a duplicate card on replay)")

			got := calls[0]
			assert.Equal(t, toolCallStatusDenied, got.Status,
				"a non-approved ask must persist as status=denied so the SPA renders it as a "+
					"resolved-but-not-run call rather than showing nothing")
			assert.Equal(t, "run_task", got.Tool)
			require.NotNil(t, got.Result, "the denial must carry a result explaining itself")
			assert.Equal(t, true, got.Result["error"])
			assert.Equal(t, tc.reason, got.Result["reason"],
				"the machine-readable outcome reason must be preserved verbatim")
			assert.Contains(t, got.Result["text"], tc.wantInText,
				"the persisted text must explain WHY nothing ran, in a sentence a human can read")
		})
	}
}

// TestAskGate_ApprovedCall_SettlesPlaceholderInPlace covers the happy path:
// once approved and executed, the real completed record must REPLACE the
// placeholder. Without this the thread would show two cards for one call — a
// stuck "pending" one and the real result.
func TestAskGate_ApprovedCall_SettlesPlaceholderInPlace(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_approved_1")
	args := map[string]any{"task_id": "t-123"}

	recordAskPendingToolCall(ts, callID, "run_task", args)

	// The normal post-execution record path (loop.go's runTurn) — unchanged
	// call site, which is exactly why the settle lives inside
	// appendToolCallTranscript rather than at the call site.
	ts.appendToolCallTranscript(session.ToolCall{
		ID:         callID,
		Tool:       "run_task",
		Status:     "success",
		DurationMS: 12,
		Parameters: args,
		Result:     map[string]any{"text": `{"task_id":"t-123","status":"in_progress"}`},
	})

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1,
		"an approved call must leave exactly ONE entry — the placeholder settled in place")
	assert.Equal(t, "success", calls[0].Status)
	assert.EqualValues(t, 12, calls[0].DurationMS)
	assert.NotNil(t, calls[0].Result)
}

// TestAskGate_NonAskToolCall_TakesPlainAppendPath is the no-regression guard on
// the hot path: a tool call that never went through the ask gate must be
// appended exactly as before, with no in-place rewrite attempted. The sync.Map
// membership check is what keeps the extra file I/O off every other tool call.
func TestAskGate_NonAskToolCall_TakesPlainAppendPath(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_plain_1")

	ts.appendToolCallTranscript(session.ToolCall{
		ID:     callID,
		Tool:   "read_file",
		Status: "success",
	})

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1)
	assert.Equal(t, "success", calls[0].Status)

	_, stillTracked := ts.askPendingToolCalls.Load(callID)
	assert.False(t, stillTracked, "a non-ask call must never be registered as pending")
}

// TestAskGate_SettleWithoutPlaceholder_StillRecords covers the headless
// auto-deny path (loop.go's AutoDenyAsk branch, issue #342) and any case where
// the placeholder write failed: there is no pending entry to replace, and the
// denial must still be recorded rather than silently dropped.
//
// This is the branch that previously left a scheduled run's transcript claiming
// the tool was simply never called, with the real reason living only in the
// audit log.
func TestAskGate_SettleWithoutPlaceholder_StillRecords(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_headless_1")

	settleAskToolCallTranscript(ts, callID, "run_task", map[string]any{"task_id": "t-9"},
		"auto-denied: ask-policy tool not allowed in a headless scheduled run")

	calls := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, calls, 1,
		"a denial with no prior placeholder must still be appended, never dropped")
	assert.Equal(t, toolCallStatusDenied, calls[0].Status)
	assert.Contains(t, calls[0].Result["text"], "headless scheduled run",
		"the headless auto-deny reason must reach the transcript verbatim")
}

// TestAskGate_DoubleSettle_IsIdempotent guards the expectStatus interlock in
// mutateToolCallInTranscript. A second settle for the same call (a racing
// timeout firing just after a user denial, say) must not clobber the recorded
// outcome or add a second entry.
func TestAskGate_DoubleSettle_IsIdempotent(t *testing.T) {
	ts, store, sessionID := newApprovalTranscriptTS(t)
	const callID = session.ToolCallID("call_double_1")
	args := map[string]any{"task_id": "t-123"}

	recordAskPendingToolCall(ts, callID, "run_task", args)
	settleAskToolCallTranscript(ts, callID, "run_task", args, "user")

	before := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, before, 1)

	// Second settle: the pending registration is already consumed, and the
	// on-disk entry is no longer "pending", so the in-place path must decline.
	settleAskToolCallTranscript(ts, callID, "run_task", args, "timeout")

	after := toolCallsFor(t, store, sessionID, callID)
	require.Len(t, after, 2,
		"a second settle appends rather than corrupting the first — the first outcome must survive")
	assert.Equal(t, "user", before[0].Result["reason"])
	assert.Equal(t, "user", after[0].Result["reason"],
		"the ORIGINAL recorded outcome must be preserved; a late duplicate must never rewrite it")
}
