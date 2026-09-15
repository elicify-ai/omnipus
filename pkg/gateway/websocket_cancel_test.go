// websocket_cancel_test.go: tests for cancel and interrupt handling over the socket.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from websocket.go tests 2026-09-15 ---

// TestHandleCancel_ArmedLatch_SendsAckFrame drives a cancel against a session
// id that has never had a turn registered under it. RequestCancel's initial
// lookup finds nothing, so it arms a pre-registration cancel latch
// (pkg/agent/cancel_prearm.go) and returns CancelOutcome{Fired:false,
// Armed:true}. handleCancel must surface that with a "graceful" cancel_stage
// frame — the same acknowledged/pending stage value the Fired:true path
// already uses — never silence, and never a stage implying the turn was
// actually stopped (there is nothing yet to stop).
func TestHandleCancel_ArmedLatch_SendsAckFrame(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// This session id has never had a turn registered under it. Canceling it
	// is exactly the pre-registration race cancel_prearm.go exists to close.
	const sessionID = "armed-latch-never-registered-session"

	// armCancelOrFindActiveTurn (pkg/agent/cancel_prearm.go) now additionally
	// requires turnImminentForIdentity — real dispatcher evidence that a turn
	// is actually about to exist for this identity, not merely "none is
	// registered right now" (see that function's doc comment; caught by
	// TestWS_Cancel_OnlyInterruptsTargetSession,
	// websocket_multisession_test.go). PrimeSessionImminentForTest installs
	// the same real sessionWorkers evidence pkg/agent's own
	// primeImminentSessionWorker test helper does (cancel_prearm_test.go) —
	// this package cannot call that unexported helper directly, so
	// PrimeSessionImminentForTest is the exported, test-only (testing.Testing()
	// guarded) cross-package seam. It exercises the REAL gate rather than
	// forcing Armed=true, so this test still fails if turnImminentForIdentity
	// itself is deleted (see the mutation-test note in this fix's report).
	require.NoError(t, al.PrimeSessionImminentForTest(sessionID))

	cancelFrame := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	data, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	stages := readCancelStageFrames(conn, 1*time.Second)
	require.GreaterOrEqual(t, len(stages), 1,
		"an armed cancel (Fired:false, Armed:true) must produce a user-visible "+
			"cancel_stage ack frame — before this fix it produced none at all, "+
			"indistinguishable from the pre-fix silent no-op")
	assert.Equal(t, "graceful", stages[0],
		"the ack must reuse the existing 'graceful' stage value (acknowledged/"+
			"pending), never a stage implying the turn was actually canceled — "+
			"nothing has fired yet")
}

// TestHandleCancel_SecondClickOnAlreadyClaimedTurn_SendsNoAckFrame proves the
// fix does not over-signal. It drives a real turn via a provider that ignores
// context cancellation (so the turn stays registered in activeTurnStates well
// past both clicks), cancels it once (claims it — Fired:true, the ordinary
// path, unmodified by this fix), then cancels it again immediately.
//
// The second RequestCancel call finds a live, still-registered active turn
// (not nil), so the pre-registration latch path in RequestCancel is never
// reached — ClaimCancel() simply returns false because the first click
// already won the race. That is CancelOutcome{Fired:false, Armed:false}: a
// genuine no-op, and handleCancel must send nothing for it.
func TestHandleCancel_SecondClickOnAlreadyClaimedTurn_SendsNoAckFrame(t *testing.T) {
	// Not parallel — shares the "iron provider blocks for a long time" shape
	// with TestCancel_TwoStageTimer_GracefulThenHard; keep it isolated for
	// the same reason that test is.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Blocks for 20s — far longer than this test's whole run, so the turn
	// stays registered (and thus findable via GetActiveTurnHookForSession)
	// through both cancel clicks.
	ip := newIronProvider(20 * time.Second)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18803, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "iron-provider"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent. There is no implicit "main" sentinel
			// to fall back on any more (ADR-064), and handleChatMessage now
			// REFUSES a chat frame it cannot resolve an agent for rather than
			// creating a session with an empty owner -- so a config with no
			// agents produces no session_started frame at all.
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Mode: config.SandboxModeOff,
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, ip)

	ctx, cancelCtx := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run: %v", err)
		}
	}()
	// t.Cleanup runs LIFO — register cancelCtx/wait BEFORE ip.Shutdown so
	// ip.Shutdown (which unblocks the provider) fires first.
	t.Cleanup(func() {
		cancelCtx()
		select {
		case <-runDone:
		case <-time.After(cancelTestTurnStartDeadline):
			t.Logf("agent loop Run did not exit within %v", cancelTestTurnStartDeadline)
		}
	})
	t.Cleanup(ip.Shutdown)
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start iron turn"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	select {
	case <-ip.ready:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("BLOCKED: ironProvider never entered Chat")
	}

	cancelFrame := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	cancelData, err := json.Marshal(cancelFrame)
	require.NoError(t, err)

	// First click: claims the turn. Drain its "graceful" ack — this is the
	// existing, unmodified Fired:true path. This is the ONLY read on conn:
	// gorilla/websocket's Conn caches its first read error (including a
	// plain deadline timeout — see conn.go's sticky c.readErr, set via
	// hideTempErr) and replays that SAME cached error on every later
	// ReadMessage call regardless of a freshly-set deadline. A second
	// bounded "assert nothing arrives" read reusing THIS SAME conn, after
	// readCancelStageFrames's internal loop has already timed out once,
	// would therefore return empty in microseconds no matter what the
	// server sent — a vacuous pass, not a real observation (confirmed
	// empirically: a post-timeout read on the same conn returns instantly
	// even with a brand-new 500ms deadline). Using a second, independent
	// connection below for the second click's observation avoids that trap.
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))
	firstStages := readCancelStageFrames(conn, 1*time.Second)
	require.GreaterOrEqual(t, len(firstStages), 1, "first cancel must produce a graceful ack")
	assert.Equal(t, "graceful", firstStages[0])

	// Second click, same session, from a SECOND, independent connection —
	// exactly the "another tab/device clicks Stop again" shape, and it also
	// sidesteps the sticky-readErr trap above: conn2 has never had a read
	// error, so its bounded wait below is a genuine, trustworthy timeout,
	// not a poisoned instant return. The turn is still registered
	// (ironProvider is still blocking 20s, so it has not reached Finish()
	// and deregistered) and already claimed by the first click, so
	// RequestCancel finds a live activeTurn whose ClaimCancel() returns
	// false — Fired:false AND Armed:false (the pre-registration latch path
	// in RequestCancel is only reached when no active turn is found at all;
	// see cancel.go).
	conn2 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn2.Close() })
	sendWSAuthFrameDevMode(t, conn2)
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, cancelData))

	// Well under the 3s hard-abort timer the FIRST cancel armed, so any
	// cancel_stage frame observed here is attributable only to the SECOND
	// click.
	secondStages := readCancelStageFrames(conn2, 500*time.Millisecond)
	assert.Empty(t, secondStages,
		"a second cancel on an already-claimed, still-registered turn is a "+
			"genuine no-op (Fired:false, Armed:false — a live turn was found, "+
			"ClaimCancel just lost the race) and must NOT produce another ack "+
			"frame; doing so would over-signal")

	cancelCtx()
}

// TestHandleCancel_LatchExpiredUnfired_SendsHonestErrorFrame drives the real
// pre-registration-latch machinery end to end, with NO shortcuts:
//
//  1. Cancel session A, which has never had a turn registered under it —
//     RequestCancel arms a latch (Armed:true) and handleCancel's existing
//     Armed-ack path sends the ordinary "graceful" cancel_stage frame.
//  2. Wait past the real cancelPreArmTTL (5s; pkg/gateway cannot shrink
//     pkg/agent's unexported var, so this uses the real default — matching
//     this codebase's own convention of real-timer cancel tests, e.g.
//     TestCancel_TwoStageTimer_GracefulThenHard's real 3s/5s escalation).
//     Session A's latch is now armed but unconsumed: nothing ever registered
//     for it.
//  3. Cancel an UNRELATED session B (also never registered). Per
//     armLocked's doc comment (cancel_prearm.go), a completely unrelated
//     cancel's own arm call is the ONLY discovery point for a latch whose
//     target turn never registers at all — this opportunistic sweep evicts
//     A's now-expired latch and asynchronously invokes A's ORIGINAL
//     OnLatchExpired hook (captured from THIS SAME connection when A was
//     canceled in step 1).
//  4. Assert an ErrorFrame arrives for session A, honestly stating the
//     cancel did not take effect — never a second "graceful" (which would
//     claim progress that never happened).
//
// This is also this fix's MUTATION TARGET: reverting the OnLatchExpired
// field in buildCancelHooks (websocket.go) makes step 4 time out (the base
// agent-level Warn log in notifyLatchExpired still fires, unconditionally,
// but no ErrorFrame is ever sent because notifyLatchExpired nil-checks the
// hook before calling it) — a clean, real RED, not a panic.
func TestHandleCancel_LatchExpiredUnfired_SendsHonestErrorFrame(t *testing.T) {
	// Not t.Parallel(): waits out a real multi-second timer (cancelPreArmTTL).
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	const sessionA = "cancel-latch-expiry-never-registered-a"
	const sessionB = "cancel-latch-expiry-never-registered-b"

	// armCancelOrFindActiveTurn (pkg/agent/cancel_prearm.go) now additionally
	// requires turnImminentForIdentity — real dispatcher evidence a turn is
	// actually about to exist, not merely "none is registered right now" (see
	// that function's doc comment). Both A and B need this primed: A so its
	// own cancel below arms a latch at all, and B so ITS cancel in step 3
	// reaches armLocked (cancel_prearm.go) — the opportunistic sweep that
	// discovers and evicts A's now-expired latch is inside armLocked, which
	// turnImminentForIdentity gates just as much as the arm itself.
	// PrimeSessionImminentForTest is the exported, test-only
	// (testing.Testing()-guarded) cross-package equivalent of pkg/agent's own
	// unexported primeImminentSessionWorker helper (cancel_prearm_test.go) —
	// see cancel_armed_ack_test.go's identical note for the full rationale.
	require.NoError(t, al.PrimeSessionImminentForTest(sessionA))
	require.NoError(t, al.PrimeSessionImminentForTest(sessionB))

	// Step 1: cancel A — arms a latch, Armed:true. Read exactly one frame
	// (the "graceful" ack); this succeeds without ever hitting conn's read
	// deadline, so conn's read state stays clean for the later read in step 4
	// (see readAllWSFramesFor's doc comment on the sticky-read-error trap).
	cancelA := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionA}
	dataA, err := json.Marshal(cancelA)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataA))

	// readFrameOfType's only job here is to consume exactly one clean frame
	// off the wire before moving on — the Armed-ack shape itself (that this
	// is "graceful") is already covered by cancel_armed_ack_test.go.
	readFrameOfType(t, conn, "cancel_stage", 2*time.Second)

	// Step 2: wait past the real cancelPreArmTTL (5s default) so A's latch is
	// expired-but-unconsumed by the time B's cancel arrives.
	time.Sleep(5500 * time.Millisecond)

	// Step 3: cancel B — an unrelated session, also never registered. Its own
	// arm call opportunistically sweeps A's now-expired latch (armLocked,
	// cancel_prearm.go) and fires A's ORIGINAL OnLatchExpired hook async.
	cancelB := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionB}
	dataB, err := json.Marshal(cancelB)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataB))

	// Step 4: find A's honest ErrorFrame among whatever else arrives (B's own
	// "graceful" ack for its own Armed:true outcome may interleave in either
	// order — this loop skips anything that isn't the error frame we want).
	deadline := time.Now().Add(3 * time.Second)
	var found *genericWSTestFrame
	for time.Now().Before(deadline) && found == nil {
		conn.SetReadDeadline(deadline) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		var f genericWSTestFrame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if f.Type == "error" && f.SessionID == sessionA {
			found = &f
		}
	}

	require.NotNil(t, found,
		"an expired-unfired pre-registration cancel latch must produce an honest "+
			"ErrorFrame for the session it stood in for — silence here means the "+
			"user was told 'acknowledged' and never told it didn't happen")
	assert.Contains(t, found.Message, cancelLatchExpiredMessageSubstring,
		"the message must honestly state the cancel did NOT take effect")
}

// TestHandleCancel_FiredImmediately_NoLatchExpiredFalseAlarm is the
// over-signaling guard: an ordinary cancel that succeeds immediately
// (Fired:true — a live turn is found on the very first lookup, so
// RequestCancel never reaches the pre-registration-latch path at all, see
// cancel.go) must NEVER produce the OnLatchExpired ErrorFrame, no matter how
// long the observer waits. Drives a real blocking turn through the full
// graceful→hard→detached escalation (the same shape as
// TestCancel_TwoStageTimer_GracefulThenHard) and asserts across that ENTIRE
// window that no frame carries the "did not take effect" wording.
func TestHandleCancel_FiredImmediately_NoLatchExpiredFalseAlarm(t *testing.T) {
	// Not parallel — real ~8s timer sequence, same rationale as
	// TestCancel_TwoStageTimer_GracefulThenHard.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Blocks for 20s — well past the 3s+5s graceful/hard/detached window.
	ip := newIronProvider(20 * time.Second)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18804, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "iron-provider"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent. There is no implicit "main" sentinel
			// to fall back on any more (ADR-064), and handleChatMessage now
			// REFUSES a chat frame it cannot resolve an agent for rather than
			// creating a session with an empty owner -- so a config with no
			// agents produces no session_started frame at all.
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Mode: config.SandboxModeOff,
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, ip)

	ctx, cancelCtx := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancelCtx()
		select {
		case <-runDone:
		case <-time.After(cancelTestTurnStartDeadline):
			t.Logf("agent loop Run did not exit within %v", cancelTestTurnStartDeadline)
		}
	})
	t.Cleanup(ip.Shutdown)
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start iron turn"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	select {
	case <-ip.ready:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("BLOCKED: ironProvider never entered Chat")
	}

	// This turn is live and registered, so this cancel is Fired:true
	// immediately — the pre-registration latch path (and therefore
	// OnLatchExpired) is never reached at all.
	cancelFrame := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	cancelData, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))

	// ONE continuous collection window spanning graceful (immediate), hard
	// (t+3s), and detached (t+8s) — see readAllWSFramesFor's doc comment for
	// why this must be a single call rather than several sequential ones.
	frames := readAllWSFramesFor(conn, 9*time.Second)

	var sawGraceful bool
	for _, f := range frames {
		if f.Type == "cancel_stage" && f.Stage == "graceful" {
			sawGraceful = true
		}
		if f.Type == "error" {
			assert.NotContains(t, f.Message, cancelLatchExpiredMessageSubstring,
				"a real, immediately-fired cancel (no latch ever armed) must "+
					"never emit the OnLatchExpired 'did not take effect' message — "+
					"doing so would be a false alarm on every successful Stop click")
		}
	}
	assert.True(t, sawGraceful,
		"sanity check: the ordinary Fired:true cancel path must still work")
}

// TestU11CollectDescendantSessionIDs_MultiLevelWalk proves
// u11CollectDescendantSessionIDs walks the FULL subtree — a grandchild two
// delegation levels deep, not just direct children — over a REAL on-disk
// LifecycleStore. This is the mechanism BDD-19 names ("the parent index
// makes the walk cost proportional to descendants") and the exact gap FR-032
// exists to close: a chat-level Stop must reach every live descendant, not
// just the ones one hop away.
func TestU11CollectDescendantSessionIDs_MultiLevelWalk(t *testing.T) {
	ls := session.NewLifecycleStore(t.TempDir())

	const (
		root       = "sess_u11_walk_root"
		child      = "sess_u11_walk_child"
		grandchild = "sess_u11_walk_grandchild"
		sibling    = "sess_u11_walk_unrelated" // NOT a descendant of root
		other      = "sess_u11_walk_someone_else"
	)
	u11PersistLifecycleChild(t, ls, child, root)
	u11PersistLifecycleChild(t, ls, grandchild, child)
	u11PersistLifecycleChild(t, ls, sibling, other)

	got := u11CollectDescendantSessionIDs(ls, root)

	// Positive lower bound (binding rule 4) BEFORE the exclusion check below.
	if len(got) < 2 {
		t.Fatalf("must find at least the 2 real descendants (child, grandchild); got %v", got)
	}
	var foundChild, foundGrandchild, foundSibling bool
	for _, id := range got {
		switch id {
		case child:
			foundChild = true
		case grandchild:
			foundGrandchild = true
		case sibling:
			foundSibling = true
		case root:
			t.Errorf("the root itself must never appear in its own descendant list; got %v", got)
		}
	}
	if !foundChild {
		t.Errorf("child %q missing from descendant set %v", child, got)
	}
	if !foundGrandchild {
		t.Errorf("grandchild %q (two levels deep) missing from descendant set %v — "+
			"the walk must recurse past direct children, not stop at depth 1", grandchild, got)
	}
	if foundSibling {
		t.Errorf("unrelated session %q must NOT appear in root's descendant set %v", sibling, got)
	}
}

// TestU11CollectDescendantSessionIDs_NilStoreAndEmptyRoot covers the two
// degenerate inputs: a nil lifecycle store (no delegation lifecycle store
// wired — most webchat-only installs never mint one) and an empty root id.
// Both MUST degrade to an empty slice rather than panicking, so the caller
// (buildCancelHooks's CancelPendingApprovals closure) falls back to exactly
// cancelAllPendingForSession's documented single-id behavior instead of
// crashing a Stop click.
func TestU11CollectDescendantSessionIDs_NilStoreAndEmptyRoot(t *testing.T) {
	if got := u11CollectDescendantSessionIDs(nil, "sess_u11_whatever"); len(got) != 0 {
		t.Errorf("a nil store must yield zero descendants, got %v", got)
	}
	ls := session.NewLifecycleStore(t.TempDir())
	if got := u11CollectDescendantSessionIDs(ls, ""); len(got) != 0 {
		t.Errorf("an empty root id must yield zero descendants, got %v", got)
	}
}

// TestU11CollectDescendantSessionIDs_CyclicParentIndexTerminates guards
// against a corrupted/cyclic ParentDurableKey chain (two sessions each naming
// the other as parent) hanging a Stop forever. The visited-set MUST stop the
// walk, independent of whatever the system's own delegation-depth cap
// happens to be — this walk must terminate even over on-disk state that
// predates or violates that cap.
func TestU11CollectDescendantSessionIDs_CyclicParentIndexTerminates(t *testing.T) {
	ls := session.NewLifecycleStore(t.TempDir())
	const (
		a = "sess_u11_cycle_a"
		b = "sess_u11_cycle_b"
	)
	u11PersistLifecycleChild(t, ls, a, b) // a's parent is b
	u11PersistLifecycleChild(t, ls, b, a) // b's parent is a — the cycle

	done := make(chan []string, 1)
	go func() { done <- u11CollectDescendantSessionIDs(ls, a) }()
	select {
	case got := <-done:
		// From a: children-of-a == {b}; from b: children-of-b == {a}, already
		// visited, so the walk stops there.
		if len(got) != 1 || got[0] != b {
			t.Errorf("cyclic walk from %q must terminate with exactly [%q], got %v", a, b, got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("u11CollectDescendantSessionIDs did not terminate over a cyclic ParentDurableKey chain")
	}
}
