package email

// RED round 2 — the A8 mail-operation budget unit (architect design note,
// team-lead brief): ONE per-account shared gate in pkg/email for BOTH the
// REST panel path and the agent-tool path, keyed by account = IMAP host +
// username (not the agent/workspace pair). Three layers: backoff-no-dial
// (reads the SAME persisted watcher state both paths already read via
// LoadWatcherState/keyFor), then singleflight coalescing keyed by (account,
// operation, normalized params), then a 2-per-account semaphore — overflow
// queues under the caller's context deadline, then fails.
//
// retry=true bypasses the backoff CHECK only, never the semaphore or
// singleflight; the tool path can never set it (structural guard in
// pkg/tools). These tests run in package email (internal): writeWatcherState
// and cycleStubTransport come from watcher_cycle_if_due_red_test.go.
//
// INTENDED SHAPE this file pins (backend-lead lands it in pkg/email; the
// compile-red naming the missing symbols IS the RED evidence until then):
//
//	type MailBudgetRequest struct {
//		Account     string         // "host|username" per-account key
//		AgentID     string         // selects the watcher state file
//		WorkspaceID string
//		Operation   string         // e.g. listMailFolders / read_inbox
//		Params      map[string]any // normalized; part of the singleflight key
//		Retry       bool           // REST-only human bypass (backoff check only)
//	}
//	func NewMailBudget(stateDir string) *MailBudget
//	func (b *MailBudget) Call(ctx, op, fn func(context.Context) error) error
//	func (b *MailBudget) TryCall(ctx, op, fn func(context.Context) error) error
//
// with *MailBackoffError{LastErrorClass, NextAttemptAt} as the typed backoff
// refusal (carries the 503 body's fields), ErrMailBusy for semaphore-deadline
// overflow, and ErrMailSkipped as the watcher's countable non-blocking skip.

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const mbAccount = "imap.test.local|mailbox@test.local"

// mbOp builds a budget op for the test pair (agent-bf/ws-bf — the same pair
// writeWatcherState writes, watcher_cycle_if_due_red_test.go).
func mbOp(retry bool, op string, params map[string]any) MailBudgetRequest {
	return MailBudgetRequest{
		Account:     mbAccount,
		AgentID:     "agent-bf",
		WorkspaceID: "ws-bf",
		Operation:   op,
		Params:      params,
		Retry:       retry,
	}
}

// mbDeadPort returns a loopback port with nothing listening on it.
func mbDeadPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port
}

// TestMailBudget_BackoffRefusalSymmetric — during backoff, an automatic
// REST-shaped call (retry=false) and a TOOL-shaped call (retry=false — the
// tool path can never set retry; the pkg/tools schema guard pins that
// structurally) are refused identically, while retry=true on the SAME
// backing-off account dials. This is the proof the gate is SHARED, not a
// REST-only gate.
func TestMailBudget_BackoffRefusalSymmetric(t *testing.T) {
	dir := t.TempDir()
	nextAt := time.Now().Add(10 * time.Minute).UTC().Round(time.Second)
	writeWatcherState(t, dir, WatcherState{
		AgentID:        "agent-bf",
		WorkspaceID:    "ws-bf",
		State:          "error",
		LastErrorClass: "auth_failed",
		NextAttemptAt:  nextAt.Format(time.RFC3339),
		Attempt:        1,
	})
	budget := NewMailBudget(dir)
	dials := &atomic.Int32{}
	fn := func(context.Context) error { dials.Add(1); return nil }

	t.Run("automatic REST shape refused with typed error", func(t *testing.T) {
		err := budget.Call(context.Background(), mbOp(false, "listMailFolders", nil), fn)
		var bf *MailBackoffError
		require.ErrorAs(t, err, &bf)
		require.Equal(t, "auth_failed", bf.LastErrorClass)
		require.Equal(t, nextAt.Format(time.RFC3339), bf.NextAttemptAt)
	})
	t.Run("tool path refused identically", func(t *testing.T) {
		err := budget.Call(context.Background(), mbOp(false, "read_inbox", map[string]any{"limit": float64(20)}), fn)
		var bf *MailBackoffError
		require.ErrorAs(t, err, &bf)
		require.Equal(t, "auth_failed", bf.LastErrorClass)
		require.Equal(t, nextAt.Format(time.RFC3339), bf.NextAttemptAt)
	})
	t.Run("retry=true bypasses the backoff check and dials", func(t *testing.T) {
		dials.Store(0)
		require.NoError(t, budget.Call(context.Background(), mbOp(true, "listMailFolders", nil), fn))
		require.Equal(t, int32(1), dials.Load(), "retry=true must run the dial")
	})
	t.Run("no call reached the transport while backing off", func(t *testing.T) {
		require.Equal(t, int32(1), dials.Load(),
			"the two refused calls must never have run fn")
	})
}

// TestMailBudget_SemaphoreCapTwoHoldsUnderRetry — retry=true bypasses ONLY
// the backoff check. Three concurrent retry=true calls on one account: at
// most 2 ever run fn at once; the third queues and only enters after a slot
// frees; with an expired context it fails with ErrMailBusy instead of
// dialing. (The REST surface re-proves the same gate — the handler wraps
// every dial in the budget — but the cap itself lives here.)
func TestMailBudget_SemaphoreCapTwoHoldsUnderRetry(t *testing.T) {
	dir := t.TempDir()
	budget := NewMailBudget(dir)
	gate := make(chan struct{})
	inFlight := &atomic.Int32{}
	maxSeen := &atomic.Int32{}
	fn := func(context.Context) error {
		n := inFlight.Add(1)
		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		defer inFlight.Add(-1)
		<-gate
		return nil
	}
	ops := []MailBudgetRequest{
		mbOp(true, "listMailFolders", nil),
		mbOp(true, "listMailMessages", map[string]any{"folder": "inbox"}),
		mbOp(true, "getMailMessage", map[string]any{"ref": "uid:1:1"}),
	}
	errs := make([]error, 3)
	var wg sync.WaitGroup
	for i, op := range ops {
		wg.Add(1)
		go func(i int, op MailBudgetRequest) {
			defer wg.Done()
			errs[i] = budget.Call(context.Background(), op, fn)
		}(i, op)
	}

	t.Run("third concurrent retry=true call queues", func(t *testing.T) {
		require.Eventually(t, func() bool { return inFlight.Load() == 2 },
			2*time.Second, 5*time.Millisecond, "two slots taken")
		time.Sleep(50 * time.Millisecond) // grace: a cap>2 would have let fn 3 in
		require.Equal(t, int32(2), maxSeen.Load(), "never more than 2 concurrent dials")
		// If the third call had entered fn, maxSeen would now read 3 - the maxSeen assertion above IS the queue proof.
		close(gate)
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		require.NoError(t, errs[2], "the queued call proceeds once a slot frees")
	})
}

// TestMailBudget_SingleflightOneDial — two concurrent IDENTICAL requests for
// the same (account, operation, params): exactly ONE fn run, and both callers
// receive the same outcome. Cross-path (REST + tool) coalescing is this same
// property at the shared unit: both paths enter through budget.Call with the
// same account and operation, differing only in caller. The test pins the
// property at the layer that owns it. (Deliberate same-unit shape: a true
// REST+tool fan-in needs the production wiring both paths use; see the
// report's wiring note.)
func TestMailBudget_SingleflightOneDial(t *testing.T) {
	dir := t.TempDir()
	budget := NewMailBudget(dir)
	dials := &atomic.Int32{}
	entered := make(chan struct{})
	release := make(chan struct{})
	errShared := errors.New("singleflight shared outcome")
	fnRan := false
	var fnMu sync.Mutex
	fn := func(context.Context) error {
		fnMu.Lock()
		fnRan = true
		fnMu.Unlock()
		dials.Add(1)
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return errShared
	}
	op := mbOp(false, "listMailMessages", map[string]any{"folder": "inbox", "limit": float64(25)})

	err1 := make(chan error, 1)
	go func() { err1 <- budget.Call(context.Background(), op, fn) }()
	<-entered

	err2 := make(chan error, 1)
	go func() { err2 <- budget.Call(context.Background(), op, fn) }()
	time.Sleep(100 * time.Millisecond) // grace: a missing singleflight would have run fn twice

	require.Equal(t, int32(1), dials.Load(), "identical concurrent requests share ONE dial")
	close(release)
	require.ErrorIs(t, <-err1, errShared)
	require.ErrorIs(t, <-err2, errShared, "both callers must inherit the same outcome")
	require.True(t, func() bool { fnMu.Lock(); defer fnMu.Unlock(); return fnRan }())
}

// TestMailBudget_SingleflightKeyIncludesParams — round-3 CHECK finding F2:
// the coalescing key carries the NORMALIZED PARAMS (mail_budget.go header:
// "singleflight coalescing keyed by (account, operation, normalized
// params)"). Two concurrent CallValue requests for the SAME account and
// operation but DIFFERENT params must NOT coalesce: both fns run, and each
// caller receives its OWN dial's value — a params-blind key would put two
// different reads into one flight and serve one caller the other's data.
//
// Mutant this kills: flightKey dropping Params (key = account+operation) —
// then the second request joins the first's flight, never runs its own fn,
// and dies on the caller's context deadline (ErrMailBusy here).
func TestMailBudget_SingleflightKeyIncludesParams(t *testing.T) {
	dir := t.TempDir()
	budget := NewMailBudget(dir)

	var dials atomic.Int32
	started := make(chan struct{})       // closed once by the first dial's fn
	secondEntered := make(chan struct{}) // closed by the second dial's fn
	var startedOnce sync.Once
	// Test-end safety: unblocks the parked first dial even on the failure
	// path, so a wrongly-coalescing key cannot deadlock the harness.
	unblockFirst := make(chan struct{})
	var unblockOnce sync.Once
	t.Cleanup(func() { unblockOnce.Do(func() { close(unblockFirst) }) })

	firstFn := func(context.Context) (any, error) {
		dials.Add(1)
		startedOnce.Do(func() { close(started) })
		// The first dial parks until the second request has proven it runs
		// its OWN dial — or the test ends.
		select {
		case <-secondEntered:
		case <-unblockFirst:
		}
		return "first-dial", nil
	}
	secondFn := func(context.Context) (any, error) {
		dials.Add(1)
		close(secondEntered)
		return "second-dial", nil
	}

	valCh := make(chan any, 1)
	errCh := make(chan error, 1)
	go func() {
		v, err := budget.CallValue(context.Background(),
			mbOp(false, "read_inbox", map[string]any{"folder": "INBOX", "limit": float64(25)}), firstFn)
		valCh <- v
		errCh <- err
	}()
	<-started

	ctxSecond, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	valSecond, errSecond := budget.CallValue(ctxSecond,
		mbOp(false, "read_inbox", map[string]any{"folder": "ARCHIVE"}), secondFn)

	require.NoError(t, errSecond,
		"a different-params request must dial on its own, never join the first flight — "+
			"ErrMailBusy here means the two requests wrongly coalesced (params dropped from the key)")
	require.Equal(t, "second-dial", valSecond, "each caller must receive its OWN dial's value")
	require.Equal(t, int32(2), dials.Load(),
		"same account+operation, different params: two dials, never one shared flight")

	require.NoError(t, <-errCh)
	require.Equal(t, "first-dial", <-valCh)
}

// TestMailBudget_WatcherTryAcquireSkipsNonBlocking — the watcher's cycle
// takes its slot from the SAME per-account budget but NON-BLOCKINGLY: with
// both slots held by human/tool callers it returns ErrMailSkipped immediately
// (proven by it returning while both slots are STILL held — queuing would
// not have returned), and it skips during backoff too. The skip is
// countable: ErrMailSkipped is a distinct sentinel — neither nil, nor
// ErrMailBusy, nor the backoff refusal.
func TestMailBudget_WatcherTryAcquireSkipsNonBlocking(t *testing.T) {
	dir := t.TempDir()
	budget := NewMailBudget(dir)
	gate := make(chan struct{})
	inFlight := &atomic.Int32{}
	holder := func(context.Context) error {
		inFlight.Add(1)
		defer inFlight.Add(-1)
		<-gate
		return nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = budget.Call(context.Background(), mbOp(true, "listMailFolders", nil), holder)
		}()
	}
	require.Eventually(t, func() bool { return inFlight.Load() == 2 }, 2*time.Second, 5*time.Millisecond)

	t.Run("slots held: skip, never queue", func(t *testing.T) {
		watcherDials := &atomic.Int32{}
		err := budget.TryCall(context.Background(), mbOp(false, "watcher_cycle", nil), func(context.Context) error {
			watcherDials.Add(1)
			return nil
		})
		require.ErrorIs(t, err, ErrMailSkipped)
		require.NotErrorIs(t, err, ErrMailBusy)
		require.Zero(t, watcherDials.Load(), "the watcher cycle never dialed")
		require.Equal(t, int32(2), inFlight.Load(),
			"TryCall returned while both slots are still held - non-blocking, never queued")
	})
	t.Run("backoff: skip too", func(t *testing.T) {
		nextAt := time.Now().Add(10 * time.Minute).UTC().Round(time.Second)
		writeWatcherState(t, dir, WatcherState{
			AgentID: "agent-bf", WorkspaceID: "ws-bf", State: "error",
			LastErrorClass: "timeout", NextAttemptAt: nextAt.Format(time.RFC3339), Attempt: 2,
		})
		err := budget.TryCall(context.Background(), mbOp(false, "watcher_cycle", nil), func(context.Context) error {
			return nil
		})
		require.ErrorIs(t, err, ErrMailSkipped)
		close(gate)
		wg.Wait()
	})
}

// TestMailBudget_WatcherBackoffAndCallersAgree — the founder's agreement
// requirement: the watcher's OWN backoff bookkeeping and the budget's backoff
// check read and agree on the SAME persisted state. A real Watcher is driven
// into backoff by a failed cycle (dial to a dead port), then the budget's
// refusal for the same pair must carry exactly the class and next_attempt_at
// the watcher persisted — and retry=true must still dial.
func TestMailBudget_WatcherBackoffAndCallersAgree(t *testing.T) {
	dir := t.TempDir()
	port := mbDeadPort(t)
	cl, err := NewClient(Account{
		IMAPHost: "127.0.0.1",
		IMAPPort: port,
		SMTPHost: "127.0.0.1",
		Username: "mailbox@test.local",
		Password: "s3cret",
	})
	require.NoError(t, err)
	w, err := NewWatcher(WatcherConfig{
		AgentID: "agent-bf", WorkspaceID: "ws-bf",
		Transport: cl, StateDir: dir,
	})
	require.NoError(t, err)
	require.Error(t, w.Cycle(context.Background()), "the dead-port cycle must fail and record backoff")

	st, lerr := LoadWatcherState(dir, "agent-bf", "ws-bf")
	require.NoError(t, lerr)
	require.Equal(t, "error", st.State)
	require.NotEmpty(t, st.NextAttemptAt)

	budget := NewMailBudget(dir)
	t.Run("budget refusal echoes the watcher's persisted state", func(t *testing.T) {
		err := budget.Call(context.Background(), mbOp(false, "listMailFolders", nil), func(context.Context) error {
			return nil
		})
		var bf *MailBackoffError
		require.ErrorAs(t, err, &bf)
		require.Equal(t, st.LastErrorClass, bf.LastErrorClass)
		require.Equal(t, st.NextAttemptAt, bf.NextAttemptAt)
	})
	t.Run("retry=true still dials after a real recorded backoff", func(t *testing.T) {
		require.NoError(t, budget.Call(context.Background(), mbOp(true, "listMailFolders", nil), func(context.Context) error {
			return nil
		}))
	})
	t.Run("ErrMailBusy is the semaphore-deadline refusal", func(t *testing.T) {
		healthy := t.TempDir()
		b2 := NewMailBudget(healthy)
		gate := make(chan struct{})
		var wg sync.WaitGroup
		inFlight := &atomic.Int32{}
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = b2.Call(context.Background(), mbOp(false, "getMailAttachment", nil), func(context.Context) error {
					inFlight.Add(1)
					defer inFlight.Add(-1)
					<-gate
					return nil
				})
			}()
		}
		require.Eventually(t, func() bool { return inFlight.Load() == 2 }, 2*time.Second, 5*time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		err := b2.Call(ctx, mbOp(false, "getMailAttachment", nil), func(context.Context) error {
			return nil
		})
		close(gate)
		wg.Wait()
		require.ErrorIs(t, err, ErrMailBusy, "queued past the context deadline must fail busy, never dial")
	})
}
