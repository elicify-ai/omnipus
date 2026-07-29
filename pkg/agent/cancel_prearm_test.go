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
)

// newPreArmTestTurnState builds the same minimal bare turnState shape the
// existing cancel_test.go / cancel_race_test.go suites already use to drive
// RequestCancel without a full runTurn lifecycle.
func newPreArmTestTurnState(turnID, sessionKey, transcriptSessionID, channel, chatID string) *turnState {
	return &turnState{
		turnID:              turnID,
		sessionKey:          sessionKey,
		transcriptSessionID: transcriptSessionID,
		channel:             channel,
		chatID:              chatID,
		depth:               0,
		finishedChan:        make(chan struct{}),
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
