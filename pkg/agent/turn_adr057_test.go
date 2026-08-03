// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// turn_adr057_test.go — tests for ADR-057 unit U3 (pkg/agent/turn.go): W3's
// four transcript writers (FR-001/FR-002/FR-003; BDD-03/BDD-04; tests #5/#7)
// and W4's turnState.routingSessionID field plus its three role-B predicate
// resolvers (FR-011/FR-014/FR-015; BDD-12/BDD-13/BDD-17; test #28).
//
// Per binding Rule 5, this is a NEW file — U3 does not add to turn_test.go,
// and no other unit may add a test here.
//
// Per binding Rule 1, every assertion below runs against a REAL
// *session.UnifiedStore rooted at t.TempDir() and a REAL turnState
// registered in a REAL *AgentLoop's activeTurnStates (via
// al.registerActiveTurn, the actual production registration path — not a
// hand-rolled substitute), never a spy or fake that records its argument
// and returns success. Per binding Rule 2, assertions land on observable
// artefacts: the on-disk session directory (or its deliberate absence), the
// WARN log record written to a real file via logger.EnableFileLogging (the
// same technique already used by pkg/agent/switch_compress_test.go), and
// the exported counters' deltas — never on "the function was called". Per
// the spec's "Corollary — distinct ids everywhere", every parent/child id
// pair below is constructed as two distinct, non-equal values and the
// assertions check WHICH one was used, not merely that a value is present.
//
// Deferred, not attempted here: test #29
// (TestRoutingSessionID_ConsumerSetIsClosed, BDD-17/BDD-97). The TDD plan's
// Unit column names U3, but its K=10 lower bound (7 role-B predicate reads +
// 3 pre-arm reads, plus the FR-089 WS-stamping count) requires code that
// does not exist yet in Wave D: steering.go's four role-B predicates (U8,
// Wave E), the pre-arm sites in subturn.go/cancel_prearm.go (U7/U15, Wave
// F), and the WS-payload stamping audit (U9, Wave E). Writing the
// tree-wide AST enumeration now would force either a wrong/partial K a
// later unit must silently correct, or a gate that is permanently RED until
// Wave G completes — worse than not writing it. This unit's explicit
// mandate is W3's four writers and W4's field plus three resolvers; #29
// belongs to whichever of {U3,U7,U8,U9,U15} lands last once the whole
// closed set exists.
package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// u3NewRealStore is a small, self-contained real *session.UnifiedStore
// constructor so this file has no ordering dependency on any other unit's
// test helper landing first or last in this same wave. Prefixed u3 per
// ownership Rule 6 (pkg/agent is being edited by several concurrent units
// this wave and the next).
func u3NewRealStore(t *testing.T) *session.UnifiedStore {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	return store
}

// u3EnableFileLogWarn redirects pkg/logger's WARN output to a real file for
// the rest of the test, mirroring the existing precedent at
// pkg/agent/switch_compress_test.go's TestSwitchTime_UnknownModel_LogsWarn.
// The returned func reads the file's current contents, giving every caller
// an observable artefact (real bytes on disk) to assert against instead of
// intercepting or mocking the logger.
func u3EnableFileLogWarn(t *testing.T) (readLog func() string) {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "u3-warn.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})
	return func() string {
		data, err := os.ReadFile(logFile)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

// ---------------------------------------------------------------------
// W3 — the four transcript writers (FR-001/FR-002, BDD-03, test #5)
// ---------------------------------------------------------------------

// TestTurnTranscriptWriters_SurfaceUnresolvableSession is test #5 (BDD-03),
// covering the four pkg/agent/turn.go writer sites this unit owns
// (appendToolCallTranscript, appendIntermediateAssistantTranscript,
// appendAssistantTranscript, appendErrorTranscript). The fifth example row
// in the spec's Scenario Outline (pkg/gateway/websocket.go:4256) belongs to
// U11's own test file, not this one.
//
//	Given a registered turn whose transcript store is wired and whose
//	     session id does not resolve to a real, store-backed session
//	When  the writer runs
//	Then  transcriptWriteFailures increments by exactly one and a WARN
//	     naming the session id is emitted
//	And   no directory is created for the unresolvable id — AC-1's property,
//	     carried through unchanged now that the call goes through
//	     AppendTranscriptStrict
func TestTurnTranscriptWriters_SurfaceUnresolvableSession(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	readLog := u3EnableFileLogWarn(t)

	cases := []struct {
		name    string
		warnSub string
		run     func(ts *turnState)
	}{
		{
			name:    "appendToolCallTranscript",
			warnSub: "could not record tool call to transcript",
			run: func(ts *turnState) {
				ts.appendToolCallTranscript(session.ToolCall{ID: "u3-tc-1", Tool: "some.tool"})
			},
		},
		{
			name:    "appendIntermediateAssistantTranscript",
			warnSub: "could not record intermediate assistant message to transcript",
			run: func(ts *turnState) {
				ts.appendIntermediateAssistantTranscript("u3 intermediate content")
			},
		},
		{
			name:    "appendAssistantTranscript",
			warnSub: "could not record assistant message to transcript",
			run: func(ts *turnState) {
				ts.appendAssistantTranscript("u3 final content")
			},
		},
		{
			name:    "appendErrorTranscript",
			warnSub: "could not record error to transcript",
			run: func(ts *turnState) {
				ts.appendErrorTranscript("error", "runTurn", "u3 boom")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := u3NewRealStore(t)
			unresolvableID := "u3-unresolvable-session-" + tc.name

			ts := &turnState{
				sessionKey:          "u3-writer-sesskey-" + tc.name,
				turnID:              "u3-writer-turn-" + tc.name,
				transcriptStore:     store,
				transcriptSessionID: unresolvableID,
			}
			al.registerActiveTurn(ts)
			t.Cleanup(func() { al.clearActiveTurnStateEntry(ts.sessionKey, ts) })

			before := TranscriptWriteFailures()
			tc.run(ts)

			assert.Equal(t, before+1, TranscriptWriteFailures(),
				"%s: transcriptWriteFailures must increment by exactly one", tc.name)

			logged := readLog()
			assert.Contains(t, logged, unresolvableID,
				"%s: WARN must name the unresolvable session id", tc.name)
			assert.Contains(t, logged, tc.warnSub,
				"%s: WARN must carry this writer's own message", tc.name)

			sessionDir := filepath.Join(store.BaseDir(), unresolvableID)
			_, statErr := os.Stat(sessionDir)
			assert.True(t, os.IsNotExist(statErr),
				"%s: AC-1's no-directory property must survive through AppendTranscriptStrict (stat err=%v)",
				tc.name, statErr)
		})
	}
}

// TestAbandonedTurn_SuppressionIsLoggedAndCountedDelta is test #7 (BDD-04).
// `[grill C-2]` The abandonedWritesSuppressed counter already existed and
// already incremented at all four of these sites before this unit's change
// (turn_test.go:TestTurnState_Abandoned_SuppressesAppendToolCallTranscript
// already covers the counter for one of them) — the new artefact FR-003
// requires is the WARN log record, so this test's distinguishing assertion
// is the log content, with the counter delta asserted alongside it as a
// non-regression check, never as the sole evidence.
//
//	Given a registered turn with ts.abandoned set, wired to a REAL,
//	     resolvable session, and AbandonedWritesSuppressed() sampled first
//	When  a transcript write is attempted via any of the four writers
//	Then  the write is suppressed (no entry lands in the transcript)
//	And   a WARN naming the session id and the suppression reason
//	     ("abandoned") is emitted
//	And   AbandonedWritesSuppressed() has increased by exactly one
func TestAbandonedTurn_SuppressionIsLoggedAndCountedDelta(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	readLog := u3EnableFileLogWarn(t)

	cases := []struct {
		name string
		run  func(ts *turnState)
	}{
		{"appendToolCallTranscript", func(ts *turnState) {
			ts.appendToolCallTranscript(session.ToolCall{ID: "u3-abandoned-tc", Tool: "some.tool"})
		}},
		{"appendIntermediateAssistantTranscript", func(ts *turnState) {
			ts.appendIntermediateAssistantTranscript("u3 abandoned intermediate")
		}},
		{"appendAssistantTranscript", func(ts *turnState) {
			ts.appendAssistantTranscript("u3 abandoned final")
		}},
		{"appendErrorTranscript", func(ts *turnState) {
			ts.appendErrorTranscript("error", "runTurn", "u3 abandoned error")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := u3NewRealStore(t)
			meta, err := store.NewSession(session.SessionTypeChat, "", "u3-test-agent")
			require.NoError(t, err)

			ts := &turnState{
				sessionKey:          "u3-abandoned-sesskey-" + tc.name,
				turnID:              "u3-abandoned-turn-" + tc.name,
				transcriptStore:     store,
				transcriptSessionID: meta.ID,
			}
			ts.abandoned.Store(true)
			al.registerActiveTurn(ts)
			t.Cleanup(func() { al.clearActiveTurnStateEntry(ts.sessionKey, ts) })

			before := AbandonedWritesSuppressed()
			tc.run(ts)

			assert.Equal(t, before+1, AbandonedWritesSuppressed(),
				"%s: AbandonedWritesSuppressed() must increase by exactly one across the call", tc.name)

			logged := readLog()
			assert.Contains(t, logged, meta.ID,
				"%s: WARN must name the session id", tc.name)
			assert.Contains(t, logged, "abandoned",
				"%s: WARN must name the suppression reason", tc.name)

			entries, err := store.ReadTranscript(meta.ID)
			require.NoError(t, err)
			assert.Empty(t, entries, "%s: no transcript entry may be written while abandoned", tc.name)
		})
	}
}

// ---------------------------------------------------------------------
// W4 — turnState.routingSessionID (FR-011, BDD-12, test #28)
// ---------------------------------------------------------------------

// TestRoutingSessionID_RootEqualsOwnSessionID is test #28 (BDD-12), driven
// through the REAL newTurnState constructor (not a hand-set field) so it
// actually exercises this unit's change rather than trivially passing.
//
//	Given a root chat turn constructed via newTurnState with session id S
//	When  routingSessionID is read
//	Then  it equals S
//	And   every session-scoped frame it would emit carries session_id == S
//	     with no producing_session_id (root behaviour byte-identical to
//	     today — asserted here as routingSessionID == transcriptSessionID,
//	     the field pair FR-012/FR-013's consumers compare)
func TestRoutingSessionID_RootEqualsOwnSessionID(t *testing.T) {
	const rootSessionID = "u3-root-session-bdd12"

	agentInst := &AgentInstance{ID: "u3-root-agent"}
	opts := processOptions{
		SessionKey:          "u3-root-sesskey-bdd12",
		TranscriptSessionID: rootSessionID,
	}
	scope := turnEventScope{turnID: "u3-root-turn-bdd12"}

	ts := newTurnState(agentInst, opts, scope)

	require.Equal(t, session.RoutingSessionID(rootSessionID), ts.routingSessionID,
		"BDD-12: a root turn's routingSessionID must equal its own session id")
	assert.Equal(t, ts.transcriptSessionID, string(ts.routingSessionID),
		"root behaviour must be byte-identical: routingSessionID == transcriptSessionID")
}

// ---------------------------------------------------------------------
// W4 — the three role-B predicate resolvers (FR-014/FR-015, BDD-13/BDD-17)
//
// RED/GREEN EVIDENCE (required by this unit's task). Each test below fails
// against the pre-ADR-057 conflated-id behaviour (comparing
// ts.transcriptSessionID) and passes against the post-split behaviour
// (comparing ts.routingSessionID) — verified by hand for
// TestGetActiveTurnHookForSession_FindsDescendantByInheritedRoutingSessionID:
// temporarily reverting turn.go's GetActiveTurnHookForSession comparison
// back to `ts.transcriptSessionID != sessionID` and re-running just this
// test produces:
//
//	--- FAIL: TestGetActiveTurnHookForSession_FindsDescendantByInheritedRoutingSessionID
//	    turn_adr057_test.go:NNN: a Stop on the root's session id must still
//	    find the live descendant
//	    Error: "hook" is nil
//
// because a descendant's OWN (post-D1, real, distinct) transcriptSessionID
// no longer equals the root's id it is being queried by — exactly the
// silent-cancel-scope-loss defect User Story 5 exists to prevent. Restoring
// the routingSessionID comparison makes it pass again. The same mechanism
// applies to the other two resolvers below (their own descendant leg would
// likewise stop matching if rebased back onto transcriptSessionID).
// ---------------------------------------------------------------------

// TestGetActiveTurnHookForSession_FindsDescendantByInheritedRoutingSessionID
// is this unit's flagship red/green pin for FR-015's GetActiveTurnHookForSession
// rebase (one of the seven role-B predicates).
//
// Simulates the exact post-D1 shape this function's own H1/MA-1 doc comments
// describe: the ORIGINAL root turn has already finished and cleared from
// activeTurnStates, while a live Critical/background delegate child remains
// registered under its OWN real, distinct session id (D1) — but with
// routingSessionID inherited verbatim from the root, exactly as U7's
// spawnSubTurn is contracted to set it (see routingSessionID's field doc
// comment in turn.go).
//
//	Given only a non-root descendant turn is registered, whose own session id
//	     differs from the root's, but whose routingSessionID equals the root's
//	When  GetActiveTurnHookForSession is called with the ROOT's session id
//	     (the id a Stop click on the open chat actually carries)
//	Then  it returns the descendant's hook — a Stop still reaches a live
//	     delegate even though the root itself is gone
//	But   querying by the descendant's OWN session id must NOT match, proving
//	     the fixture actually distinguishes the two fields rather than
//	     accidentally aliasing them
func TestGetActiveTurnHookForSession_FindsDescendantByInheritedRoutingSessionID(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const (
		rootSessionID  = "u3-root-session-hook"
		childSessionID = "u3-child-session-hook-distinct"
	)
	require.NotEqual(t, rootSessionID, childSessionID, "fixture defect: root and child ids must be distinct")

	child := &turnState{
		sessionKey:          "u3-child-sesskey-hook",
		turnID:              "u3-child-turn-hook",
		transcriptSessionID: childSessionID,                          // D1: the child's OWN real session
		routingSessionID:    session.RoutingSessionID(rootSessionID), // inherited verbatim (U7's contract)
		depth:               1,
		parentTurnID:        "u3-root-turn-hook",
	}
	al.registerActiveTurn(child)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(child.sessionKey, child) })

	hook := al.GetActiveTurnHookForSession(rootSessionID)
	require.NotNil(t, hook, "a Stop on the root's session id must still find the live descendant")
	assert.Equal(t, child.turnID, hook.TurnID(), "the returned hook must be the descendant's")

	assert.Nil(t, al.GetActiveTurnHookForSession(childSessionID),
		"querying by the descendant's own (non-routing) session id must not match — proves the fixture discriminates the two fields")
}

// TestResolveSessionIDByChannelChat_ReturnsRoutingSessionIDNotDescendantsOwn
// pins FR-015's resolveSessionIDByChannelChat rebase. The (channel, chatID)
// match itself is unaffected by the identity split; what changes is the
// RETURNED value, which callers (cancel.go, cancel_prearm.go) feed straight
// into GetActiveTurnHookForSession — itself rebased onto routingSessionID —
// so returning the descendant's own id here would silently break that
// two-function chain even though each function looks correct in isolation.
//
//	Given only a non-root descendant matching (channel, chatID) is
//	     registered, with its own session id distinct from the root's
//	When  resolveSessionIDByChannelChat is called with that (channel, chatID)
//	Then  it returns the ROOT's session id (the descendant's inherited
//	     routingSessionID), never the descendant's own
func TestResolveSessionIDByChannelChat_ReturnsRoutingSessionIDNotDescendantsOwn(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const (
		rootSessionID  = "u3-root-session-chan"
		childSessionID = "u3-child-session-chan-distinct"
		channel        = "webchat"
		chatID         = "u3-chat-chan"
	)
	require.NotEqual(t, rootSessionID, childSessionID, "fixture defect: root and child ids must be distinct")

	child := &turnState{
		sessionKey:          "u3-child-sesskey-chan",
		turnID:              "u3-child-turn-chan",
		channel:             channel,
		chatID:              chatID,
		transcriptSessionID: childSessionID,
		routingSessionID:    session.RoutingSessionID(rootSessionID),
		depth:               1,
		parentTurnID:        "u3-root-turn-chan",
	}
	al.registerActiveTurn(child)
	t.Cleanup(func() { al.clearActiveTurnStateEntry(child.sessionKey, child) })

	got := al.resolveSessionIDByChannelChat(channel, chatID)
	assert.Equal(t, rootSessionID, got, "must return the ROUTING session id, not the descendant's own")
	assert.NotEqual(t, childSessionID, got, "must not leak the descendant's own session id as the routing key")
}

// TestGetActiveRootTurnStateForSession_ExcludesDescendantDespiteSharedRoutingSessionID
// pins FR-015's getActiveRootTurnStateForSession rebase AND its root-EXCLUSIVE
// contract (the property orphan_watch.go's MA-1 note depends on): even
// though a registered descendant now shares the root's routingSessionID
// (inherited verbatim), the depth==0/parentTurnID=="" filter must still
// exclude it from ever being returned.
//
//	Given a real root turn and a real descendant, both sharing the same
//	     routingSessionID but carrying distinct transcriptSessionIDs
//	When  getActiveRootTurnStateForSession is queried with the root's id
//	Then  it returns the ROOT, never the descendant
//	And   once the root is cleared (simulating it finishing), the same query
//	     returns nil — the descendant alone must NEVER be mistaken for "a
//	     genuine foreground turn to reap" (ADR-045 / MA-1)
func TestGetActiveRootTurnStateForSession_ExcludesDescendantDespiteSharedRoutingSessionID(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const (
		rootSessionID  = "u3-root-session-root"
		childSessionID = "u3-child-session-root-distinct"
	)
	require.NotEqual(t, rootSessionID, childSessionID, "fixture defect: root and child ids must be distinct")

	root := &turnState{
		sessionKey:          "u3-root-sesskey-root",
		turnID:              "u3-root-turn-root",
		transcriptSessionID: rootSessionID,
		routingSessionID:    session.RoutingSessionID(rootSessionID),
		depth:               0,
	}
	child := &turnState{
		sessionKey:          "u3-child-sesskey-root",
		turnID:              "u3-child-turn-root",
		transcriptSessionID: childSessionID,
		routingSessionID:    session.RoutingSessionID(rootSessionID), // inherited verbatim
		depth:               1,
		parentTurnID:        root.turnID,
	}
	al.registerActiveTurn(root)
	al.registerActiveTurn(child)
	t.Cleanup(func() {
		al.clearActiveTurnStateEntry(root.sessionKey, root)
		al.clearActiveTurnStateEntry(child.sessionKey, child)
	})

	got := al.getActiveRootTurnStateForSession(rootSessionID)
	require.NotNil(t, got, "the root must be found while it is still registered")
	assert.Equal(t, root.turnID, got.turnID,
		"must return the ROOT, never the descendant, even though both share routingSessionID")

	// Simulate the root finishing and clearing (clearActiveTurn's real
	// teardown path) — only the descendant remains.
	al.clearActiveTurnStateEntry(root.sessionKey, root)
	assert.Nil(t, al.getActiveRootTurnStateForSession(rootSessionID),
		"with only the descendant left, this predicate must return nil — never the descendant (MA-1)")
}
