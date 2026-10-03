package email

// W1 §4.3.1/4.10; US-8/AS-3/4, B-W1-29/30, MC-W1-22/23.
// Real watcher + client + pool; a controllable clock makes each cycle distinct.
// This lane asserts pool/skip/last-checked behavior only. Dirty-mark consumers,
// W2 cache files, scheduler fairness and gateway presence are other lanes' work.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newWatcherOverPool(t *testing.T, stub *stubIMAPServer, sessions *MailSessions, clock *poolTestClock) (*Watcher, string) {
	t.Helper()
	client := poolFacadeForStub(t, stub, sessions)
	dir := t.TempDir()
	watcher, err := NewWatcher(WatcherConfig{AgentID: "agent-w", WorkspaceID: "ws-w", Transport: client,
		StateDir: dir, Budget: NewMailBudget(dir), Now: clock.Now})
	require.NoError(t, err)
	return watcher, dir
}

func watcherStateForPoolTest(t *testing.T, dir string) *WatcherState {
	t.Helper()
	state, err := LoadWatcherState(dir, "agent-w", "ws-w")
	require.NoError(t, err)
	return state
}

// Pool TryAcquire has its own typed skip. Its short caller deadline is a
// negative control: a mistakenly BLOCKING acquisition returns timeout/busy,
// not the required immediate skip, while all eight holders stay active.
func TestPool_WatcherTryAcquireSkipsNonBlocking(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	probe := swapCountingDial(t)
	for i := 0; i < 8; i++ {
		poolHold(t, sessions, sessionIdentity{fmt.Sprintf("fg-%d/ws-%d", i, i), stub.addr(), "gen-1"}, "INBOX", false)
	}
	request := sessionIdentity{"agent-w/ws-w", stub.addr(), "gen-1"}.leaseRequest("", false, false)
	out := make(chan poolAcquireResult, 1)
	go func() {
		lease, err := sessions.TryAcquire(testCtx(t, 100*time.Millisecond), request)
		out <- poolAcquireResult{lease, err}
	}()
	got := expectResult(t, out, time.Second, "non-blocking watcher pool acquisition")
	if got.err == nil {
		got.lease.Release()
	}
	require.ErrorIs(t, got.err, ErrPoolSkipped, "§4.10.2: pool-starved watcher is a skip, never a queued failure")
	require.NotErrorIs(t, got.err, context.DeadlineExceeded)
	require.NotErrorIs(t, got.err, ErrPoolBusy)
	require.Equal(t, int32(8), probe.starts.Load(), "skip attempts no dial")
	require.Equal(t, 8, stub.openCount(), "skip cannot displace active foreground work")
}

func TestWatcher_PoolFullSkipsWithoutTouchingState(t *testing.T) {
	stub, clock := newStubIMAPServer(t), newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)
	watcher, dir := newWatcherOverPool(t, stub, sessions, clock)
	require.NoError(t, watcher.cycleIfDue(testCtx(t, 3*time.Second), clock.Now()))
	poolAwaitCount(t, stub.openCount, 0, "completed closed-panel control cycle released its socket")
	require.Equal(t, 1, stub.acceptCount(), "instrument: real initial cycle performed one network probe")
	beforeState := *watcherStateForPoolTest(t, dir)
	require.Equal(t, clock.Now().Format(time.RFC3339), beforeState.LastSuccessAt)
	require.Equal(t, 1, beforeState.UnseenTotal) // scripted STATUS seed
	require.Equal(t, uint32(4242), beforeState.UIDValidity)
	path := filepath.Join(dir, "email-watch", keyFor("agent-w", "ws-w")+".json")
	beforeBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	var holders []Lease
	for i := 0; i < 8; i++ {
		holders = append(holders, poolHold(t, sessions,
			sessionIdentity{fmt.Sprintf("fg-%d/ws-%d", i, i), stub.addr(), "gen-1"}, "INBOX", false))
	}
	poolAwaitCount(t, stub.openCount, 8, "foreground holds every global socket")
	accepts := stub.acceptCount()
	clock.advance(time.Minute) // it is a genuinely different, due check
	err = watcher.cycleIfDue(testCtx(t, 100*time.Millisecond), clock.Now())
	if err != nil {
		require.True(t, errors.Is(err, ErrMailSkipped) || errors.Is(err, ErrPoolSkipped),
			"expected nil/typed skip from Cycle, actual: %v", err)
	}
	require.NotErrorIs(t, err, ErrPoolBusy)
	require.NotErrorIs(t, err, ErrMailBusy)
	require.Equal(t, accepts, stub.acceptCount(), "starved due cycle never dialed")
	require.Equal(t, beforeState, *watcherStateForPoolTest(t, dir), "skip preserves last-success, attempt and backoff state")
	afterBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, beforeBytes, afterBytes, "not even a false fresh-check timestamp is persisted")
	for _, holder := range holders {
		holder.Release()
	}
	poolAwaitCount(t, stub.openCount, 0, "capacity genuinely free again")
	require.NoError(t, watcher.cycleIfDue(testCtx(t, 3*time.Second), clock.Now()))
	recovered := watcherStateForPoolTest(t, dir)
	require.Equal(t, clock.Now().Format(time.RFC3339), recovered.LastSuccessAt, "next due cycle really checked")
	require.Equal(t, accepts+1, stub.acceptCount(), "recovery performs exactly one actual probe")
	require.Zero(t, recovered.Attempt)
	require.Empty(t, recovered.NextAttemptAt)
	poolAwaitCount(t, stub.openCount, 0, "recovered watcher does not retain")
}

// MC-W1-23's three intervals; another workspace's panel confers no retention
// authority. STATUS uses a no-folder lease, so it never needs SELECT/EXAMINE.
func TestWatcher_CycleNeverRetainsSocket(t *testing.T) {
	for _, mode := range []string{"unwired", "other_workspace_panel_open"} {
		t.Run(mode, func(t *testing.T) {
			stub, clock := newStubIMAPServer(t), newPoolTestClock()
			sessions := newSessionsForStub(t, stub, clock)
			if mode == "unwired" {
				require.False(t, sessions.RetentionEnabled())
			} else {
				poolRetainFor(t, sessions, "unrelated-workspace")
			}
			watcher, dir := newWatcherOverPool(t, stub, sessions, clock)
			for cycle := 1; cycle <= 3; cycle++ {
				clock.advance(time.Minute)
				require.NoError(t, watcher.cycleIfDue(testCtx(t, 3*time.Second), clock.Now()))
				poolAwaitCount(t, stub.openCount, 0, "each cycle closes its real server socket")
				require.Zero(t, sessions.OpenSockets(), "no retained watcher socket between intervals")
				require.Equal(t, cycle, stub.acceptCount(), "each closed-panel cycle needs exactly one request-owned session")
				state := watcherStateForPoolTest(t, dir)
				require.Equal(t, clock.Now().Format(time.RFC3339), state.LastSuccessAt, "cycle was executed, not just called")
				require.Equal(t, uint32(2), state.LastSeenUID) // UIDNEXT 3 -> baseline 2
				require.Equal(t, 1, state.UnseenTotal)
			}
			entries := stub.logEntries()
			require.Equal(t, 3, countStr(entries, " STATUS "))
			require.Zero(t, countStr(entries, " SELECT "), "STATUS-style watcher lease has no unnecessary selected state")
			require.Zero(t, countStr(entries, " EXAMINE "))
			require.Zero(t, countStr(entries, " STORE "), "watcher does not mutate flags")
		})
	}
}
