// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cancel_scope_fix_test.go — FIX-5 regression coverage for the ADR-057
// review-wave fixes to pkg/agent/session_messaging_wire.go and
// pkg/agent/cancel.go:
//
//   - Defect 1: delegate action="cancel" now wires ScopeSubtree (D8/R-13),
//     proven END TO END through the REAL wiring call site — not just the
//     al.Interrupt primitive session_messaging_wire_adr057_test.go already
//     covers. That file's TestSetCancelHooks_ChildCancelReachesSubtree /
//     TestSetCancelHooks_HardVariantAlsoUsesScopeSubtree call al.Interrupt /
//     al.InterruptSessionHard DIRECTLY with an explicit scope constant, so
//     they pass identically regardless of which scope
//     wireSessionMessagingForAgent actually wires — they document/pin the
//     scope CONTRACT but do not exercise the wiring itself. TestDelegate...
//     below closes that gap: it drives a REAL *tools.DelegateTool registered
//     by a REAL *AgentLoop's own boot path, wires it via the REAL
//     SetSessionMessagingStores → wireSessionMessagingForAgent path, and
//     invokes the tool's real action="cancel" dispatch — the actual
//     regression surface a future "simplify this back to ScopeSelfOnly" edit
//     would break.
//   - Defect 2/4: CollectDescendantSessionIDs (hoisted, pkg/agent/cancel.go)
//     now returns a non-nil error when a lifecycleStore.List call fails
//     partway through the walk, instead of silently truncating the returned
//     set. Proven against a REAL on-disk session.LifecycleStore with one
//     corrupted node, per binding Rule 1 (no spies).
//   - Defect 3b: RequestCancel's PHASE-A/Interrupt consistency guard now
//     detects a same-SIZE-but-different-MEMBERSHIP mismatch, not just a
//     length mismatch.
//
// Per binding Rule 4, every new test here was run against the pre-fix code
// and observed to fail for the documented reason BEFORE the corresponding
// fix landed.
package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestDelegateCancel_WiredThroughRealAgentLoop_ReachesGrandchild is the
// end-to-end proof for Defect 1: a delegate(action="cancel") issued against
// a REAL *tools.DelegateTool — registered by a REAL *AgentLoop's own default
// agent, wired via the REAL SetSessionMessagingStores →
// wireSessionMessagingForAgent path (not a hand-installed stub hook) — must
// reach the target child's own live descendant (a grandchild delegate),
// closing the ADR-057 D8/R-13 leak: before this fix, a per-delegation cancel
// left the child's own grandchildren (and their background shells) running
// forever.
//
// Every turnState here is registered via the real production path
// (al.registerActiveTurn, via u19FixtureTurns from
// session_messaging_wire_adr057_test.go) and every lifecycle record is
// persisted to a REAL on-disk session.LifecycleStore (per binding Rule 1 —
// no spies).
func TestDelegateCancel_WiredThroughRealAgentLoop_ReachesGrandchild(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	parent, child, grandchild, fixtureCleanup := u19FixtureTurns(t, al, "e2e-cancel")
	defer fixtureCleanup()

	// The lifecycle record is what delegate.executeCancel's ownership check
	// and terminal check consult — it must name child.sessionKey (the
	// "session_id" the tool call below targets) as a direct child of
	// parent.sessionKey (the caller-owner key the test's context asserts).
	lifecycleStore := session.NewLifecycleStore(t.TempDir())
	require.NoError(t, lifecycleStore.Persist(&session.LifecycleRecord{
		SessionID:        child.sessionKey,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		OwnerScopeID:     parent.sessionKey,
		ParentDurableKey: parent.sessionKey,
		AgentID:          "main",
	}))

	// Real production wiring: installs the store AND re-wires every
	// currently-registered agent's delegate tool (SetLifecycleStore +
	// SetCancelHooks with the FIXED ScopeSubtree scope) — the exact call the
	// gateway makes at boot.
	al.SetSessionMessagingStores(nil, lifecycleStore)

	inst := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, inst, "AgentLoop has no default agent registered")
	toolIface, ok := inst.Tools.Get("delegate")
	require.True(t, ok, "default agent has no 'delegate' tool registered")
	dt, ok := toolIface.(*tools.DelegateTool)
	require.True(t, ok, "'delegate' tool is not a *tools.DelegateTool")

	// FR-196 kill switch: a bare test config has session_messaging.enabled
	// unset (false), which fails-closed the "cancel" action before it ever
	// reaches the scope under test. wireSessionMessagingForAgent (called by
	// SetSessionMessagingStores above) installs that live-false reader, so
	// this override MUST come AFTER it — exactly mirroring
	// wiring_adr057_fix_test.go's own pattern for the same precondition.
	dt.SetSessionMessagingEnabled(func() bool { return true })

	callerCtx := tools.WithTranscriptSessionID(context.Background(), parent.sessionKey)
	result := dt.Execute(callerCtx, map[string]any{
		"action":     "cancel",
		"session_id": child.sessionKey,
		"hard":       false,
	})
	require.False(t, result.IsError, "delegate cancel failed: %s", result.ForLLM)

	childInterrupted, _ := child.gracefulInterruptRequested()
	assert.True(t, childInterrupted, "the named child's own graceful-interrupt flag must be set")

	grandchildInterrupted, _ := grandchild.gracefulInterruptRequested()
	assert.True(t, grandchildInterrupted,
		"delegate action=cancel, dispatched through the REAL wireSessionMessagingForAgent wiring, "+
			"must reach the child's own grandchild — this is the ADR-057 D8/R-13 fix. Before it, "+
			"session_messaging_wire.go wired ScopeSelfOnly and this assertion would fail: the "+
			"grandchild (and any background shells it owns) would be left running forever")
}

// TestCollectDescendantSessionIDs_PartialFailureReturnsErrorAndPartialSet is
// Defect 2/4's proof: a real on-disk session.LifecycleStore where ONE node's
// children query fails (simulated by corrupting that node's on-disk
// day-partition record so LifecycleStore.List errors for it, per binding
// Rule 1 — a real store, not a spy) must surface a non-nil error AND still
// return the PARTIAL descendant set actually discovered before the failure —
// never silently drop the failed branch and report a clean, complete walk.
//
// Per binding Rule 4 (positive lower bound before any zero-count assertion):
// this asserts the walk found the one healthy branch (len >= 1) BEFORE
// asserting the corrupted branch's own descendant is absent — a walk that
// returned an empty slice AND a nil error would otherwise look identical to
// "the corrupted node legitimately has no children", which is exactly the
// silent-truncation defect being fixed here.
func TestCollectDescendantSessionIDs_PartialFailureReturnsErrorAndPartialSet(t *testing.T) {
	dir := t.TempDir()
	ls := session.NewLifecycleStore(dir)

	const (
		root         = "sess_fix5_root"
		healthyChild = "sess_fix5_healthy_child" // root's child; walk must reach this
		badChild     = "sess_fix5_bad_child"     // root's OTHER child; its OWN List call will fail
		badGrandkid  = "sess_fix5_bad_grandkid"  // badChild's child — unreachable once badChild's list fails
	)
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID: healthyChild, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: root, ParentDurableKey: root,
	}))
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID: badChild, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: root, ParentDurableKey: root,
	}))
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID: badGrandkid, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: badChild, ParentDurableKey: badChild,
	}))

	// Force ls.List(LifecycleFilter{ParentDurableKey: badChild}) to error:
	// listByParentDurableKey resolves badChild's children from the in-memory
	// parent index (here: [badGrandkid]) and then calls s.Load(badGrandkid)
	// for each — if THAT Load fails with anything other than
	// ErrLifecycleNotFound (a torn/missing-file self-heals silently; see
	// lifecycle.go's own doc comment), the WHOLE List call returns that
	// error immediately, exactly the "one corrupt record fails the whole
	// query" defect. Reproduced against genuine on-disk state (binding Rule
	// 1, no spies): overwrite badGrandkid's own .jsonl with a single line
	// longer than LifecycleStore's 10MB bufio.Scanner buffer, which makes
	// scanner.Err() return bufio.ErrTooLong — a real I/O-layer failure, not
	// a JSON-parse error (a torn/malformed JSON line is deliberately
	// tolerated by tail()'s per-line skip and would NOT reproduce this
	// defect).
	corruptLifecycleRecordFile(t, dir, badGrandkid)

	descendants, err := CollectDescendantSessionIDs(ls, root)

	require.Error(t, err, "a lifecycleStore.List failure partway through the walk must be reported, "+
		"not silently swallowed as 'this node has no children'")
	assert.Contains(t, err.Error(), "descendant walk incomplete",
		"the error must clearly say the walk is INCOMPLETE, not just name the underlying I/O error")

	// Positive lower bound (binding Rule 4) BEFORE the exclusion checks below.
	require.GreaterOrEqual(t, len(descendants), 1,
		"the healthy branch must still be present in the PARTIAL result — a failure in one branch "+
			"must not blank out the whole walk")
	assert.Contains(t, descendants, healthyChild, "the healthy branch must be reached despite the sibling failure")
	assert.Contains(t, descendants, badChild, "badChild itself is still a direct, successfully-listed child of root")
	assert.NotContains(t, descendants, badGrandkid,
		"badGrandkid is unreachable once badChild's OWN List call fails — this is the exact "+
			"'every descendant beneath the failure point silently vanishes' defect; the fix's job is "+
			"making that fact OBSERVABLE (via the returned error), not making the walk somehow still "+
			"reach past a genuinely failed query")
}

// TestCollectDescendantSessionIDs_NilStoreAndEmptyRoot pins the two
// degenerate, non-error inputs (Defect 4's "behavior that must be
// preserved"): a nil lifecycle store and an empty root id both yield (nil,
// nil) — never an error, and never treated the same as a genuine walk
// failure elsewhere in this file.
func TestCollectDescendantSessionIDs_NilStoreAndEmptyRoot(t *testing.T) {
	descendants, err := CollectDescendantSessionIDs(nil, "sess_fix5_whatever")
	assert.Nil(t, descendants)
	assert.NoError(t, err)

	ls := session.NewLifecycleStore(t.TempDir())
	descendants, err = CollectDescendantSessionIDs(ls, "")
	assert.Nil(t, descendants)
	assert.NoError(t, err)
}

// TestStringSliceSetDiff_Fix3b is Defect 3b's proof at the pure-function
// level: a same-SIZE-but-different-MEMBERSHIP pair (the exact class the old
// len(a) != len(b) guard in RequestCancel could not see) must be reported as
// a real mismatch, with each side's diverging elements named — not silently
// treated as equal because the counts happen to match.
func TestStringSliceSetDiff_Fix3b(t *testing.T) {
	t.Run("same size, different membership — the defect's exact shape", func(t *testing.T) {
		collected := []string{"turn-X"}
		interrupted := []string{"turn-Y"} // same length, different turn entirely
		onlyInCollected, onlyInInterrupted := stringSliceSetDiff(collected, interrupted)
		require.Len(t, onlyInCollected, 1, "turn-X must be flagged as missing from what Interrupt actually reached")
		assert.Equal(t, "turn-X", onlyInCollected[0])
		require.Len(t, onlyInInterrupted, 1, "turn-Y must be flagged as reached but not in the pre-collected/audited set")
		assert.Equal(t, "turn-Y", onlyInInterrupted[0])
	})

	t.Run("identical sets, different order — must report no mismatch", func(t *testing.T) {
		onlyInA, onlyInB := stringSliceSetDiff([]string{"a", "b", "c"}, []string{"c", "a", "b"})
		assert.Empty(t, onlyInA)
		assert.Empty(t, onlyInB)
	})

	t.Run("both empty — must report no mismatch", func(t *testing.T) {
		onlyInA, onlyInB := stringSliceSetDiff(nil, nil)
		assert.Empty(t, onlyInA)
		assert.Empty(t, onlyInB)
	})
}

// corruptLifecycleRecordFile overwrites sessionID's on-disk
// <storeDir>/<sessionID>.jsonl with a single line longer than
// session.LifecycleStore's 10MB bufio.Scanner buffer, forcing a genuine,
// reproducible I/O-layer failure (bufio.ErrTooLong) the next time
// LifecycleStore.Load(sessionID) — and therefore any List call that must
// Load this id — runs. This targets the SCANNER'S buffer limit specifically
// (not JSON validity): a merely-malformed JSON line is deliberately
// tolerated by LifecycleStore's tail() (skipped, non-fatal), so reproducing
// the "one corrupt record fails the whole query" defect requires an error
// class tail() does NOT already swallow.
func corruptLifecycleRecordFile(t *testing.T, storeDir, sessionID string) {
	t.Helper()
	path := filepath.Join(storeDir, sessionID+".jsonl")
	// One line, no newline, comfortably past the 10MB max token size
	// (session/lifecycle.go: scanner.Buffer(64KB initial, 10MB max)).
	oversized := bytes.Repeat([]byte("x"), 11*1024*1024)
	require.NoError(t, os.WriteFile(path, oversized, 0o600),
		"fixture: could not overwrite %s with an oversized line", path)
}
