package email

// W1 §4.1/§4.4/§3.1 row 17 / §8 rows 25, 26, 30; B-W1-34, MC-W1-26,
// MC-W1-4, MC-W1-28. Audit hole F3 (partial): the nil-source wiring error,
// the deadline bounds and the instrument sub-fields were absent from the
// runtime pack. Scope notes: T26's W1-observable half is subordination —
// §4.4 makes the 45 s total the ceiling the dial and every command ride
// under, and a caller-supplied shorter deadline always wins, so the bound is
// provable in seconds; the full 44/45 s boundary elapsed matrix (DS-7) is
// w6-proof's, with its controllable clock. T30 asserts W1's emitter seam in
// w6-proof §6.1's frozen outcome domain ("ok" | "pool_busy"); the record
// shape's single publisher is w6-proof — W1 adds no field of its own, and a
// coalesced joiner never reaches the pool, so it emits nothing (the record
// sum stays comparable to the server's connection counter). Expected values
// derive from the spec, never from the implementation.

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mailboxStatusErr runs MailboxStatus and returns only its error: the
// wiring tests here assert the typed failure, never the counters (which
// cannot be produced when the session source is missing — the very defect
// under test).
func mailboxStatusErr(ctx context.Context, c *Client) error {
	unseen, uidNext, uidValidity, err := c.MailboxStatus(ctx)
	_ = unseen
	_ = uidNext
	_ = uidValidity
	return err
}

// TestClient_MissingSourceFailsVisibly asserts MC-W1-26/B-W1-34: a source-
// less client in a process where the shared manager exists fails immediately
// with the typed missing-wiring error — naming the missing injection — and
// attempts zero dials. The pre-injection window's legacy per-call dial
// (manager genuinely absent) is the preserved regression suite's existing
// behavior, not this test's.
func TestClient_MissingSourceFailsVisibly(t *testing.T) {
	stub := newStubIMAPServer(t)
	// Declare this test's world explicitly: the manager is wired. SharedMailSessions
	// below also sets the process-wide flag, but the pin is what makes the world a
	// declared fixture rather than a side effect — the previous state is restored at
	// cleanup, so this test no longer rewrites the world every later test runs in
	// (order-independence fixture, session_wiring_fixture_test.go).
	pinSessionManagerWired(t, true)
	sessions := SharedMailSessions(t.TempDir(), SessionsConfig{
		IdleSweepInterval: testSweepInterval,
		Credentials:       func(string) (string, string, error) { return testIMAPUser, testIMAPPass, nil },
	})
	t.Cleanup(sessions.Close)
	require.True(t, sessionManagerWired(), "premise: the shared manager exists in this process")

	host, port, err := net.SplitHostPort(stub.addr())
	require.NoError(t, err)
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	client, err := NewClient(Account{IMAPHost: host, IMAPPort: n, SMTPHost: host, Username: testIMAPUser, Password: testIMAPPass})
	require.NoError(t, err)
	// Deliberately NO SetSessionSource — the missed injection MC-W1-26
	// exists for. The manager is wired, so the legacy dial must be
	// unreachable, not silent.
	probe := swapCountingDial(t)

	err = mailboxStatusErr(testCtx(t, 3*time.Second), client)
	require.ErrorIs(t, err, ErrSessionSourceMissing, "typed wiring error, not a silent uncounted dial")
	require.Contains(t, err.Error(), "SetSessionSource", "FR-W1-2: the error names the missing wiring")

	_, err = client.ReadInbox(testCtx(t, 3*time.Second), InboxOptions{Limit: 5})
	require.ErrorIs(t, err, ErrSessionSourceMissing, "the read path is equally unwritable without the source")

	require.Zero(t, probe.starts.Load(), "zero dial attempts — the uncounted path is unreachable")
	require.Zero(t, stub.acceptCount(), "the server saw no connection")
	require.Zero(t, sessions.OpenSockets(), "no socket exists after the refused operations")
}

// TestPool_DeadlineBounds asserts MC-W1-4's W1-observable half: the founder
// numbers are pinned where the code defines them, and the total read
// deadline is the ceiling the dial and every command ride under.
func TestPool_DeadlineBounds(t *testing.T) {
	t.Run("founder_bounds_are_pinned", func(t *testing.T) {
		// §4.4's table; §2.1's consts. §4.4: "the two are distinct clocks
		// and the spec keeps both names separate" — each name keeps its own
		// spec value.
		require.Equal(t, 45*time.Second, totalReadDeadline, "§4.4: total read-work ceiling (founder Q3=A)")
		require.Equal(t, 30*time.Second, dialTimeout, "§4.4: dial ceiling, subordinate to the total")
		require.Equal(t, 45*time.Second, commandTimeout, "§4.4: per-command bound, subordinate to the remaining total")
		require.Equal(t, 5*time.Second, poolAcquireWait, "§4.4: pool-acquisition wait ceiling")
	})

	t.Run("dial_stall_ends_inside_caller_total", func(t *testing.T) {
		stub, clock := newStubIMAPServer(t), newPoolTestClock()
		stub.setMode(stubStallGreeting)
		// The REAL dial seam (no counting swap): the greeting stall must end
		// through the dial path's own deadline machinery, which is what
		// production runs.
		sessions := NewMailSessions(SessionsConfig{
			StateDir: t.TempDir(), Now: clock.Now, IdleSweepInterval: testSweepInterval,
			Credentials: func(string) (string, string, error) { return testIMAPUser, testIMAPPass, nil },
		})
		t.Cleanup(sessions.Close)
		budget := NewMailBudget(t.TempDir())

		started := time.Now()
		err := budget.Call(testCtx(t, 30*time.Second), MailBudgetRequest{
			Account: stub.addr() + "|" + testIMAPUser, AgentID: "deadline-agent", WorkspaceID: "ws-d",
			Operation: "listMailMessages", Params: map[string]any{"folder": "INBOX"}, Purpose: "read_live",
			TotalReadDeadline: 3 * time.Second, // §4.4: a caller-supplied shorter deadline wins
		}, func(ctx context.Context) error {
			_, aerr := sessions.Acquire(ctx,
				sessionIdentity{"deadline-agent/ws-d", stub.addr(), "gen-1"}.leaseRequest("INBOX", false, false))
			return aerr
		})
		elapsed := time.Since(started)
		require.ErrorIs(t, err, context.DeadlineExceeded, "the total read deadline ends the read with the timeout class")
		require.GreaterOrEqual(t, elapsed, 2*time.Second, "the ceiling genuinely bounded the read — not an instant failure")
		require.Less(t, elapsed, 20*time.Second,
			"§4.4: the dial can never outlive the read it serves — it must not ride the 30 s dial ceiling")
		require.Equal(t, 1, stub.acceptCount(), "the dial was genuinely attempted inside the total")
		poolAwaitCount(t, stub.openCount, 0, "the aborted establishment released its socket")
	})

	t.Run("command_stall_subordinate_to_remaining_total", func(t *testing.T) {
		stub, clock := newStubIMAPServer(t), newPoolTestClock()
		// The stall applies to FETCH answers only, so establishment and
		// SELECT complete normally and the command bound is what fires.
		stub.setMode(stubStallFetch)
		sessions := newSessionsForStub(t, stub, clock)
		budget := NewMailBudget(t.TempDir())
		client := poolFacadeForStub(t, stub, sessions)

		started := time.Now()
		err := budget.Call(testCtx(t, 30*time.Second), MailBudgetRequest{
			Account: stub.addr() + "|" + testIMAPUser, AgentID: "deadline-agent", WorkspaceID: "ws-d",
			Operation: "read_inbox", Params: map[string]any{"folder": "INBOX"}, Purpose: "read_live",
			TotalReadDeadline: 3 * time.Second,
		}, func(ctx context.Context) error {
			_, rerr := client.ReadInbox(ctx, InboxOptions{Limit: 5})
			return rerr
		})
		elapsed := time.Since(started)
		require.ErrorIs(t, err, context.DeadlineExceeded, "§4.4: a command's effective bound is min(45 s, remaining total)")
		require.GreaterOrEqual(t, elapsed, 2*time.Second, "the remaining total genuinely bounded the command")
		require.Less(t, elapsed, 20*time.Second,
			"§4.4: the command must not ride its own 45 s bound when the total expires first")
		require.Equal(t, 1, stub.acceptCount(), "exactly one establishment before the stalled command")
		poolAwaitCount(t, stub.openCount, 0, "the poisoned operation released its socket")
	})
}

// TestPool_InstrumentSubFields asserts MC-W1-28: every pooled operation
// carries W1's sub-fields of the instrument record in w6-proof §6.1's
// frozen outcome domain, and a coalesced joiner emits nothing.
func TestPool_InstrumentSubFields(t *testing.T) {
	newInstrumented := func(t *testing.T) (*MailSessions, *stubIMAPServer, *[]PoolInstrumentSample) {
		t.Helper()
		stub := newStubIMAPServer(t)
		samples := &[]PoolInstrumentSample{}
		sessions := NewMailSessions(SessionsConfig{
			StateDir: t.TempDir(), IdleSweepInterval: testSweepInterval,
			Credentials: func(string) (string, string, error) { return testIMAPUser, testIMAPPass, nil },
			Instrument:  func(s PoolInstrumentSample) { *samples = append(*samples, s) },
		})
		t.Cleanup(sessions.Close)
		return sessions, stub, samples
	}

	t.Run("establish_and_reuse_outcomes", func(t *testing.T) {
		sessions, stub, samples := newInstrumented(t)
		identity := sessionIdentity{"agent-i/ws-i", stub.addr(), "gen-1"}
		sessions.EnableRetention()
		sessions.Presence().Bind("authenticated-test-conn", "panel-ws-i", "ws-i")

		lease := poolHold(t, sessions, identity, "INBOX", true)
		lease.Release()
		got := *samples
		require.Len(t, got, 1, "exactly one record for the establishing operation")
		require.Equal(t, "ok", got[0].Outcome, "MC-W1-28: success records outcome=ok")
		require.Equal(t, 1, got[0].SocketCount, "MC-W1-28: an establishment records socket_count=1")
		require.GreaterOrEqual(t, got[0].AcquireWaitMs, int64(0), "acquire_wait_ms is populated, not negative")

		reused := poolHold(t, sessions, identity, "INBOX", true)
		reused.Release()
		got = *samples
		require.Len(t, got, 2)
		require.Equal(t, "ok", got[1].Outcome)
		require.Equal(t, 0, got[1].SocketCount,
			"MC-W1-28: a reuse adds no connection — the record sum stays comparable to the server's counter")
		require.Equal(t, 1, stub.acceptCount(), "two operations, one server connection")
	})

	t.Run("pool_busy_after_bounded_wait", func(t *testing.T) {
		sessions, stub, samples := newInstrumented(t)
		for i := 0; i < 8; i++ {
			poolHold(t, sessions, sessionIdentity{fmt.Sprintf("fg-%d/ws-%d", i, i), stub.addr(), "gen-1"}, "INBOX", false)
		}
		poolAwaitCount(t, stub.openCount, 8, "foreground holds every global socket")

		started := time.Now()
		_, err := sessions.Acquire(testCtx(t, 15*time.Second),
			sessionIdentity{"busy-agent/ws-b", stub.addr(), "gen-1"}.leaseRequest("INBOX", false, false))
		poolBusy(t, err)
		require.Less(t, time.Since(started), 10*time.Second, "the busy outcome arrives inside the bounded window plus slack")

		require.Len(t, *samples, 9, "eight holds plus the refused demand — nine records")
		busy := 0
		for _, s := range *samples {
			if s.Outcome == "pool_busy" {
				busy++
				require.Equal(t, 0, s.SocketCount, "MC-W1-28: pool_busy records socket_count=0")
				require.GreaterOrEqual(t, s.AcquireWaitMs, int64(4000),
					"§4.4: the record's acquire_wait_ms carries the bounded 5 s wait that elapsed")
			}
		}
		require.Equal(t, 1, busy, "exactly the ninth demand records the pool_busy outcome")
	})

	t.Run("coalesced_joiner_emits_nothing", func(t *testing.T) {
		sessions, stub, samples := newInstrumented(t)
		budget := NewMailBudget(t.TempDir())
		identity := sessionIdentity{"join-a/ws-j", stub.addr(), "gen-1"}

		gate := make(chan struct{})
		entered := make(chan struct{}, 1)
		exec := func(ctx context.Context) (any, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-gate
			lease, err := sessions.Acquire(ctx, identity.leaseRequest("INBOX", false, false))
			if err != nil {
				return nil, err
			}
			lease.Release()
			return "shared", nil
		}
		req := MailBudgetRequest{Account: stub.addr() + "|" + testIMAPUser,
			AgentID: "join-a", WorkspaceID: "ws-j", Generation: "gen-1",
			Operation: "listMailMessages", Params: map[string]any{"folder": "INBOX"}, Purpose: "read_live"}

		type callResult struct {
			value any
			err   error
		}
		resA := make(chan callResult, 1)
		resB := make(chan callResult, 1)
		go func() {
			v, err := CallValue(budget, testCtx(t, 10*time.Second), req, exec)
			resA <- callResult{v, err}
		}()
		waitFor(t, 2*time.Second, "the first caller entered the flight", func() bool {
			select {
			case <-entered:
				return true
			default:
				return false
			}
		})
		go func() {
			v, err := CallValue(budget, testCtx(t, 10*time.Second), req, exec)
			resB <- callResult{v, err}
		}()
		time.Sleep(50 * time.Millisecond) // the second caller joins the running flight
		close(gate)

		for _, res := range []<-chan callResult{resA, resB} {
			got := expectResult(t, res, 5*time.Second, "the coalesced call")
			require.NoError(t, got.err)
			require.Equal(t, "shared", got.value, "the joiner receives the executor's value")
		}
		require.Equal(t, 1, stub.acceptCount(), "one shared flight, one server connection")
		got := *samples
		require.Len(t, got, 1, "MC-W1-28: the joiner never reaches the pool and emits no record — the sum stays comparable")
		require.Equal(t, "ok", got[0].Outcome)
		require.Equal(t, 1, got[0].SocketCount)
	})
}
