package email

// W1 runtime RED: spec §4.2–4.7, US-1/2/3/4/7, MC-W1-1/3/5/6/11/12/13/14/20.
// Counts 2/8 are normative socket+reservation ceilings, NOT account work slots.
// B-W1-6/MC-W1-2/T2/CX-1 instead require refusal after ONE reservation;
// that contradictory criterion is unresolved and not silently reinterpreted.
// This file tests §4.2/FR-W1-3 and the ADR's unambiguous THIRD/NINTH thresholds.
// No compiler blocker is counted as observed behavioral RED.

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

type poolAcquireResult struct {
	lease Lease
	err   error
}

func poolAcquireAsync(sessions *MailSessions, ctx context.Context, req LeaseRequest) <-chan poolAcquireResult {
	out := make(chan poolAcquireResult, 1)
	go func() { lease, err := sessions.Acquire(ctx, req); out <- poolAcquireResult{lease, err} }()
	return out
}

func poolHold(t *testing.T, sessions *MailSessions, id sessionIdentity, folder string, retain bool) Lease {
	t.Helper()
	lease, err := sessions.Acquire(testCtx(t, 30*time.Second), id.leaseRequest(folder, retain, false))
	require.NoError(t, err, "at/below W1's ceiling, a demand must acquire")
	// Manager.Close owns failure-path cleanup. Do not call Release twice:
	// release idempotence is not part of the frozen lease contract.
	return lease
}

func poolBusy(t *testing.T, err error) {
	t.Helper()
	require.ErrorIs(t, err, ErrPoolBusy, "W1 §4.4: distinct typed pool-busy outcome")
	require.NotErrorIs(t, err, ErrMailBusy, "account-slot exhaustion is a different outcome")
	require.NotErrorIs(t, err, context.DeadlineExceeded, "capacity exhaustion is not a command timeout")
}

// US-2/AS-1/2; MC-W1-1/3. Bursts race the reservation decision itself.
// Both server and transport high-water counts detect even a transient over-cap.
func TestPool_CeilingsHoldUnderConcurrentDemand(t *testing.T) {
	for _, limit := range []int{2, 8} { // §4.2 mailbox and global ceilings
		t.Run(fmt.Sprintf("cap_%d", limit), func(t *testing.T) {
			stub, clock := newStubIMAPServer(t), newPoolTestClock()
			sessions := newSessionsForStub(t, stub, clock)
			probe := swapCountingDial(t)
			idFor := func(i int) sessionIdentity {
				pair := "agent-a/ws-a"
				if limit == 8 {
					pair = fmt.Sprintf("agent-%d/ws-%d", i, i)
				}
				return sessionIdentity{pair, stub.addr(), "gen-1"}
			}
			require.Zero(t, probe.live.Load(), "zero work means zero prewarmed sockets")
			var admitted []<-chan poolAcquireResult
			for i := 0; i < limit; i++ {
				admitted = append(admitted, poolAcquireAsync(sessions, testCtx(t, 30*time.Second),
					idFor(i).leaseRequest(fmt.Sprintf("F%d", i), false, false)))
			}
			var leases []Lease
			for _, ch := range admitted {
				got := expectResult(t, ch, 3*time.Second, "below/at-cap acquisition")
				require.NoError(t, got.err)
				leases = append(leases, got.lease)
			}
			poolAwaitCount(t, stub.openCount, limit, "all allowed leases have real sockets")
			require.Equal(t, int32(limit), probe.live.Load())

			// More than one overflow contender: a check-then-dial race cannot
			// hide behind a single ninth request. All holders remain ACTIVE.
			var overflow []<-chan poolAcquireResult
			start := time.Now()
			for i := 0; i < 6; i++ { // synthetic pressure count, not a product limit
				overflow = append(overflow, poolAcquireAsync(sessions, testCtx(t, 6*time.Second),
					idFor(limit+i).leaseRequest("Archive", false, false)))
			}
			for _, ch := range overflow {
				got := expectResult(t, ch, 7*time.Second, "overflow demand")
				if got.err == nil {
					got.lease.Release()
				}
				poolBusy(t, got.err)
			}
			require.Less(t, time.Since(start), 7*time.Second, "MC-W1-3: 5 s wait plus specified slack")
			require.Equal(t, limit, stub.acceptCount(), "no over-cap socket was accepted")
			require.Equal(t, int32(limit), probe.starts.Load(), "no over-cap dial was attempted")
			require.LessOrEqual(t, probe.peak.Load(), int32(limit), "no transient over-cap transport socket")
			require.LessOrEqual(t, stub.maxOpenObserved(), limit)
			for _, lease := range leases {
				lease.Release()
			}
			poolAwaitCount(t, stub.openCount, 0, "unretained completed work closes")
		})
	}
}

// FR-W1-3/5: both ceilings count all-connecting AND mixed connecting/established
// capacity. Cancelled reservations release before the error propagates.
func TestPool_ReservationsCountWhileConnecting(t *testing.T) {
	for _, established := range []int{0, 1} {
		t.Run(fmt.Sprintf("established_%d", established), func(t *testing.T) {
			poolConnectingCapacity(t, 2, established)
		})
	}
}

func TestPool_GlobalReservationsCountWhileConnecting(t *testing.T) {
	for _, established := range []int{0, 7} { // 8 connecting; then 7 established + 1 connecting
		t.Run(fmt.Sprintf("established_%d", established), func(t *testing.T) {
			poolConnectingCapacity(t, 8, established)
		})
	}
}

func poolConnectingCapacity(t *testing.T, limit, established int) {
	t.Helper()
	stub, clock := newStubIMAPServer(t), newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)
	probe := swapCountingDial(t)
	idFor := func(i int) sessionIdentity {
		pair := "agent-a/ws-a"
		if limit == 8 {
			pair = fmt.Sprintf("agent-%d/ws-%d", i, i)
		}
		return sessionIdentity{pair, stub.addr(), "gen-1"}
	}
	var holders []Lease
	for i := 0; i < established; i++ {
		holders = append(holders, poolHold(t, sessions, idFor(i), "INBOX", false))
	}
	stub.setMode(stubStallGreeting)
	var parked []<-chan poolAcquireResult
	var cancels []context.CancelFunc
	for i := established; i < limit; i++ {
		ctx, cancel := context.WithCancel(testCtx(t, 20*time.Second))
		cancels = append(cancels, cancel)
		t.Cleanup(cancel)
		parked = append(parked, poolAcquireAsync(sessions, ctx, idFor(i).leaseRequest("Sent", false, false)))
	}
	poolAwaitCount(t, stub.acceptCount, limit, "all reservations entered TCP, stalled before LOGIN")
	require.Equal(t, established, stub.loginCount(), "connecting != established: LOGIN count is the control")
	start := time.Now()
	got := expectResult(t, poolAcquireAsync(sessions, testCtx(t, 6*time.Second),
		idFor(limit).leaseRequest("Archive", false, false)), 7*time.Second, "over-reservation request")
	if got.err == nil {
		got.lease.Release()
	}
	poolBusy(t, got.err)
	require.Less(t, time.Since(start), 7*time.Second, "MC-W1-3")
	require.Equal(t, int32(limit), probe.starts.Load(), "reservations count BEFORE establishment")
	require.Equal(t, limit, stub.acceptCount(), "no third/ninth accepted socket")
	require.Equal(t, int32(limit), probe.peak.Load())
	for _, cancel := range cancels {
		cancel()
	}
	for _, ch := range parked {
		result := expectResult(t, ch, 3*time.Second, "cancelled connecting reservation")
		require.ErrorIs(t, result.err, context.Canceled)
	}
	for _, lease := range holders {
		lease.Release()
	}
	poolAwaitCount(t, stub.openCount, 0, "cancelled dials leave no server socket")
	require.Zero(t, probe.live.Load(), "cancelled reservations leave no transport socket")
	require.Zero(t, sessions.OpenSockets(), "manager's socket count returns to baseline")
	// Fill the SAME whole capacity again, not merely one slot: a partial leak
	// of a per-mailbox or global reservation would prevent full recovery.
	stub.setMode(stubNormal)
	for i := 0; i < limit; i++ {
		poolHold(t, sessions, idFor(i), "INBOX", false)
	}
	poolAwaitCount(t, stub.openCount, limit, "all cancelled capacity is immediately reusable")
}

// US-2/AS-4; MC-W1-5. Exercise BOTH failed reservations on a mailbox, then
// recover its two slots AND the full process-wide eight slots.
func TestPool_FailedDialReleasesReservation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	sessions := newSessionsForAddr(t, addr, newPoolTestClock())
	id := sessionIdentity{"agent-a/ws-a", addr, "gen-1"}
	for i := 0; i < 2; i++ {
		_, err = sessions.Acquire(testCtx(t, 2*time.Second), id.leaseRequest("INBOX", false, false))
		require.ErrorIs(t, err, syscall.ECONNREFUSED, "actual refused dial, not pool-busy or a setup failure")
		require.Zero(t, sessions.OpenSockets(), "failed establishment released its socket before returning")
	}
	stub := newStubIMAPServerOnAddr(t, addr)
	poolHold(t, sessions, id, "INBOX", false)
	poolHold(t, sessions, id, "Sent", false)
	for i := 2; i < 8; i++ {
		other := sessionIdentity{fmt.Sprintf("agent-%d/ws-%d", i, i), addr, "gen-1"}
		poolHold(t, sessions, other, "INBOX", false)
	}
	poolAwaitCount(t, stub.openCount, 8, "per-mailbox and global failed-dial reservations were all returned")
}

// US-1/AS-1/2/3, US-3/AS-1; MC-W1-6; FR-W1-24. Exact rows and epoch data
// come from the seeded network peer; a reused lease must reselect INBOX.
func TestLease_ReSelectBeforeUse(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	poolRetainFor(t, sessions, "ws-a")
	id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
	first := poolHold(t, sessions, id, "Sent", true)
	rows, err := stubFetchRows(t, first)
	require.NoError(t, err)
	require.Equal(t, []string{"Sent-row-1", "Sent-row-2"}, rows)
	first.Release()
	poolAwaitCount(t, stub.openCount, 1, "healthy eligible session is retained (positive control)")
	second := poolHold(t, sessions, id, "INBOX", true)
	require.Same(t, first.Client(), second.Client(), "reuse must skip connection setup")
	require.Equal(t, 1, stub.acceptCount())
	require.Equal(t, 1, stub.loginCount())
	require.Equal(t, uint32(4242), second.UIDValidity, "same-lease epoch from seeded SELECT")
	require.Equal(t, uint32(2), second.NumMessages, "SELECT count is available before fetch")
	require.Equal(t, "gen-1", second.Generation)
	rows, err = stubFetchRows(t, second)
	require.NoError(t, err)
	require.Equal(t, []string{"INBOX-row-1", "INBOX-row-2"}, rows, "CX-2: previous Sent selection cannot leak")
	entries := stub.logEntries()
	require.Equal(t, 1, countStr(entries, "SELECT Sent"))
	require.Equal(t, 1, countStr(entries, "SELECT INBOX"))
	require.Equal(t, 2, countStr(entries, "FETCH"))
	second.Release()
}

// US-3/AS-3; B-W1-8. Pause A BEFORE asking for B; B must complete while A
// owns the first selected connection. Read selected state after unpausing A.
func TestLease_ConcurrentBorrowersIsolated(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
	a := poolHold(t, sessions, id, "INBOX", false)
	stub.holdFetchAt(1)
	t.Cleanup(func() { stub.releaseFetch(1) })
	type fetchResult struct {
		rows []string
		err  error
	}
	out := make(chan fetchResult, 1)
	go func() { rows, err := stubFetchRows(t, a); out <- fetchResult{rows, err} }()
	waitFor(t, 2*time.Second, "A reached its parked FETCH", func() bool { return countStr(stub.logEntries(), "FETCH") == 1 })
	gotB := expectResult(t, poolAcquireAsync(sessions, testCtx(t, 5*time.Second),
		id.leaseRequest("Sent", false, false)), 3*time.Second, "B acquires while A is active")
	require.NoError(t, gotB.err)
	rowsB, err := stubFetchRows(t, gotB.lease)
	require.NoError(t, err)
	require.Equal(t, []string{"Sent-row-1", "Sent-row-2"}, rowsB)
	require.NotSame(t, a.Client(), gotB.lease.Client(), "exclusive leases cannot share one selected-state client")
	require.Equal(t, 2, stub.acceptCount())
	stub.releaseFetch(1)
	gotA := expectResult(t, out, 3*time.Second, "A's completed fetch")
	require.NoError(t, gotA.err)
	require.Equal(t, []string{"INBOX-row-1", "INBOX-row-2"}, gotA.rows, "B's SELECT cannot overwrite A's folder")
	a.Release()
	gotB.lease.Release()
}

// Cancellation is poison (§4.5). Retention is deliberately ON: otherwise a
// healthy release also closes, concealing a poison-returned-to-idle mutant.
func TestPool_CancelMidFlightReleasesReservationAndRetires(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	poolRetainFor(t, sessions, "ws-a")
	probe := swapCountingDial(t)
	baseline := goleak.IgnoreCurrent() // do not ignore any later reader/acquire goroutine
	id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
	ctx, cancel := context.WithCancel(testCtx(t, 10*time.Second))
	t.Cleanup(cancel)
	lease, err := sessions.Acquire(ctx, id.leaseRequest("INBOX", true, false))
	require.NoError(t, err)
	stub.setMode(stubStallFetch)
	out := make(chan error, 1)
	go func() { _, err := stubFetchRows(t, lease); out <- err }()
	waitFor(t, 2*time.Second, "FETCH reached server before cancellation", func() bool { return countStr(stub.logEntries(), "FETCH") == 1 })
	cancel()
	require.Error(t, expectResult(t, out, 3*time.Second, "cancelled command reader returns"))
	lease.Release()
	expectResult(t, lease.Client().Closed(), 2*time.Second, "IMAP reader termination acknowledgment")
	poolAwaitCount(t, stub.openCount, 0, "cancelled server socket terminated")
	require.Zero(t, probe.live.Load())
	require.Zero(t, sessions.OpenSockets())
	goleak.VerifyNone(t, baseline) // no +3 allowance that could hide a leaked goroutine
	stub.setMode(stubNormal)
	// Two fresh acquisitions prove every mailbox reservation was released.
	a := poolHold(t, sessions, id, "INBOX", false)
	b := poolHold(t, sessions, id, "Sent", false)
	require.NotSame(t, lease.Client(), a.Client())
	require.NotSame(t, lease.Client(), b.Client())
	require.Equal(t, 3, stub.acceptCount(), "retired socket is never reused")
	for _, fresh := range []Lease{a, b} {
		fresh.Release()
	}
	poolAwaitCount(t, stub.openCount, 0, "follow-up request-owned sockets close")
	goleak.VerifyNone(t, baseline)
}

// FR-W1-13; MC-W1-12. Borrow order differs from completed-use order: F2 is
// borrowed early but finishes LAST. F3 is the oldest completed idle; F1 is active.
func TestPool_LRUEvictsOnlyIdle(t *testing.T) {
	stub, clock := newStubIMAPServer(t), newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)
	probe := swapCountingDial(t)
	idFor := func(i int) sessionIdentity {
		ws := fmt.Sprintf("ws-%d", i)
		poolRetainFor(t, sessions, ws)
		return sessionIdentity{fmt.Sprintf("agent-%d/%s", i, ws), stub.addr(), "gen-1"}
	}
	active := poolHold(t, sessions, idFor(1), "F1", true)
	late := poolHold(t, sessions, idFor(2), "F2", true)
	for i := 3; i <= 8; i++ {
		clock.advance(time.Second) // distinct last-completed timestamps
		lease := poolHold(t, sessions, idFor(i), fmt.Sprintf("F%d", i), true)
		_, err := stubFetchRows(t, lease)
		require.NoError(t, err)
		lease.Release()
	}
	clock.advance(time.Second)
	_, err := stubFetchRows(t, late)
	require.NoError(t, err)
	late.Release()
	poolAwaitCount(t, stub.openCount, 8, "one active and seven eligible idles fill global capacity")
	ninth := poolHold(t, sessions, idFor(9), "INBOX", true)
	waitFor(t, 2*time.Second, "only F3 (oldest completed idle) was evicted", func() bool {
		return len(stub.liveSessionIDs()) == 8 && stub.liveSessionIDs()[3] == ""
	})
	require.Equal(t, map[int]string{1: "F1", 2: "F2", 4: "F4", 5: "F5", 6: "F6", 7: "F7", 8: "F8", 9: "INBOX"}, stub.liveSessionIDs())
	require.LessOrEqual(t, probe.peak.Load(), int32(8), "close before replacement, no transient ninth socket")
	rows, err := stubFetchRows(t, active)
	require.NoError(t, err, "active work survived another mailbox's warm-up")
	require.Equal(t, []string{"F1-row-1", "F1-row-2"}, rows)
	require.Zero(t, countStr(stub.logEntries(), " CLOSE "), "LRU eviction never expunges")
	ninth.Release()
	active.Release()
}

// US-4/AS-1; §4.6.3, MC-W1-13. Dedicated readable all-eight-busy scenario.
func TestPool_AllEightActiveTypedBusy(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	probe := swapCountingDial(t)
	for i := 0; i < 8; i++ {
		poolHold(t, sessions, sessionIdentity{fmt.Sprintf("a%d/w%d", i, i), stub.addr(), "gen-1"}, "INBOX", false)
	}
	start := time.Now()
	_, err := sessions.Acquire(testCtx(t, 6*time.Second),
		sessionIdentity{"ninth/ws-9", stub.addr(), "gen-1"}.leaseRequest("INBOX", false, false))
	poolBusy(t, err)
	require.Less(t, time.Since(start), 7*time.Second, "MC-W1-3 specified scheduling slack")
	require.Equal(t, 8, stub.acceptCount())
	require.Equal(t, int32(8), probe.starts.Load(), "no ninth connection attempt")
	require.Equal(t, int32(8), probe.peak.Load())
}

// MC-W1-11, §4.6.1. Active duration does not count as idle. Reuse at 119 s
// resets the last-COMPLETED-use clock, then release occurs at 121 s, not sooner.
func TestPool_IdleReleaseAfterLastCompletedUse(t *testing.T) {
	stub, clock := newStubIMAPServer(t), newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)
	poolRetainFor(t, sessions, "ws-a")
	id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
	lease := poolHold(t, sessions, id, "INBOX", true)
	clock.advance(5 * time.Minute) // operation is still active, not idle
	clock.sweepsAfterAdvance(t)
	require.Equal(t, 1, stub.openCount(), "expiry cannot close active work")
	_, err := stubFetchRows(t, lease)
	require.NoError(t, err)
	lease.Release()
	for _, step := range []time.Duration{90 * time.Second, 29 * time.Second} {
		clock.advance(step)
		clock.sweepsAfterAdvance(t)
		require.Equal(t, 1, stub.openCount(), "healthy-idle survives before two minutes since completion")
	}
	second := poolHold(t, sessions, id, "Sent", true)
	require.Same(t, lease.Client(), second.Client(), "119 s old idle can be reused")
	_, err = stubFetchRows(t, second)
	require.NoError(t, err)
	second.Release()
	clock.advance(119 * time.Second)
	clock.sweepsAfterAdvance(t)
	require.Equal(t, 1, stub.openCount(), "later completion reset idle age; first completion cannot expire this socket")
	clock.advance(2 * time.Second) // 121 s since last completed use, plus sweep slack
	poolAwaitCount(t, stub.openCount, 0, "idle socket released after two minutes")
	require.Zero(t, sessions.OpenSockets())
	expectResult(t, second.Client().Closed(), time.Second, "idle reader termination")
}

// US-7/AS-6; MC-W1-20. No implicit presence authority from an eligible request.
func TestPool_RetentionDisabledUntilWired(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	require.False(t, sessions.RetentionEnabled(), "§4.7.4: conservative unwired default")
	id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
	for i := 1; i <= 2; i++ {
		lease := poolHold(t, sessions, id, "INBOX", true)
		lease.Release()
		poolAwaitCount(t, stub.openCount, 0, "unwired presence always closes after completed work")
		require.Zero(t, sessions.OpenSockets())
		require.Equal(t, i, stub.acceptCount(), "nothing retained for the next request")
	}
}

// §4.6.4; MC-W1-14, CX-7. Real memserver state catches folder-CLOSE/EXPUNGE
// on request release, idle expiry AND LRU eviction, not merely log occurrence.
func TestPool_ReleaseNeverExpunges(t *testing.T) {
	for _, teardown := range []string{"release", "idle_expiry", "eviction"} {
		t.Run(teardown, func(t *testing.T) {
			client := startMemIMAP(t, [][]byte{mkMsg("Keep", "a@x.test", "one"), mkMsg("Also keep", "a@x.test", "two")}, nil)
			addr := net.JoinHostPort(client.acct.IMAPHost, itoa(client.acct.IMAPPort))
			clock := newPoolTestClock()
			sessions := newSessionsForAddr(t, addr, clock)
			retain := teardown != "release"
			if retain {
				poolRetainFor(t, sessions, "ws-a")
			}
			id := sessionIdentity{"agent-a/ws-a", addr, "gen-1"}
			lease := poolHold(t, sessions, id, "INBOX", retain)
			require.NoError(t, stubStoreDeleted(t, lease, 1))
			lease.Release()
			if teardown == "idle_expiry" {
				clock.advance(121 * time.Second)
				expectResult(t, lease.Client().Closed(), 2*time.Second, "idle expiry closes the selected session")
			}
			if teardown == "eviction" {
				peer := newStubIMAPServer(t)
				clock.advance(time.Second) // selected memserver session is oldest completed idle
				for i := 1; i <= 8; i++ {
					ws := fmt.Sprintf("w%d", i)
					poolRetainFor(t, sessions, ws)
					other := poolHold(t, sessions, sessionIdentity{fmt.Sprintf("a%d/%s", i, ws), peer.addr(), "gen-1"}, "INBOX", true)
					other.Release()
					clock.advance(time.Second)
				}
				expectResult(t, lease.Client().Closed(), 2*time.Second, "LRU evicts oldest completed session")
			}
			check := poolHold(t, sessions, id, "INBOX", false)
			require.Equal(t, []uint32{1, 2}, stubFetchUIDs(t, check), "Deleted UID 1 survives pool teardown; no silent expunge")
			check.Release()
		})
	}
}
