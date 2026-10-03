package email

// W1 §4.10.1 / §8 row 19; MC-W1-21, B-W1-27, B-W1-28, CX-10, DS-6 rows 1-2.
// Gate finding (pr-test-analyzer #1): the bounded-parallel watcher progress
// area had no test — the semaphore was referenced by production only, and the
// per-mailbox single-flight guard was never exercised.
//
// Oracles are derived from the spec, never from the implementation:
//   - B-W1-27: thirteen due mailboxes, one stalled at the server; the other
//     twelve complete within the pass while the stall is held, and the
//     stalled mailbox records no success. CX-10's careless sequential loop
//     completes ≤2 in the pass window — killed deterministically here by
//     placing the stalled mailbox FIRST in the provider order, so a
//     sequential loop blocks at index 0 and never reaches the others.
//   - MC-W1-21: "bounded parallel due-cycle progress". The spec pins no
//     numeric watcher bound — §4.10.1 requires "a global bound consistent
//     with the account and pool ceilings", and B-W1-5/FR-W1-21 pin the pool's
//     process-wide 8-socket ceiling the watcher must ride under. The bound is
//     therefore asserted from observation into the spec-derived envelope
//     [2, 8]: ≥2 proves parallel progress (a sequential or semaphore-of-1
//     scheduler never overlaps two cycles), ≤8 proves the pool ceiling holds.
//     The constant is deliberately never imported.
//   - B-W1-28: a mailbox whose cycle is still running gets no second cycle —
//     observed server-side: exactly one accepted connection while the first
//     is in flight.
//
// The stall is real, not mocked: each blocked cycle is parked at the network
// edge by a stubIMAPServer in greeting-stall mode (accepts TCP, never speaks
// — the same scripted-peer harness the deadline bounds use), and the probe
// rides the real pooled client path (SetSessionSource), so every dial is a
// counted pool establishment (FR-W1-21).

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// watcherSetProgressMailboxes builds n set mailboxes whose pooled clients all
// carry distinct pair scopes (§4.1: the pair is the pool identity — without a
// per-pair scope the clients would share one endpoint-only identity and its
// 2-socket ceiling would turn healthy cycles into skips). Mailbox 0 dials the
// stall server when one is given; the rest dial the normal server.
func watcherSetProgressMailboxes(t *testing.T, sessions *MailSessions, stall, normal *stubIMAPServer, n int) []Mailbox {
	t.Helper()
	mbs := make([]Mailbox, n)
	for i := range mbs {
		target := normal
		if i == 0 && stall != nil {
			target = stall
		}
		mbs[i] = Mailbox{
			AgentID:     fmt.Sprintf("agent-pr-%d", i),
			WorkspaceID: fmt.Sprintf("ws-pr-%d", i),
			Transport: facadeWithScope(t, target, sessions,
				fmt.Sprintf("agent-pr-%d/ws-pr-%d", i, i), "gen-1"),
		}
	}
	return mbs
}

// preElapsedStagger shifts the set's stagger anchor into the past. The
// stagger gate (MC-33) defers mailbox i's FIRST cycle to i × the cycle
// interval from the anchor — a 13-mailbox set's last mailbox would otherwise
// wait 12 minutes, which is the stagger working as specified but unusable for
// a scheduler test. These tests assert B-W1-27/28, not the stagger (which has
// its own test, TestWatcherSet_FirstCyclesStaggerAcrossSlots), so the anchor
// is pre-elapsed with the same-package white-box time-travel that test uses;
// the production stagger comparison itself runs unmodified on every pass.
func preElapsedStagger(t *testing.T, s *MailboxWatcherSet) {
	t.Helper()
	s.start = time.Now().Add(-time.Hour)
}

// TestWatcherSet_StalledMailboxDoesNotStallDuePass asserts B-W1-27 / CX-10 /
// MC-W1-21: with thirteen due mailboxes and mailbox 0 stalled at the server,
// the other twelve complete their checks within the pass while the stall is
// still held, and the stalled mailbox records no success. Releasing the
// stall, the pass finishes and the stalled mailbox's cycle completes as a
// visible failure — never a silent success.
func TestWatcherSet_StalledMailboxDoesNotStallDuePass(t *testing.T) {
	stall := newStubIMAPServer(t)
	stall.setMode(stubStallGreeting)
	normal := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, normal, newPoolTestClock())
	const n = 13
	mbs := watcherSetProgressMailboxes(t, sessions, stall, normal, n)
	dir := t.TempDir()
	set := NewMailboxWatcherSet(MailboxProviderFunc(func() []Mailbox { return mbs }), dir, nil)
	preElapsedStagger(t, set)

	passDone := make(chan struct{})
	go func() {
		set.CycleAll(context.Background())
		close(passDone)
	}()

	// B-W1-27: the other twelve complete within the pass — observed while
	// mailbox 0's cycle is still parked at the server greeting.
	waitFor(t, 10*time.Second, "all 12 healthy mailboxes to record a successful check while mailbox 0 is stalled at the server", func() bool {
		ok := 0
		for i := 1; i < n; i++ {
			st, err := LoadWatcherState(dir, fmt.Sprintf("agent-pr-%d", i), fmt.Sprintf("ws-pr-%d", i))
			if err == nil && st.State == "ok" {
				ok++
			}
		}
		return ok == n-1
	})

	// Premise: the stalled mailbox is mid-cycle, so it has not recorded
	// anything at all — no success, and not even a failure yet.
	_, err := LoadWatcherState(dir, "agent-pr-0", "ws-pr-0")
	require.ErrorIs(t, err, ErrNoWatcherState,
		"B-W1-27: the stalled mailbox recorded nothing while its cycle is still blocked at the server")
	require.Equal(t, n-1, normal.loginCount(),
		"each healthy mailbox dialed exactly once and the stalled mailbox never logged in — CX-10: the pass did not serialize behind the stall")

	// Release the stall: the pass finishes, and the stalled mailbox's cycle
	// completes as a visible failure — it ran, it just never succeeded.
	stall.close()
	expectResult(t, passDone, 10*time.Second, "the pass to finish once the stall is released")
	st, err := LoadWatcherState(dir, "agent-pr-0", "ws-pr-0")
	require.NoError(t, err, "the stalled mailbox's cycle completed within the pass after the stall was released")
	require.NotEqual(t, "ok", st.State,
		"the released cycle must not fabricate a success for the check that was blocked")
	require.Empty(t, st.LastSuccessAt,
		"B-W1-27: the stalled mailbox never recorded a successful check")
}

// TestWatcherSet_CyclesRunBoundedParallel asserts MC-W1-21's bound from
// observation: with thirteen due mailboxes all parked at the server, several
// cycles are concurrently in flight at the network edge (a sequential loop or
// a semaphore of 1 never overlaps two dials — each blocks forever, so the
// second could never start), and the observed concurrency never exceeds the
// pool's process-wide 8-socket ceiling (B-W1-5, FR-W1-21). The scheduler's
// constant is deliberately never imported: the envelope [2, 8] is the spec's
// (§4.10.1 "a global bound consistent with the account and pool ceilings").
func TestWatcherSet_CyclesRunBoundedParallel(t *testing.T) {
	stall := newStubIMAPServer(t)
	stall.setMode(stubStallGreeting)
	sessions := newSessionsForStub(t, stall, newPoolTestClock()) // swaps imapDial first…
	probe := swapCountingDial(t)                                 // …so this probe wraps the harness's swap and sees every dial
	const n = 13
	mbs := watcherSetProgressMailboxes(t, sessions, stall, stall, n)
	set := NewMailboxWatcherSet(MailboxProviderFunc(func() []Mailbox { return mbs }), t.TempDir(), nil)
	preElapsedStagger(t, set)

	passDone := make(chan struct{})
	go func() {
		set.CycleAll(context.Background())
		close(passDone)
	}()

	waitFor(t, 10*time.Second, "at least two watcher cycles concurrently in flight at the network edge (MC-W1-21: bounded PARALLEL progress — a sequential scheduler never overlaps two dials)", func() bool {
		return probe.peak.Load() >= 2
	})

	stall.close()
	expectResult(t, passDone, 10*time.Second, "the pass to finish once the stall is released")
	require.GreaterOrEqual(t, int(probe.peak.Load()), 2,
		"MC-W1-21: parallel due-cycle progress was observed (peak concurrent dials)")
	require.LessOrEqual(t, int(probe.peak.Load()), 8,
		"B-W1-5/FR-W1-21: the watcher's pass concurrency stays inside the pool's 8-socket ceiling — observed, never the scheduler constant")
}

// TestWatcherSet_NoSecondCycleWhileInFlight asserts B-W1-28 / DS-6 row 2: a
// mailbox whose current cycle is still running at its next due moment gets no
// second cycle. The guard is observed at the server: while the first cycle is
// parked at the greeting, a second full pass returns immediately (it skips
// the claimed mailbox rather than awaiting it) and the server has accepted
// exactly one connection — a guard-less scheduler dials again.
func TestWatcherSet_NoSecondCycleWhileInFlight(t *testing.T) {
	stall := newStubIMAPServer(t)
	stall.setMode(stubStallGreeting)
	sessions := newSessionsForStub(t, stall, newPoolTestClock())
	mbs := watcherSetProgressMailboxes(t, sessions, stall, stall, 1)
	set := NewMailboxWatcherSet(MailboxProviderFunc(func() []Mailbox { return mbs }), t.TempDir(), nil)
	preElapsedStagger(t, set) // one mailbox has offset 0; kept uniform with the 13-mailbox tests

	first := make(chan struct{})
	go func() {
		set.CycleAll(context.Background())
		close(first)
	}()
	waitFor(t, 10*time.Second, "the mailbox's first cycle to be in flight at the server", func() bool {
		return stall.acceptCount() >= 1
	})

	second := make(chan struct{})
	go func() {
		set.CycleAll(context.Background())
		close(second)
	}()
	expectResult(t, second, 3*time.Second,
		"B-W1-28: the second pass to return while the first cycle is still in flight (the claimed mailbox is skipped, not awaited)")
	require.Equal(t, 1, stall.acceptCount(),
		"B-W1-28: no second cycle starts for a mailbox whose cycle is in flight — the server accepted exactly one connection")

	stall.close()
	expectResult(t, first, 10*time.Second, "the first pass to finish once the stall is released")
}
