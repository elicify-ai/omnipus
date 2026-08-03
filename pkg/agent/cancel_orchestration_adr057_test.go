// Package agent — cancel_orchestration_adr057_test.go
//
// ADR-057 U15 (W8, W4 pre-arm keys) — new coverage for cancel.go's
// descendant-set orchestration: PHASE A/B/C threading (FR-024), the durable
// descendant lifecycle-record walk (FR-025/FR-026), the ScopeSubtree choice
// at RequestCancel's Interrupt/InterruptSessionHard call sites (FR-041/
// FR-042), the pre-arm latch key rebase onto routingSessionID (FR-016), and
// the orphan watchdog's Critical-delegate defer/fire predicate.
//
// Rule 5: every test below is NEW, in this NEW file. Rule 6: unexported
// package-level helpers here are prefixed u15. Rule 1/2: every assertion
// lands on a REAL registered turn (activeTurnStates) or a REAL, disk-backed
// *session.LifecycleStore — never a spy that records its argument.
//
// The real-OS-process half of BDD-28/#43 (kills a child's background shell,
// not a sibling's) lives in the sibling !windows-gated file
// cancel_orchestration_adr057_unix_test.go, mirroring how
// pkg/tools/session_adr057_unix_test.go split its own real-PID coverage.
package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ---------------------------------------------------------------------------
// u15-prefixed helpers (Rule 6)
// ---------------------------------------------------------------------------

// u15RegisterActiveTurn registers a REAL root (depth-0) turnState under
// sessionID, with routingSessionID explicitly set to sessionID (FR-011: for
// a root turn routingSessionID MUST equal the turn's own session id — a bare
// struct literal like the pre-existing registerActiveTurn helper
// (cancel_background_bash_test.go) leaves this field at its zero value,
// which the post-U8 role-B predicates would never match against a
// non-empty sessionID). Stored under sessionID itself, matching this
// package's existing test convention (cancel_subagent_cascade_test.go,
// orphan_watch_test.go) rather than a production-style composite routing
// key — the point-lookup half of resolveInterruptAnchors (steering.go) hits
// on this key directly, so ScopeSubtree's reach beyond the anchor is
// exercised via the parentTurnID chain (collectLiveDescendantTurnStates),
// exactly as u15RegisterChildTurn below sets up.
func u15RegisterActiveTurn(t *testing.T, al *AgentLoop, sessionID, turnID string, depth int, parentTurnID string) *turnState {
	t.Helper()
	ts := &turnState{
		turnID:              turnID,
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               depth,
		parentTurnID:        parentTurnID,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(sessionID) })
	return ts
}

// u15RegisterChildTurn registers a REAL delegate-child turnState with its
// OWN distinct transcriptSessionID (post-D1, ADR-057) but routingSessionID
// inherited VERBATIM from rootSessionID (FR-011) — the exact identity split
// this whole change set is about. parentTurnID links it into the
// parentTurnID chain collectLiveDescendantTurnStates walks for ScopeSubtree.
// Stored under its own turnID as the map key (mirrors
// orphan_watch_test.go's existing convention for a non-root turn).
func u15RegisterChildTurn(t *testing.T, al *AgentLoop, rootSessionID, childTranscriptSessionID, turnID, parentTurnID string, depth int, critical bool) *turnState {
	t.Helper()
	require.NotEqual(t, rootSessionID, childTranscriptSessionID,
		"fixture must use distinct parent/child session ids (spec's distinct-ids-everywhere corollary)")
	ts := &turnState{
		turnID:              turnID,
		transcriptSessionID: childTranscriptSessionID,
		routingSessionID:    session.RoutingSessionID(rootSessionID),
		depth:               depth,
		parentTurnID:        parentTurnID,
		critical:            critical,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(turnID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(turnID) })
	return ts
}

// u15PersistLifecycleRecord persists a real, disk-backed LifecycleRecord for
// sessionID as a direct child of parentDurableKey, in the given state.
func u15PersistLifecycleRecord(t *testing.T, store *session.LifecycleStore, sessionID, parentDurableKey string, state session.LifecycleState) {
	t.Helper()
	err := store.Persist(&session.LifecycleRecord{
		SessionID:        sessionID,
		Generation:       0,
		State:            state,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		OwnerScopeID:     parentDurableKey,
		ParentDurableKey: parentDurableKey,
		AgentID:          "u15-test-agent",
		WorkspaceID:      "u15-test-workspace",
	})
	require.NoError(t, err)
}

// u15DescendantNames converts an audit record's decoded
// descendants_canceled field (a []any of strings after the JSON round trip
// through audit.jsonl) into a []string for assert.Contains.
func u15DescendantNames(t *testing.T, raw any) []string {
	t.Helper()
	list, ok := raw.([]any)
	require.True(t, ok, "descendants_canceled must decode as a JSON array, got %T", raw)
	names := make([]string, 0, len(list))
	for _, v := range list {
		names = append(names, fmt.Sprint(v))
	}
	return names
}

// ---------------------------------------------------------------------------
// Scope choice (FR-041/FR-042) — red/green
// ---------------------------------------------------------------------------

// TestU15Cancel_ScopeSubtreeReachesDelegateChild_ScopeSelfOnlyWouldNot proves,
// against U8's real landed Interrupt entry point, WHY cancel.go's
// RequestCancel hardcodes ScopeSubtree at both its Interrupt and
// InterruptSessionHard call sites rather than ScopeSelfOnly: a chat-level
// Stop must sweep the WHOLE delegation tree (what users expect from the Stop
// button), and ScopeSelfOnly is a silent behavior regression, not a neutral
// alternative, for that surface.
//
//   - RED: calling Interrupt directly with ScopeSelfOnly reaches ONLY the
//     anchor turn — the live delegate child sharing the same chat is left
//     untouched.
//   - GREEN: RequestCancel (which always passes ScopeSubtree) reaches BOTH
//     the chat root and its delegate child.
func TestU15Cancel_ScopeSubtreeReachesDelegateChild_ScopeSelfOnlyWouldNot(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("u15-scope-root-%d", nonce)

	// --- RED: ScopeSelfOnly, called directly ---
	u15RegisterActiveTurn(t, al, rootID, "turn-u15-scope-selfonly-root", 0, "")
	u15RegisterChildTurn(t, al, rootID, fmt.Sprintf("u15-scope-child-selfonly-%d", nonce),
		"turn-u15-scope-selfonly-child", "turn-u15-scope-selfonly-root", 1, false)

	selfOnlyReached, err := al.Interrupt(rootID, ScopeSelfOnly, "u15-test-self-only")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"turn-u15-scope-selfonly-root"}, selfOnlyReached,
		"RED: ScopeSelfOnly must reach ONLY the anchor turn, never the delegate child sharing the same chat — "+
			"proving ScopeSelfOnly would be a silent behavior regression if RequestCancel used it")

	// Clean up before the GREEN fixture reuses rootID, so a leftover
	// SelfOnly-scenario turnState cannot leak into the ScopeSubtree reach
	// below and make the GREEN assertion pass for the wrong reason.
	al.activeTurnStates.Delete(rootID)
	al.activeTurnStates.Delete("turn-u15-scope-selfonly-child")

	// --- GREEN: RequestCancel, which hardcodes ScopeSubtree ---
	u15RegisterActiveTurn(t, al, rootID, "turn-u15-scope-subtree-root", 0, "")
	u15RegisterChildTurn(t, al, rootID, fmt.Sprintf("u15-scope-child-subtree-%d", nonce),
		"turn-u15-scope-subtree-child", "turn-u15-scope-subtree-root", 1, false)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired)
	assert.ElementsMatch(t, []string{"turn-u15-scope-subtree-root", "turn-u15-scope-subtree-child"}, outcome.Descendants,
		"GREEN: RequestCancel's ScopeSubtree choice must reach BOTH the chat root and its delegate child")
}

// ---------------------------------------------------------------------------
// Pre-arm keys (FR-016, W4) — BDD-13 / #37
// ---------------------------------------------------------------------------

// TestU15PreArmLatch_KeysSetAndClearedMatch covers BDD-13: a parent turn's
// pending-spawn/pre-arm marker and the child's own registration keys must
// match on the SAME identity even though the child owns its own distinct
// transcriptSessionID (ADR-057 D1). This directly exercises the FR-016 fix
// to preArmKeysForTurn (cancel_prearm.go): rebased from
// ts.transcriptSessionID onto ts.routingSessionID.
func TestU15PreArmLatch_KeysSetAndClearedMatch(t *testing.T) {
	t.Parallel()

	nonce := time.Now().UnixNano()
	rootSessionID := fmt.Sprintf("u15-prearm-root-%d", nonce)
	childTranscriptID := fmt.Sprintf("u15-prearm-child-%d", nonce)

	rootTS := &turnState{
		turnID:              "turn-u15-prearm-root",
		transcriptSessionID: rootSessionID,
		routingSessionID:    session.RoutingSessionID(rootSessionID),
		channel:             "web",
		chatID:              "chat-u15",
	}
	childTS := &turnState{
		turnID:              "turn-u15-prearm-child",
		transcriptSessionID: childTranscriptID,
		routingSessionID:    session.RoutingSessionID(rootSessionID), // inherited verbatim, FR-011
		channel:             "web",
		chatID:              "chat-u15",
	}

	require.NotEqual(t, rootTS.transcriptSessionID, childTS.transcriptSessionID,
		"fixture must use distinct parent/child transcript session ids (spec's distinct-ids-everywhere corollary)")
	require.Equal(t, rootTS.routingSessionID, childTS.routingSessionID,
		"FR-011: a delegate child's routingSessionID must equal its root's, inherited verbatim")

	// The key a chat-level Stop's latch is armed under, from the caller's
	// own already-resolved session id (preArmKeyForScope, cancel_prearm.go).
	armedKey := preArmKeyForScope(rootSessionID, CancelScope{SessionID: rootSessionID})
	require.NotEmpty(t, armedKey)

	rootKeys := preArmKeysForTurn(rootTS)
	childKeys := preArmKeysForTurn(childTS)
	require.Contains(t, rootKeys, armedKey,
		"the root's own registration keys must include the key a chat-level Stop arms")
	require.Contains(t, childKeys, armedKey,
		"ADR-057 FR-016: rebased onto routingSessionID, the CHILD's registration keys must ALSO include "+
			"the SAME key a chat-level Stop arms under — a transcriptSessionID-keyed implementation would "+
			"break this exact invariant, since the child's transcriptSessionID differs from the root's")

	// "the keys cleared are exactly the keys set": arm a latch under armedKey
	// (simulating a chat-level Stop arriving before ANY turn is registered),
	// then consume it via the CHILD's own key set (simulating the delegate
	// child's registerActiveTurn -> consumePreArmedCancel).
	pa := newCancelPreArm()
	now := time.Now()
	pa.armLocked(armedKey, &cancelPreArmLatch{
		scope:   CancelScope{SessionID: rootSessionID},
		armedAt: now,
		ttl:     cancelPreArmTTL,
	})

	latch, ok, expired := pa.consume(now, childKeys...)
	require.True(t, ok, "the child's registration must find and consume the latch armed under the chat's own routing key")
	require.Nil(t, expired)
	require.NotNil(t, latch)
	assert.Equal(t, rootSessionID, latch.scope.SessionID, "the consumed latch must be the SAME one armed for the chat root")

	// Exactly-once: a second consume against the same keys must find nothing
	// — the keys cleared are exactly (and only) the keys set.
	_, ok2, expired2 := pa.consume(now, childKeys...)
	assert.False(t, ok2, "a latch must be consumed AT MOST once")
	assert.Nil(t, expired2)
}

// ---------------------------------------------------------------------------
// PHASE A → B → C threading (FR-024) — BDD-23/BDD-24 / #38/#39
// ---------------------------------------------------------------------------

// TestU15Cancel_PhaseB_HardAbortsLiveChild covers BDD-23: a registered root
// turn that finishes gracefully almost immediately, plus a registered
// Critical:true child turn that does NOT finish, must still be hard-aborted
// by PHASE B's 3s timer — proving PHASE B threads the PHASE-A-computed
// descendant set (al.liveTurnStatesAmong(descendants)) rather than gating
// solely on activeTurn.IsAlive() (which would already be false for the
// finished root).
func TestU15Cancel_PhaseB_HardAbortsLiveChild(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	rootSessionID := fmt.Sprintf("u15-phaseb-root-%d", nonce)
	childTranscriptID := fmt.Sprintf("u15-phaseb-child-%d", nonce)

	rootTS := u15RegisterActiveTurn(t, al, rootSessionID, "turn-u15-phaseb-root", 0, "")
	childTS := u15RegisterChildTurn(t, al, rootSessionID, childTranscriptID,
		"turn-u15-phaseb-child", "turn-u15-phaseb-root", 1, true)

	require.True(t, childTS.IsAlive(), "precondition: the Critical child must be alive before the Stop")
	require.False(t, childTS.hardAbortRequested(), "precondition: no hard-abort has been requested yet")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootSessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)

	// BDD-23's Given: the root finishes gracefully (almost immediately) while
	// the Critical child keeps running — mirrors the existing package's
	// fixture pattern (cancel_subagent_cascade_test.go) for simulating a
	// finished turn without a real turn-processing goroutine.
	rootTS.isFinished.Store(true)

	require.Eventually(t, childTS.hardAbortRequested, 4*time.Second, 50*time.Millisecond,
		"FR-024: PHASE B must hard-abort the still-live Critical child via the PHASE-A-computed "+
			"descendant set, even though the root already finished gracefully")
}

// TestU15Cancel_PhaseC_DetachesSurvivingChild covers BDD-24: the SAME tree as
// BDD-23, but the Critical child survives past PHASE B's hard-abort signal
// (it never actually terminates — a stuck goroutine) — PHASE C's 5s-after-
// hard timer must detach it (MarkAbandoned), again threading the SAME
// PHASE-A-computed descendant set rather than re-scanning.
func TestU15Cancel_PhaseC_DetachesSurvivingChild(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)

	nonce := time.Now().UnixNano()
	rootSessionID := fmt.Sprintf("u15-phasec-root-%d", nonce)
	childTranscriptID := fmt.Sprintf("u15-phasec-child-%d", nonce)

	rootTS := u15RegisterActiveTurn(t, al, rootSessionID, "turn-u15-phasec-root", 0, "")
	childTS := u15RegisterChildTurn(t, al, rootSessionID, childTranscriptID,
		"turn-u15-phasec-child", "turn-u15-phasec-root", 1, true)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootSessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)

	rootTS.isFinished.Store(true)
	require.False(t, childTS.abandoned.Load(), "precondition: not yet abandoned")

	// PHASE C fires 5s after PHASE B's hard abort (t=3s), i.e. ~t=8s after
	// the Stop. The child never actually terminates (simulating a stuck
	// goroutine that ignored the hard-abort signal), so MarkAbandoned must
	// fire against it.
	require.Eventually(t, childTS.abandoned.Load, 10*time.Second, 100*time.Millisecond,
		"FR-024: PHASE C must detach (MarkAbandoned) the still-live child 5s after the hard abort, "+
			"using the SAME PHASE-A-computed descendant set as PHASE B")
}

// ---------------------------------------------------------------------------
// Audit trail (FR-030) — BDD-25 / #40 and BDD-100 / #97
// ---------------------------------------------------------------------------

// TestU15Cancel_AuditNamesDescendants covers BDD-25: a Stop that reached one
// child must produce a turn_canceled audit entry whose descendants_canceled
// is non-empty and names the child's turn id.
func TestU15Cancel_AuditNamesDescendants(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)

	nonce := time.Now().UnixNano()
	rootSessionID := fmt.Sprintf("u15-audit-root-%d", nonce)
	childTranscriptID := fmt.Sprintf("u15-audit-child-%d", nonce)

	rootTS := u15RegisterActiveTurn(t, al, rootSessionID, "turn-u15-audit-root", 0, "")
	u15RegisterChildTurn(t, al, rootSessionID, childTranscriptID,
		"turn-u15-audit-child", "turn-u15-audit-root", 1, false)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootSessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)
	require.Contains(t, outcome.Descendants, "turn-u15-audit-child")

	require.NotNil(t, rootTS.onCancelFinish, "RequestCancel must have registered onCancelFinish")
	rootTS.onCancelFinish("graceful")

	records := readCancelAuditRecords(t, auditDir)
	var found bool
	for _, r := range records {
		if r.Event != audit.EventTurnCancelled {
			continue
		}
		found = true
		raw, ok := r.Fields["descendants_canceled"]
		require.True(t, ok, "turn_canceled audit event must carry descendants_canceled")
		names := u15DescendantNames(t, raw)
		require.NotEmpty(t, names, "BDD-25: descendants_canceled must be non-empty")
		assert.Contains(t, names, "turn-u15-audit-child")
	}
	assert.True(t, found, "expected a turn_canceled audit event")
}

// TestU15Cancel_AuditNamesEveryDescendantAtDepth3 covers BDD-100 (the
// non-holdout depth-3 twin of BDD-25/FR-030): a chat with LIVE descendants
// at depths 1, 2 and 3 must have ALL THREE named in descendants_canceled,
// not merely the depth-1 child — FR-030 says "every descendant", and this is
// the visible-plan scenario asserting it (grill M-12).
func TestU15Cancel_AuditNamesEveryDescendantAtDepth3(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("u15-depth3-root-%d", nonce)

	rootTS := u15RegisterActiveTurn(t, al, rootID, "turn-u15-d0", 0, "")
	u15RegisterChildTurn(t, al, rootID, fmt.Sprintf("u15-depth3-d1-%d", nonce), "turn-u15-d1", "turn-u15-d0", 1, false)
	u15RegisterChildTurn(t, al, rootID, fmt.Sprintf("u15-depth3-d2-%d", nonce), "turn-u15-d2", "turn-u15-d1", 2, false)
	u15RegisterChildTurn(t, al, rootID, fmt.Sprintf("u15-depth3-d3-%d", nonce), "turn-u15-d3", "turn-u15-d2", 3, false)

	wantTurnIDs := []string{"turn-u15-d0", "turn-u15-d1", "turn-u15-d2", "turn-u15-d3"}

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)
	require.Len(t, outcome.Descendants, len(wantTurnIDs),
		"root + 3 nested delegate depths, all sharing routingSessionID (FR-011 inherited verbatim), "+
			"must ALL be reached by a single chat-level Stop")
	for _, want := range wantTurnIDs {
		assert.Contains(t, outcome.Descendants, want)
	}

	require.NotNil(t, rootTS.onCancelFinish)
	rootTS.onCancelFinish("graceful")

	records := readCancelAuditRecords(t, auditDir)
	var found bool
	for _, r := range records {
		if r.Event != audit.EventTurnCancelled {
			continue
		}
		found = true
		names := u15DescendantNames(t, r.Fields["descendants_canceled"])
		for _, want := range wantTurnIDs {
			assert.Contains(t, names, want,
				"BDD-100: descendants_canceled must contain ALL THREE depths' turn ids, not merely the depth-1 child")
		}
	}
	assert.True(t, found, "expected a turn_canceled audit event")
}

// ---------------------------------------------------------------------------
// Durable descendant lifecycle-record walk (FR-025/FR-026) — BDD-30 / #45
// ---------------------------------------------------------------------------

// TestU15Cancel_TransitionsEveryDescendantLifecycleRecord_Depth3 covers
// BDD-30: a chat with children at DURABLE depths 1, 2 and 3 (persisted
// LifecycleRecords chained via ParentDurableKey, independent of any
// in-memory turnState) must have EVERY descendant's persisted record
// transitioned to cancelled by RequestCancel's own-goroutine durable walk
// (cancelDurableDescendantLifecycleRecords), not just the root's.
func TestU15Cancel_TransitionsEveryDescendantLifecycleRecord_Depth3(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)
	lifecycleStore := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(nil, lifecycleStore)

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("u15-lc-root-%d", nonce)
	d1 := fmt.Sprintf("u15-lc-d1-%d", nonce)
	d2 := fmt.Sprintf("u15-lc-d2-%d", nonce)
	d3 := fmt.Sprintf("u15-lc-d3-%d", nonce)

	u15PersistLifecycleRecord(t, lifecycleStore, d1, rootID, session.LifecycleRunning)
	u15PersistLifecycleRecord(t, lifecycleStore, d2, d1, session.LifecycleRunning)
	u15PersistLifecycleRecord(t, lifecycleStore, d3, d2, session.LifecycleRunning)

	// Positive lower bound (Rule 4's spirit): prove the fixture's own walk is
	// live before asserting the post-cancel state — all three records must
	// exist and read "running" before RequestCancel runs.
	for _, id := range []string{d1, d2, d3} {
		rec, err := lifecycleStore.Load(id)
		require.NoError(t, err)
		require.Equal(t, session.LifecycleRunning, rec.State, "precondition: %s must start running", id)
	}

	u15RegisterActiveTurn(t, al, rootID, "turn-u15-lc-root", 0, "")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Fired)

	// The durable walk runs on its own goroutine (FR-025) — poll until all
	// three depths have transitioned.
	for _, id := range []string{d1, d2, d3} {
		require.Eventually(t, func() bool {
			rec, err := lifecycleStore.Load(id)
			return err == nil && rec.State == session.LifecycleCancelled
		}, 3*time.Second, 20*time.Millisecond,
			"FR-025/FR-026: depth-3 descendant %s's persisted lifecycle record must transition to cancelled", id)
	}
}

// ---------------------------------------------------------------------------
// Orphan watchdog Critical-delegate defer/fire (ADR-045) — BDD-26/BDD-27 / #41/#42
// ---------------------------------------------------------------------------

// TestU15OrphanWatchdog_DefersWhileCriticalDelegateAlive covers BDD-26: an
// orphaned root turn with a LIVE Critical async delegate must have the
// watchdog's fire predicate defer reaping entirely — reap must never be
// invoked while the delegate survives.
func TestU15OrphanWatchdog_DefersWhileCriticalDelegateAlive(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	rootTS := u15RegisterActiveTurn(t, al, sessionID, "turn-u15-orphan-root", 0, "")
	childTS := u15RegisterChildTurn(t, al, sessionID, sessionID+"-u15-critical-child",
		"turn-u15-orphan-critical-child", "turn-u15-orphan-root", 1, true)

	var reapCalled bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalled = true },
		alwaysOrphaned,
	)

	time.Sleep(1500 * time.Millisecond) // past the 1s grace, with margin

	assert.False(t, reapCalled,
		"BDD-26: reap must be deferred entirely while a live Critical delegate survives on the session")
	assert.True(t, rootTS.IsAlive())
	assert.True(t, childTS.IsAlive())
}

// TestU15OrphanWatchdog_FiresAfterDelegateFinishes covers BDD-27: the SAME
// orphaned root, once its Critical delegate completes, must have the
// watchdog fire and reap the root on the NEXT grace-period arm.
func TestU15OrphanWatchdog_FiresAfterDelegateFinishes(t *testing.T) {
	al, sessionID := newOrphanTestAgentLoop(t)

	u15RegisterActiveTurn(t, al, sessionID, "turn-u15-orphan-root2", 0, "")
	childTS := u15RegisterChildTurn(t, al, sessionID, sessionID+"-u15-critical-child2",
		"turn-u15-orphan-critical-child2", "turn-u15-orphan-root2", 1, true)

	var reapCalledWhileAlive bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalledWhileAlive = true },
		alwaysOrphaned,
	)
	time.Sleep(1500 * time.Millisecond)
	require.False(t, reapCalledWhileAlive, "precondition: must not have reaped while the delegate was still alive")

	// The Critical delegate completes.
	childTS.isFinished.Store(true)
	require.False(t, childTS.IsAlive(), "precondition: the delegate must now report finished")

	var reapReason string
	var reapCalled bool
	al.ArmOrphanForegroundTurnWatch(sessionID, 1,
		func(reason string) { reapCalled = true; reapReason = reason },
		alwaysOrphaned,
	)

	require.Eventually(t, func() bool { return reapCalled }, 3*time.Second, 50*time.Millisecond,
		"BDD-27: once the Critical delegate finishes, the watchdog must fire and reap the root")
	assert.Equal(t, "orphan_timeout", reapReason)
}
