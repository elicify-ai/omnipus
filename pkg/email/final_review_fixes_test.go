package email

// Regression tests for the final-review fix round (commit 15df2c990):
//
//  1. TestMailBudget_FlightSurvivesFirstCallerCancel — silent-failure-hunter
//     HIGH finding: the shared dial flight captured the FIRST coalescing
//     caller's request context, so that caller's cancel spuriously failed
//     every joiner with live contexts of their own. Fix: flightContext
//     (context.WithoutCancel + the ctxOrCommandDeadline fallback rule).
//     Oracle: the finding's intended behavior — a shared flight is bounded
//     by its own deadline rule (first caller's deadline when set, else
//     commandTimeout), each caller keeps an independent ErrMailBusy bail-out,
//     and joiners receive the executor's value AND error.
//
//  2. TestWatcher_RecordFailureJitterVariesAndRespectsWindow — code-reviewer
//     Important finding (MC-33 jitter unwired: recordFailure drew a hardcoded
//     literal that made the factor exactly 1.0, i.e. zero jitter). Oracle:
//     spec MC-33 (docs/internal/specs/email-mail-view-spec.md): per-mailbox
//     backoff 60 s → 2 → 4 … cap 15 min with ±20% jitter.
//
//  3. TestWatcherSet_FirstCyclesStaggerAcrossSlots — code-reviewer Important
//     finding (MC-33 first-cycle stagger unwired). Oracle: spec MC-33 —
//     "first cycles … offset so same-host mailboxes never align", as built as
//     WatcherInitialOffset(i): mailbox i's first cycle waits i × the cycle
//     interval from the set's first-CycleAll anchor (consecutive cycle slots).
//
// Runs in package email (internal): cycleStubTransport and writeWatcherState
// come from watcher_cycle_if_due_red_test.go.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMailBudget_FlightSurvivesFirstCallerCancel — two joiners coalesce into
// the FIRST caller's flight (same account+operation+params); the first
// caller's own context is cancelled partway, while the shared dial is still
// running. The flight is shared, so it must NOT be hostage to that
// cancellation: the dial completes on its detached context, the joiners
// receive the executor's value, and only the cancelled caller itself gets its
// own ErrMailBusy bail-out. The dial fn distinguishes the two worlds: it
// returns the value when released, but a cancellation-flavored error if its
// OWN context dies first — under the pre-fix code the flight rode the first
// caller's bare ctx, so the dial died with context.Canceled and every joiner
// inherited that false error.
func TestMailBudget_FlightSurvivesFirstCallerCancel(t *testing.T) {
	dir := t.TempDir()
	budget := NewMailBudget(dir)

	op := MailBudgetRequest{
		Account:     "imap.flight.test|flight@test.local",
		AgentID:     "agent-fl",
		WorkspaceID: "ws-fl",
		Operation:   "read_inbox",
		// Identical non-empty params for every caller: the coalescing key
		// (flightKey) must match, so all three share one flight.
		Params: map[string]any{"folder": "INBOX", "limit": float64(25)},
	}

	var dials atomic.Int32
	started := make(chan struct{})
	var startedOnce sync.Once
	release := make(chan struct{})
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }
	// Failure-path safety: unblocks the parked shared dial even if the test
	// fails before its release point, so the harness never deadlocks.
	t.Cleanup(doRelease)

	// The shared dial: returns the value when released; a
	// cancellation-flavored error if its own context dies first — that error
	// is the pre-fix bug's signature, and the joiners' assertion names it.
	fn := func(ctx context.Context) (any, error) {
		dials.Add(1)
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
			return "flight-value", nil
		case <-ctx.Done():
			return nil, fmt.Errorf("dial ctx died: %w", ctx.Err())
		}
	}

	// Caller A starts first and becomes the executor: <-started fires from
	// inside the running flight fn, so A's flight is registered before B and
	// C call — their joiner status is deterministic, not racy.
	ctxA, cancelA := context.WithCancel(context.Background())
	type result struct {
		val any
		err error
	}
	resA := make(chan result, 1)
	go func() {
		v, err := budget.CallValue(ctxA, op, fn)
		resA <- result{v, err}
	}()
	<-started

	// Two joiners with live contexts of their own.
	resB := make(chan result, 1)
	go func() {
		v, err := budget.CallValue(context.Background(), op, fn)
		resB <- result{v, err}
	}()
	resC := make(chan result, 1)
	go func() {
		v, err := budget.CallValue(context.Background(), op, fn)
		resC <- result{v, err}
	}()
	// Grace so B and C have joined the flight before A cancels (mirrors the
	// existing singleflight tests' grace-sleep style).
	time.Sleep(100 * time.Millisecond)

	cancelA()

	// A's OWN bail-out: only this caller's context ended, so A fails with the
	// budget's busy sentinel wrapping its own cancellation — never with the
	// flight's result (which does not exist yet; the dial is still parked).
	select {
	case r := <-resA:
		// The bail-out chains ErrMailBusy (the budget's own sentinel); the
		// caller's ctx.Err() itself is formatted into the message, not
		// error-chained (the package's long-standing %w: %v shape — see
		// runDialValue). Assert the contract that exists, not a stronger one.
		require.ErrorIs(t, r.err, ErrMailBusy, "the cancelled caller must fail with its own ErrMailBusy bail-out")
		require.Nil(t, r.val, "the cancelled caller receives no dial value — the shared dial is still running")
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled first caller never returned — its per-caller bail-out is gone")
	}

	// Release the shared dial: under the fix it is running on the detached
	// flight context, so it completes and delivers to the joiners.
	doRelease()

	for name, resCh := range map[string]<-chan result{"joiner B": resB, "joiner C": resC} {
		select {
		case r := <-resCh:
			require.NotErrorIs(t, r.err, context.Canceled,
				name+" must not receive a context-cancellation-flavored error — that is the pre-fix bug (the flight riding the first caller's ctx)")
			require.NoError(t, r.err, name+" has a live context and must not fail at all")
			require.Equal(t, "flight-value", r.val, name+" must receive the executor's value")
		case <-time.After(3 * time.Second):
			t.Fatalf("%s never received the flight result — the flight died with the first caller's cancellation", name)
		}
	}
	require.Equal(t, int32(1), dials.Load(),
		"exactly ONE shared dial must have run: this test only proves flight survival if the joiners really coalesced (dials=2 would mean they never joined)")
}

// failingCycleTransport fails every inbox read with a class-keyword-free
// error, so classifyMailError resolves it deterministically to server_error
// (no auth_failed cap override, no connect/tls/dns keywords) — the backoff
// ladder stays purely attempt-driven for the jitter assertions.
type failingCycleTransport struct {
	readInbox atomic.Int32
}

func (s *failingCycleTransport) ReadInbox(ctx context.Context, opts InboxOptions) ([]Message, error) {
	s.readInbox.Add(1)
	return nil, errors.New("boom: storage unavailable")
}
func (s *failingCycleTransport) Search(ctx context.Context, query string, opts SearchOptions) (SearchResult, error) {
	return SearchResult{}, nil
}
func (s *failingCycleTransport) ReadMessage(ctx context.Context, uid uint32) (*Message, error) {
	return nil, errWatcherStubReadMessage
}
func (s *failingCycleTransport) Send(ctx context.Context, req SendRequest) error { return nil }
func (s *failingCycleTransport) MarkSeen(ctx context.Context, uid uint32) error  { return nil }

// TestWatcher_RecordFailureJitterVariesAndRespectsWindow — 20 failed cycles
// with a fixed clock: every recorded interval must sit inside MC-33's ±20%
// window around the plain exponential ladder (60 s → 2 → 4 … cap 15 min), and
// the derived jitter units must not all be identical. Under the pre-fix code
// recordFailure drew a hardcoded literal (factor exactly 1.0), so every
// interval equals its base exactly and the non-uniformity assertion fails.
func TestWatcher_RecordFailureJitterVariesAndRespectsWindow(t *testing.T) {
	dir := t.TempDir()
	const agentID, wsID = "agent-jit", "ws-jit"
	t0 := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	tr := &failingCycleTransport{}
	w, err := NewWatcher(WatcherConfig{
		AgentID: agentID, WorkspaceID: wsID,
		Transport: tr, StateDir: dir,
		Now: func() time.Time { return t0 }, // fixed clock: interval = NextAttemptAt − t0 exactly
	})
	require.NoError(t, err)

	const cycles = 20
	units := make([]float64, 0, cycles)
	for i := 1; i <= cycles; i++ {
		require.Error(t, w.Cycle(context.Background()), "cycle %d must fail (failing transport)", i)

		st, lerr := LoadWatcherState(dir, agentID, wsID)
		require.NoError(t, lerr)
		require.Equal(t, i, st.Attempt, "failure %d must be the attempt-th failure (ladder position drives the base)")
		require.Equal(t, "server_error", st.LastErrorClass,
			"the class-keyword-free error must classify as server_error — anything else invalidates the base normalization below")

		next, perr := time.Parse(time.RFC3339, st.NextAttemptAt)
		require.NoError(t, perr)
		interval := next.Sub(t0)

		// MC-33 ladder base for attempt i: 60 s → 2 → 4 … cap 15 min, before
		// jitter — derived from the spec, not read off the implementation.
		base := min(watcherBackoffBase<<uint(i-1), watcherBackoffCap)

		// ±20% window (MC-33), plus 1 s of slack for the RFC3339 second-
		// granularity the state file stores NextAttemptAt with.
		minI := base*4/5 - time.Second
		maxI := base*6/5 + time.Second
		require.GreaterOrEqual(t, interval, minI,
			"attempt %d: interval %v is below MC-33's −20%% floor (%v)", i, interval, minI)
		require.LessOrEqual(t, interval, maxI,
			"attempt %d: interval %v is above MC-33's +20%% ceiling (%v)", i, interval, maxI)

		// Derived jitter unit: interval = base × (0.8 + 0.4u) → u. The
		// distinctness check below compares these; RFC3339 rounding makes u
		// coarse (±1 s / base / 0.4), which only helps: identical draws still
		// land on identical u.
		units = append(units, (float64(interval)/float64(base)-0.8)/0.4)
	}
	require.Equal(t, int32(cycles), tr.readInbox.Load(), "every failed cycle dialed exactly once")

	seen := map[float64]bool{}
	for _, u := range units {
		seen[u] = true
	}
	require.Greater(t, len(seen), 1,
		"all %d failures produced the IDENTICAL jitter unit %v — MC-33's jitter is not wired (a constant draw instead of rand.Float64())", cycles, units[0])
}

// TestWatcherSet_FirstCyclesStaggerAcrossSlots — three mailboxes in one set:
// the first CycleAll anchors the stagger clock, and mailbox i's FIRST cycle
// waits WatcherInitialOffset(i) = i × the cycle interval. So the first pass
// dials ONLY mailbox 0, the anchor does not reset on a second immediate pass,
// mailbox 1 releases exactly when its offset boundary is reached (inclusive:
// start+offset is not After now), and mailbox 2 releases one interval later.
// Time travel: the set has no injected clock, so the test shifts the
// unexported anchor backward (same-package white-box) instead of sleeping —
// the production stagger logic itself runs unmodified.
func TestWatcherSet_FirstCyclesStaggerAcrossSlots(t *testing.T) {
	dir := t.TempDir()
	const n = 3
	stubs := make([]*cycleStubTransport, n)
	mbs := make([]Mailbox, n)
	for i := range mbs {
		stubs[i] = &cycleStubTransport{}
		mbs[i] = Mailbox{
			AgentID:     fmt.Sprintf("agent-stg-%d", i),
			WorkspaceID: fmt.Sprintf("ws-stg-%d", i),
			Transport:   stubs[i],
		}
	}
	set := NewMailboxWatcherSet(MailboxProviderFunc(func() []Mailbox { return mbs }), dir, nil)
	ctx := context.Background()

	// Pass 1 (anchors the clock): only mailbox 0 (offset 0) dials; mailboxes
	// 1 and 2 are deferred to their stagger slots — the pre-fix lockstep code
	// would have dialed all three here.
	set.CycleAll(ctx)
	require.Equal(t, int32(1), stubs[0].readInbox.Load(), "mailbox 0 has offset 0 and dials on the first pass")
	require.Zero(t, stubs[1].readInbox.Load(), "mailbox 1's first cycle must wait its 60 s stagger slot")
	require.Zero(t, stubs[2].readInbox.Load(), "mailbox 2's first cycle must wait its 120 s stagger slot")
	_, err := LoadWatcherState(dir, "agent-stg-1", "ws-stg-1")
	require.ErrorIs(t, err, ErrNoWatcherState, "a deferred mailbox never ran, so it must have no state file")

	// Pass 2, immediately: the anchor is set once, not per call — the deferred
	// mailboxes stay deferred, mailbox 0 cycles on its normal cadence.
	set.CycleAll(ctx)
	require.Equal(t, int32(2), stubs[0].readInbox.Load())
	require.Zero(t, stubs[1].readInbox.Load(), "the anchor must not reset per CycleAll call")
	require.Zero(t, stubs[2].readInbox.Load())

	// Time-travel exactly one interval: mailbox 1's offset boundary is
	// reached (start+offset NOT After now → due — the boundary is inclusive)
	// and mailbox 2 (two intervals out) stays deferred.
	shiftStaggerAnchor(t, set, -watcherCycleInterval)
	set.CycleAll(ctx)
	require.Equal(t, int32(3), stubs[0].readInbox.Load())
	require.Equal(t, int32(1), stubs[1].readInbox.Load(), "mailbox 1 releases at exactly its offset boundary")
	require.Zero(t, stubs[2].readInbox.Load(), "mailbox 2's 120 s slot has not arrived yet")

	// One more interval: every offset has elapsed — offsets are a no-op once
	// elapsed, and all mailboxes cycle on the normal cadence.
	shiftStaggerAnchor(t, set, -watcherCycleInterval)
	set.CycleAll(ctx)
	require.Equal(t, int32(4), stubs[0].readInbox.Load())
	require.Equal(t, int32(2), stubs[1].readInbox.Load())
	require.Equal(t, int32(1), stubs[2].readInbox.Load(), "mailbox 2 releases at exactly its own offset boundary")
}

// shiftStaggerAnchor moves the set's stagger anchor by d (negative = back in
// time) — the time-travel mechanism for the stagger test, because
// MailboxWatcherSet has no injected clock. White-box by necessity (unexported
// field); requires the anchor to already be set by a first CycleAll.
func shiftStaggerAnchor(t *testing.T, s *MailboxWatcherSet, d time.Duration) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.False(t, s.start.IsZero(), "the stagger anchor must be set by the first CycleAll before it can be shifted")
	s.start = s.start.Add(d)
}
