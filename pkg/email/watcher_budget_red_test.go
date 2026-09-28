package email

// RED round 3 — CHECK findings F3 (zero Watcher×Budget integration coverage)
// and F4's Cycle skip branch. Two tests:
//
//   - TestWatcher_BudgetSkipIsNotFailure pins Cycle's skip branch semantics:
//     a budget skip (ErrMailSkipped surfacing from the dial — the exact shape
//     a TryCall refusal wraps) is NOT a cycle failure: Cycle returns nil and
//     the watcher's OWN bookkeeping is untouched. This is the mutant-killer
//     for "someone starts calling recordFailure on a skip".
//
//   - TestWatcher_CycleGoesThroughBudget drives the gate end-to-end: a real
//     MailBudget wired into a real Watcher, both slots held by other callers,
//     the cycle's dial must be refused. Oracle: spec MC-33/MAJ-018 + the A8
//     row ("watcher cycles" share the 2-per-mailbox gate; TryCall is "the
//     watcher's non-blocking entry", mail_budget.go).
//
// Runs in package email (internal): writeWatcherState and cycleStubTransport
// come from watcher_cycle_if_due_red_test.go.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// budgetSkipStubTransport surfaces the TryCall skip shape from the dial edge:
// the wrapped ErrMailSkipped sentinel is exactly what a budget-gated dial
// returns when the watcher's cycle is skipped (mail_budget.go::TryCall wraps
// the sentinel into its refusals). It does NOT implement MailboxStatuser, so
// probe() takes the ReadInbox fallback and the counter is the dial evidence.
type budgetSkipStubTransport struct {
	readInbox atomic.Int32
}

func (s *budgetSkipStubTransport) ReadInbox(ctx context.Context, opts InboxOptions) ([]Message, error) {
	s.readInbox.Add(1)
	return nil, fmt.Errorf("mail budget: watcher_cycle skipped, no free slot: %w", ErrMailSkipped)
}
func (s *budgetSkipStubTransport) Search(ctx context.Context, query string, opts SearchOptions) (SearchResult, error) {
	return SearchResult{}, nil
}
func (s *budgetSkipStubTransport) ReadMessage(ctx context.Context, uid uint32) (*Message, error) {
	return nil, errWatcherStubReadMessage
}
func (s *budgetSkipStubTransport) Send(ctx context.Context, req SendRequest) error { return nil }
func (s *budgetSkipStubTransport) MarkSeen(ctx context.Context, uid uint32) error  { return nil }

func TestWatcher_BudgetSkipIsNotFailure(t *testing.T) {
	dir := t.TempDir()
	const agentID, wsID = "agent-skip", "ws-skip"

	// The watcher must carry a NON-NIL budget for the skip branch to apply
	// (Cycle checks w.cfg.Budget != nil) — production watchers always do
	// (watcher_set.go wires SharedMailBudget into every WatcherConfig).
	w, err := NewWatcher(WatcherConfig{
		AgentID: agentID, WorkspaceID: wsID,
		Transport: &budgetSkipStubTransport{},
		StateDir:  dir,
		Budget:    NewMailBudget(t.TempDir()),
	})
	require.NoError(t, err)

	// Pre-seed a SUCCESS state: the skip must leave it untouched — a skip
	// must look identical to "did not run yet", never to "ran and failed".
	writeWatcherState(t, dir, WatcherState{
		AgentID: agentID, WorkspaceID: wsID,
		State:         "ok",
		LastSuccessAt: "2026-09-27T00:00:00Z",
	})
	before, err := LoadWatcherState(dir, agentID, wsID)
	require.NoError(t, err)

	require.NoError(t, w.Cycle(context.Background()),
		"a budget skip is not a cycle failure — Cycle returns nil")

	st, err := LoadWatcherState(dir, agentID, wsID)
	require.NoError(t, err)
	require.Equal(t, "ok", st.State, "a skip must not re-write the state as an error")
	require.Zero(t, st.Attempt, "a budget skip must not advance the failure counter")
	require.Empty(t, st.NextAttemptAt, "a budget skip must not schedule a backoff attempt")
	require.Empty(t, st.LastErrorClass, "a skip carries no error class")
	require.Empty(t, st.LastErrorText, "a skip carries no error text")
	require.Equal(t, before.LastSuccessAt, st.LastSuccessAt,
		"the skip must not claim a success either — nothing was recorded")
}

// TestWatcher_CycleGoesThroughBudget — A8: the watcher's cycle dial takes its
// slot from the SAME shared 2-per-account gate as the panel and the agent
// tools (spec MC-33/MAJ-018, A8 row: "watcher cycles" in the gate; TryCall is
// "the watcher's non-blocking entry"). The budget IS wired into every watcher
// (watcher_set.go::NewMailboxWatcherSet passes SharedMailBudget through).
//
// RED TODAY (round-3 finding, deliberate): MailBudget.TryCall has NO
// production caller — the watcher's probe (Client.ReadInbox /
// Client.MailboxStatus) dials right past the wired budget, and Cycle's
// ErrMailSkipped branch is unreachable in the production wiring. This test
// fails at the probe-count assertion naming the gap; it goes green when
// backend-lead wires the watcher dial through TryCall. The oracle is the
// spec (A8/MC-33/MAJ-018), not the current implementation — never adapt it.
func TestWatcher_CycleGoesThroughBudget(t *testing.T) {
	dir := t.TempDir()
	const agentID, wsID = "agent-gate", "ws-gate"

	budget := NewMailBudget(dir)
	stub := &cycleStubTransport{}
	w, err := NewWatcher(WatcherConfig{
		AgentID: agentID, WorkspaceID: wsID,
		Transport: stub,
		StateDir:  dir,
		Budget:    budget,
	})
	require.NoError(t, err)

	// Baseline cycle with a free budget: the dial goes through, success is
	// recorded. (Under the missing wiring this also passes — the dial runs.)
	require.NoError(t, w.Cycle(context.Background()))
	require.Equal(t, int32(1), stub.readInbox.Load(), "the ungated baseline tick dials")
	st, err := LoadWatcherState(dir, agentID, wsID)
	require.NoError(t, err)
	require.Equal(t, "ok", st.State, "baseline success recorded")

	// Hold BOTH slots through the public API. Paramless ops (Params nil)
	// skip singleflight and go straight to the semaphore. The account key is
	// the one this watcher's gated dial resolves under (accountKeyOf: a stub
	// that is not an AccountKeyer falls back to the pair key) — the only
	// derivation a TryCall caller has for a stub transport.
	acct := accountKeyOf(stub, agentID, wsID)
	gate := make(chan struct{})
	inFlight := &atomic.Int32{}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = budget.Call(context.Background(), MailBudgetRequest{
				Account: acct, AgentID: agentID, WorkspaceID: wsID,
				Operation: "listMailFolders",
			}, func(context.Context) error {
				inFlight.Add(1)
				defer inFlight.Add(-1)
				<-gate
				return nil
			})
		}()
	}
	require.Eventually(t, func() bool { return inFlight.Load() == 2 }, 2*time.Second, 5*time.Millisecond,
		"two holders must occupy both slots before the gated tick")

	// The gated tick: both slots held → the cycle must be SKIPPED — the dial
	// never happens and nothing is recorded (a skip is not a failure).
	err = w.Cycle(context.Background())
	close(gate)
	wg.Wait()

	require.NoError(t, err, "a budget skip is not a cycle failure")
	require.Equal(t, int32(1), stub.readInbox.Load(),
		"the gated tick must NOT have dialed: the watcher cycle shares the 2-per-account budget (A8, MC-33/MAJ-018). "+
			"This fails while the watcher dial is not gated through MailBudget.TryCall — TryCall currently has no production caller")

	st, err = LoadWatcherState(dir, agentID, wsID)
	require.NoError(t, err)
	require.Equal(t, "ok", st.State, "the skipped tick recorded nothing")
	require.Zero(t, st.Attempt, "the skipped tick advanced no failure counter")
	require.Empty(t, st.NextAttemptAt, "the skipped tick scheduled no backoff")
}
