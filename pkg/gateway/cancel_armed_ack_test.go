// cancel_armed_ack_test.go — HIGH-severity review finding on commit 99d4e729.
//
// CancelOutcome.Armed's own doc comment (pkg/agent/cancel.go) mandates:
// "Callers surfacing Fired to a user MUST also check Armed before reporting
// a cancel as a no-op." Before this fix, handleCancel (and its two siblings,
// reapOrphanForegroundTurn and schedules.go's watchDeadline) read
// outcome.Fired and never outcome.Armed. A Stop click arriving before its
// turn registered latched correctly on the backend
// (pkg/agent/cancel_prearm.go) — the next turn to register under that
// session IS canceled the instant it does — but the click itself produced
// ZERO frames, visually identical to the pre-fix silent no-op this whole
// mechanism exists to close.
//
// Two tests, matching the two failure modes this fix must avoid:
//
//   - TestHandleCancel_ArmedLatch_SendsAckFrame proves the fix: a cancel
//     against a session with no active turn (nothing has registered yet)
//     now produces a "graceful" cancel_stage frame, because RequestCancel
//     arms a pre-registration latch (Armed:true) instead of silently
//     reporting Fired:false.
//
//   - TestHandleCancel_SecondClickOnAlreadyClaimedTurn_SendsNoAckFrame is the
//     over-signaling guard. A SECOND cancel on a turn whose first cancel
//     already claimed it is Fired:false AND Armed:false — a real, live turn
//     is still registered (ClaimCancel simply lost the race), so the
//     pre-registration latch path is never reached. This is the genuinely
//     "nothing to cancel" case and must NOT produce another ack frame.
//     Without this test, "always ack whenever !Fired" would silently
//     reintroduce the same defect class inverted: claiming progress
//     (an acknowledgement) that nothing backs.
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

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

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
