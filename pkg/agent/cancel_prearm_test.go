// Package agent — cancel_prearm_test.go
//
// Tests for the pre-registration cancel latch (cancel_prearm.go): a cancel
// that arrives before its target turn has registered in activeTurnStates
// must still stop that turn, and a latch that goes unconsumed past its TTL
// must never reach a later, unrelated turn.
//
// Bug this closes: RequestCancel used to find no turnState for a
// not-yet-registered turn, report Fired:false, and forget the request ever
// happened. The turn then registered moments later and ran to completion,
// having never seen the cancel (observed repro: two Stop clicks, a full
// completion delivered anyway, and the orphan watchdog's ClaimCancel
// succeeding 22s later on the same session).
//
// PREMISE UPDATE (this file): arming used to be unconditional whenever no
// active turn was registered. TestWS_Cancel_OnlyInterruptsTargetSession
// (pkg/gateway/websocket_multisession_test.go) proved that premise wrong —
// "no active turn" is equally true of a session about to start its first
// turn and one that just finished its last turn with nothing coming, and a
// latch armed in the second case would sit for cancelPreArmTTL and cancel
// whatever unrelated turn the user started next in that same session.
// cancel_prearm.go's armCancelOrFindActiveTurn now additionally requires
// turnImminentForIdentity — real evidence (a queued or in-flight message on
// the identity's sessionWorker) that a turn is actually coming. Every test
// below that expects Armed:true now calls primeImminentSessionWorker(TierB)
// first to construct that evidence explicitly, so these tests keep
// validating the arm+consume MECHANISM correctly under the precondition
// that now actually gates it, rather than relying on the over-broad
// behavior. TestPreArmedCancel_FinishedSession_DoesNotArmOrPoisonNextTurn
// (bottom of this file) is the new test covering the case that changed.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newPreArmTestTurnState builds the same minimal bare turnState shape the
// existing cancel_test.go / cancel_race_test.go suites already use to drive
// RequestCancel without a full runTurn lifecycle.
//
// ADR-057 fixture repair: preArmKeysForTurn (cancel_prearm.go) now derives
// its "s:"+... key from ts.routingSessionID, not transcriptSessionID (FR-016).
// A root turn's routingSessionID defaults to its own transcriptSessionID
// (turn.go newTurnState, FR-011) — mirrored explicitly here since this
// fixture bypasses that constructor.
func newPreArmTestTurnState(turnID, sessionKey, transcriptSessionID, channel, chatID string) *turnState {
	return &turnState{
		turnID:              turnID,
		sessionKey:          sessionKey,
		transcriptSessionID: transcriptSessionID,
		routingSessionID:    session.RoutingSessionID(transcriptSessionID),
		channel:             channel,
		chatID:              chatID,
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
}

// primeImminentSessionWorker registers a minimal fake sessionWorker so
// turnImminentForIdentity (cancel_prearm.go) reports a turn as genuinely
// imminent for sessionID — simulating "a message has been dequeued and is
// mid-registration" (inTurn=true), which is the real-world condition
// armCancelOrFindActiveTurn now requires before it will arm a latch. Without
// this, RequestCancel correctly refuses to arm for a bare session id that
// carries no evidence a turn is actually coming (see
// TestPreArmedCancel_FinishedSession_DoesNotArmOrPoisonNextTurn below, and
// TestWS_Cancel_OnlyInterruptsTargetSession,
// pkg/gateway/websocket_multisession_test.go, the integration test that
// caught the pre-fix over-broad behavior these unit tests used to encode
// unconditionally).
//
// Delegates to AgentLoop.PrimeSessionImminentForTest (cancel_prearm.go) —
// the same cross-package seam pkg/gateway's WS integration tests use (they
// cannot construct the unexported *sessionWorker type this package's own
// helper used to build inline) — so both packages exercise the identical
// mechanism rather than two parallel ones. The error return is ignored via
// panic, not a silent drop: every call site here passes a non-empty
// sessionID from within `go test`, so a non-nil error can only mean this
// helper itself was mis-called, which should fail loudly, not quietly no-op.
func primeImminentSessionWorker(al *AgentLoop, sessionID string) {
	if err := al.PrimeSessionImminentForTest(sessionID); err != nil {
		panic(fmt.Sprintf("primeImminentSessionWorker: %v", err))
	}
}

// primeImminentSessionWorkerTierB is primeImminentSessionWorker's (channel,
// chatID) counterpart — see turnImminentForIdentity's doc comment for why
// the scope key must contain both, lower-cased, for its Tier B substring
// match. Delegates to AgentLoop.PrimeChannelChatImminentForTest for the same
// reason primeImminentSessionWorker delegates to PrimeSessionImminentForTest
// above.
func primeImminentSessionWorkerTierB(al *AgentLoop, channel, chatID string) {
	if err := al.PrimeChannelChatImminentForTest(channel, chatID); err != nil {
		panic(fmt.Sprintf("primeImminentSessionWorkerTierB: %v", err))
	}
}

// readCancelAuditRows parses every audit.jsonl line under dir into a raw
// field map, unlike readCancelAuditEvents (cancel_test.go) which only
// extracts the event name — needed here to assert on the "armed" field.
func readCancelAuditRows(t *testing.T, dir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var rows []map[string]any
	for _, line := range splitCancelTestLines(data) {
		if len(line) == 0 {
			continue
		}
		var row map[string]any
		if json.Unmarshal(line, &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows
}

// ---------------------------------------------------------------------------
// Outcome reporting: "was_fired must stop lying"
// ---------------------------------------------------------------------------

// TestRequestCancel_PreArm_ReportsArmedNotBareFalse verifies that a cancel
// arriving with no active turn now reports Armed:true alongside Fired:false,
// and that the turn_cancel_attempt audit row carries armed:true — the API
// must not represent "a latch now stands in for this cancel" identically to
// "nothing happened at all".
func TestRequestCancel_PreArm_ReportsArmedNotBareFalse(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)

	// Construct the precondition turnImminentForIdentity now requires: a
	// message genuinely in flight for this identity, not merely "no active
	// turn registered" (see the file-level PREMISE UPDATE comment above).
	primeImminentSessionWorker(al, "sess-prearm-report")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-prearm-report"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.False(t, outcome.Fired, "no active turn yet → Fired must be false")
	assert.True(t, outcome.Armed, "no active turn yet → a latch must be armed, reported via Armed")

	rows := readCancelAuditRows(t, auditDir)
	var sawArmedAttempt bool
	for _, r := range rows {
		if r["event"] != audit.EventTurnCancelAttempt {
			continue
		}
		fields, _ := r["fields"].(map[string]any)
		if armed, ok := fields["armed"].(bool); ok && armed {
			sawArmedAttempt = true
		}
	}
	assert.True(t, sawArmedAttempt, "turn_cancel_attempt audit row must carry fields.armed:true")

	// Clean up the latch so it cannot leak into another test sharing al's
	// process-wide state (it does not here — al is per-test — but keeps the
	// test self-documenting about what state it created).
	al.cancelPreArm.consume(time.Now(), "s:sess-prearm-report")
}

// ---------------------------------------------------------------------------
// Half (a): an early cancel is honoured by the turn that follows
// ---------------------------------------------------------------------------

// TestPreArmedCancel_AppliesToTurnThatRegistersNext is the core regression
// test: RequestCancel runs BEFORE any turn is registered (arms a latch),
// then a turn registers under the SAME identity — registerActiveTurn must
// consume the latch and cancel that turn immediately, synchronously, before
// this test's call to registerActiveTurn even returns.
func TestPreArmedCancel_AppliesToTurnThatRegistersNext(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)

	const sessionID = "sess-prearm-2"

	// 0. Construct the precondition turnImminentForIdentity now requires: a
	// message genuinely in flight for this identity (simulating the real
	// race — the worker has dequeued the message and is mid-processMessage,
	// before registerActiveTurn runs), not merely "no active turn
	// registered." See the file-level PREMISE UPDATE comment above.
	primeImminentSessionWorker(al, sessionID)

	// 1. Cancel arrives first — no turn registered yet.
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.False(t, outcome.Fired)
	require.True(t, outcome.Armed, "precondition: the cancel must have armed a latch")

	// 2. The turn registers moments later.
	ts := newPreArmTestTurnState("turn-prearm-2", "sess-prearm-2-key", sessionID, "", "")
	al.registerActiveTurn(ts)
	defer al.clearActiveTurn(ts)

	// 3. The turn must already be canceled — ClaimCancel already consumed —
	// by the time registerActiveTurn returns (synchronous consumption).
	assert.True(t, ts.cancelFired.Load(),
		"a turn registering under a session with an armed pre-cancel latch must be canceled immediately")
	assert.False(t, ts.ClaimCancel(),
		"cancelFired must already be true — a second ClaimCancel must find it already claimed")

	// 4. The full cascade must have run, not just the cancelFired flag: the
	// onCancelFinish callback (transcript + turn_canceled audit on Finish)
	// must be registered, exactly as it would be for a cancel that arrived
	// after registration.
	ts.mu.RLock()
	onFinish := ts.onCancelFinish
	ts.mu.RUnlock()
	assert.NotNil(t, onFinish, "RequestCancel's onCancelFinish callback must be wired by latch consumption")
}

// TestPreArmedCancel_TierB_ChannelChatIDKey verifies the fallback key form:
// when the caller has no session id (Tier B channels), the latch is keyed on
// (channel, chatID) and still cancels the turn that later registers with a
// matching transcriptSessionID under that channel/chatID pair.
func TestPreArmedCancel_TierB_ChannelChatIDKey(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)

	// Construct the Tier B precondition turnImminentForIdentity now
	// requires: a message genuinely in flight for (channel, chatID). See the
	// file-level PREMISE UPDATE comment above.
	primeImminentSessionWorkerTierB(al, "telegram", "chat-prearm-1")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{Channel: "telegram", ChatID: "chat-prearm-1"},
		CancelCanceller{UserID: "@user", Channel: "telegram"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.False(t, outcome.Fired)
	require.True(t, outcome.Armed)

	ts := newPreArmTestTurnState("turn-prearm-tb", "sess-prearm-tb-key", "sess-prearm-tb", "telegram", "chat-prearm-1")
	al.registerActiveTurn(ts)
	defer al.clearActiveTurn(ts)

	assert.True(t, ts.cancelFired.Load(),
		"a turn registering under the (channel, chatID) a Tier B cancel armed must be canceled")
}

// ---------------------------------------------------------------------------
// Half (b): an expired or mismatched latch does NOT cancel a later turn
// ---------------------------------------------------------------------------

// TestPreArmedCancel_ExpiredLatch_DoesNotCancelSubsequentTurn is the
// protection half of the fix: a latch that outlives cancelPreArmTTL before
// any turn consumes it must be discarded, not applied to whatever turn
// registers next under that identity.
func TestPreArmedCancel_ExpiredLatch_DoesNotCancelSubsequentTurn(t *testing.T) {
	// Not t.Parallel(): mutates the package-level cancelPreArmTTL var.
	origTTL := cancelPreArmTTL
	cancelPreArmTTL = 20 * time.Millisecond
	t.Cleanup(func() { cancelPreArmTTL = origTTL })

	al := newCancelTestAgentLoop(t)

	const sessionID = "sess-prearm-expired"

	// Construct the precondition turnImminentForIdentity now requires. See
	// the file-level PREMISE UPDATE comment above.
	primeImminentSessionWorker(al, sessionID)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Armed, "precondition: the cancel must have armed a latch")

	// Let the latch age past its (shrunk) TTL before any turn registers.
	time.Sleep(60 * time.Millisecond)

	ts := newPreArmTestTurnState("turn-prearm-expired", "sess-prearm-expired-key", sessionID, "", "")
	al.registerActiveTurn(ts)
	defer al.clearActiveTurn(ts)

	assert.False(t, ts.cancelFired.Load(),
		"an expired latch must NOT cancel the turn that registers after it has aged out")
}

// TestPreArmedCancel_KeyMismatch_DoesNotCancelUnrelatedTurn verifies the
// other half of "provably cannot stop a later, unrelated turn": a latch
// armed for session A must not touch a turn registering under an unrelated
// session B, and must still be there (unconsumed) for the turn it was
// actually meant for.
func TestPreArmedCancel_KeyMismatch_DoesNotCancelUnrelatedTurn(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)

	const sessionA = "sess-prearm-a"
	const sessionB = "sess-prearm-b"

	// Construct the precondition turnImminentForIdentity now requires, for
	// session A only — session B never has a cancel requested against it in
	// this test, only a turn registered directly. See the file-level
	// PREMISE UPDATE comment above.
	primeImminentSessionWorker(al, sessionA)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionA},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	require.True(t, outcome.Armed)

	// An unrelated turn for session B registers first.
	tsB := newPreArmTestTurnState("turn-prearm-b", "sess-prearm-b-key", sessionB, "", "")
	al.registerActiveTurn(tsB)
	defer al.clearActiveTurn(tsB)
	assert.False(t, tsB.cancelFired.Load(), "session B's latch-free turn must be unaffected by session A's armed latch")

	// The turn the latch was actually meant for (session A) still gets
	// canceled when it registers afterward — proves the mismatch above
	// didn't accidentally consume/drop the real latch.
	tsA := newPreArmTestTurnState("turn-prearm-a", "sess-prearm-a-key", sessionA, "", "")
	al.registerActiveTurn(tsA)
	defer al.clearActiveTurn(tsA)
	assert.True(t, tsA.cancelFired.Load(), "session A's own turn must still be canceled by its own latch")
}

// ---------------------------------------------------------------------------
// Concurrency proof
// ---------------------------------------------------------------------------

// TestPreArmedCancel_ConcurrentArmAndRegister_AlwaysCancels drives
// RequestCancel and registerActiveTurn concurrently, for many independent
// session identities, with randomized jitter biasing which side runs first
// across iterations. Run with -race: this is the direct proof that
// armCancelOrFindActiveTurn's lock (cancel_prearm.go) and
// registerActiveTurn's consume step can never BOTH miss each other —
// regardless of interleaving, the turn always ends up canceled.
func TestPreArmedCancel_ConcurrentArmAndRegister_AlwaysCancels(t *testing.T) {
	al := newCancelTestAgentLoop(t)

	const iterations = 200
	var wg sync.WaitGroup
	results := make([]*turnState, iterations)

	for i := 0; i < iterations; i++ {
		sessionID := fmt.Sprintf("sess-race-%03d", i)
		wg.Add(2)

		// Construct the precondition turnImminentForIdentity now requires,
		// for EVERY iteration's identity, before either goroutine below can
		// run — otherwise the "cancel first" half of iterations would
		// correctly refuse to arm (nothing imminent yet from
		// turnImminentForIdentity's point of view) and this test would stop
		// proving what it says it proves. See the file-level PREMISE UPDATE
		// comment above.
		primeImminentSessionWorker(al, sessionID)

		cancelFirst := i%2 == 0

		go func(sid string, cancelFirst bool) {
			defer wg.Done()
			if !cancelFirst {
				time.Sleep(time.Duration(i%7) * time.Microsecond)
			}
			_, _ = al.RequestCancel(
				context.Background(),
				CancelScope{SessionID: sid},
				CancelCanceller{UserID: "alice", Channel: "web"},
				CancelHooks{},
			)
		}(sessionID, cancelFirst)

		ts := newPreArmTestTurnState("turn-"+sessionID, sessionID+"-key", sessionID, "", "")
		results[i] = ts
		go func(cancelFirst bool) {
			defer wg.Done()
			if cancelFirst {
				time.Sleep(time.Duration(i%7) * time.Microsecond)
			}
			al.registerActiveTurn(ts)
		}(cancelFirst)
	}
	wg.Wait()

	for i, ts := range results {
		if !assert.True(t, ts.cancelFired.Load(), "iteration %d: turn must have been canceled regardless of arm/register ordering", i) {
			break
		}
		al.clearActiveTurn(ts)
	}
}

// ---------------------------------------------------------------------------
// The case that changed: a finished/idle session must not arm, and must not
// poison whatever turn the user starts next
// ---------------------------------------------------------------------------

// TestPreArmedCancel_FinishedSession_DoesNotArmOrPoisonNextTurn is the
// regression test for the design flaw TestWS_Cancel_OnlyInterruptsTargetSession
// (pkg/gateway/websocket_multisession_test.go) caught: RequestCancel used to
// arm a pre-registration latch whenever no active turn was registered for an
// identity — with no check for whether a turn was actually about to exist.
// A cancel on a session that had simply FINISHED its only turn (nothing
// queued, nothing in flight) armed identically to a cancel that arrived a
// moment before a real turn's registration, which is wrong in two ways this
// test pins down separately:
//
//  1. The user-visible symptom: canceling a finished session must be a true
//     no-op (Armed:false), not "acknowledged, pending" — the WS integration
//     test asserts the corollary of this at the wire level (no cancel_stage
//     frame emitted).
//  2. The dangerous one — the actual reason this is a design flaw and not
//     just a cosmetic over-report: an unconditionally-armed latch on a
//     finished session sits there for cancelPreArmTTL (5s) and will cancel
//     the NEXT, wholly unrelated turn the user starts in that same session
//     if they do so within the window — silently stopping work the user
//     just asked for. This is exactly the hazard cancel_prearm.go's own top
//     doc comment warns a latch with no bound would create; the TTL and
//     exactly-once protections bound HOW LONG and HOW MANY TIMES a stale
//     latch can fire, but neither one prevents arming a latch that should
//     never have existed in the first place — only turnImminentForIdentity
//     does that.
//
// Deliberately does NOT call primeImminentSessionWorker: the whole point is
// that NO evidence of an imminent turn exists for this identity.
func TestPreArmedCancel_FinishedSession_DoesNotArmOrPoisonNextTurn(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)

	const sessionID = "sess-prearm-finished"

	// Cancel arrives for a session with nothing active and nothing queued —
	// the "just finished, nothing coming" state, not "about to start."
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.False(t, outcome.Fired, "no active turn → Fired must be false")
	assert.False(t, outcome.Armed,
		"no active turn AND no evidence a turn is imminent → this must be a genuine no-op, not an armed latch")

	// A brand-new, wholly unrelated turn registers under the SAME session id
	// shortly after — well within cancelPreArmTTL. Under the pre-fix
	// unconditional-arm behavior, THIS turn would be the one spuriously
	// canceled: "a latch that outlives its turn would cancel the next one"
	// (cancel_prearm.go's own top doc comment), made concrete.
	ts := newPreArmTestTurnState("turn-prearm-finished-next", "sess-prearm-finished-key", sessionID, "", "")
	al.registerActiveTurn(ts)
	defer al.clearActiveTurn(ts)

	assert.False(t, ts.cancelFired.Load(),
		"a new turn registering after a finished-session cancel must NOT be canceled by a stale latch that should never have armed")
}
