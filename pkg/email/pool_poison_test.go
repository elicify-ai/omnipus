package email

// W1 §4.3.3/4.5; US-3/AS-4, US-5/AS-1/2/3/4; MC-W1-8/9/10; CX-3/8.
// Retention is enabled with a matching observer so retiring differs from a
// healthy release. Network counters, reader completion and exact rows are
// observed; the manager, facade and go-imap parser remain real.

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestPool_PoisonOnTimeout_Cancel_Bye_Protocol(t *testing.T) {
	cases := []struct {
		name, class string
		mode        stubMode
		cancel      bool
	}{
		{"command_timeout", "timeout", stubStallFetch, false},
		{"cancellation", "timeout", stubStallFetch, true},
		{"server_BYE", "server_error", stubSendBye, false},
		{"protocol_error", "server_error", stubGarbage, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStubIMAPServer(t)
			sessions := newSessionsForStub(t, stub, newPoolTestClock())
			poolRetainFor(t, sessions, "ws-a")
			probe := swapCountingDial(t)
			baseline := goleak.IgnoreCurrent()
			id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
			warm := poolHold(t, sessions, id, "INBOX", true)
			rows, err := stubFetchRows(t, warm)
			require.NoError(t, err)
			require.Equal(t, []string{"INBOX-row-1", "INBOX-row-2"}, rows)
			warm.Release()
			poolAwaitCount(t, stub.openCount, 1, "healthy control returns to retained idle pool")
			ctx, cancel := context.WithCancel(testCtx(t, 3*time.Second))
			if !tc.cancel {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond) // shorter caller deadline, §4.4
			}
			t.Cleanup(cancel)
			lease, err := sessions.Acquire(ctx, id.leaseRequest("INBOX", true, false))
			require.NoError(t, err)
			require.Same(t, warm.Client(), lease.Client(), "test actually reuses an eligible pooled socket")
			stub.setMode(tc.mode)
			out := make(chan error, 1)
			go func() {
				_, err := runIMAP(ctx, "poison read", func() ([]string, error) { return stubFetchRows(t, lease) })
				out <- err
			}()
			waitFor(t, 2*time.Second, "poisoning FETCH reached peer", func() bool { return countStr(stub.logEntries(), "FETCH") == 2 })
			if tc.cancel {
				cancel()
			}
			err = expectResult(t, out, 3*time.Second, "poisoned request ends")
			require.Error(t, err)
			require.Equal(t, tc.class, classifyMailError(err), "spec's named failure class, not an unrelated setup error")
			lease.Release()
			expectResult(t, lease.Client().Closed(), 2*time.Second, "poisoned reader terminated before retirement completes")
			poolAwaitCount(t, stub.openCount, 0, "poison closes, it never returns to retention")
			require.Zero(t, probe.live.Load())
			require.Zero(t, sessions.OpenSockets())
			goleak.VerifyNone(t, baseline)
			stub.setMode(stubNormal)
			fresh := poolHold(t, sessions, id, "Sent", false)
			rows, err = stubFetchRows(t, fresh)
			require.NoError(t, err)
			require.Equal(t, []string{"Sent-row-1", "Sent-row-2"}, rows, "no stale/interleaved INBOX responses")
			require.NotSame(t, lease.Client(), fresh.Client(), "retired client never borrowed again")
			require.Equal(t, 2, stub.acceptCount(), "one healthy replacement establish, no retry chain")
			fresh.Release()
			poolAwaitCount(t, stub.openCount, 0, "request-owned replacement is released")
			goleak.VerifyNone(t, baseline)
		})
	}
}

// US-5/AS-3, MC-W1-9. Retry is bounded by the original request context.
func TestPool_DeadIdleReplacementOnce(t *testing.T) {
	for _, secondDead := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement_succeeds", true: "replacement_also_dead"}[secondDead], func(t *testing.T) {
			stub := newStubIMAPServer(t)
			sessions := newSessionsForStub(t, stub, newPoolTestClock())
			poolRetainFor(t, sessions, "ws-a")
			probe := swapCountingDial(t)
			id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
			warm := poolHold(t, sessions, id, "INBOX", true)
			rows, err := stubFetchRows(t, warm)
			require.NoError(t, err)
			require.Equal(t, []string{"INBOX-row-1", "INBOX-row-2"}, rows)
			warm.Release()
			require.Equal(t, 1, stub.killSessionByFolder("INBOX"), "control: killed the exact retained idle socket")
			poolAwaitCount(t, stub.openCount, 0, "server's idle close completed")
			expectResult(t, warm.Client().Closed(), time.Second, "dead client's reader termination")
			if secondDead {
				stub.setMode(stubKillOnAccept)
			}
			start := time.Now()
			lease, err := sessions.Acquire(testCtx(t, 2*time.Second), id.leaseRequest("INBOX", true, false))
			if secondDead {
				require.Error(t, err, "second dead establishment must fail visibly")
				require.Equal(t, "server_error", classifyMailError(err))
			} else {
				require.NoError(t, err)
				rows, err = stubFetchRows(t, lease)
				require.NoError(t, err)
				require.Equal(t, []string{"INBOX-row-1", "INBOX-row-2"}, rows)
				require.NotSame(t, warm.Client(), lease.Client())
				lease.Release()
			}
			require.Less(t, time.Since(start), 3*time.Second, "replacement cannot reset the caller's original 2 s budget")
			require.Equal(t, int32(2), probe.starts.Load(), "one replacement maximum; never a third dial")
			require.Equal(t, 2, stub.acceptCount())
		})
	}
}

// US-5/AS-4, MC-W1-10. Lose the ACK AFTER a real STORE effect. Exercise the
// production facade, not a test-owned one-STORE helper: an automatic replay
// would visibly add a second effect/connection to the same failed operation.
func TestPool_MutationNeverReplayed(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	client := poolFacadeForStub(t, stub, sessions)
	require.NoError(t, client.MarkSeen(testCtx(t, 3*time.Second), 1), "positive control: healthy mutation executes one STORE")
	require.Equal(t, 1, stub.storeCount())
	require.Equal(t, 1, stub.acceptCount())
	stub.setMode(stubDropStoreReply)
	err := client.MarkSeen(testCtx(t, 3*time.Second), 1)
	require.Error(t, err, "lost mutation ACK must be visible; never silently report success")
	require.Equal(t, "server_error", classifyMailError(err))
	require.Equal(t, 2, stub.storeCount(), "failed operation executed exactly once; no duplicate side effect")
	require.Equal(t, 2, stub.acceptCount(), "no automatic second establish for the failed mutation")
}

// US-3/AS-4; FR-W1-8. [NONEXISTENT] is a lease SelectError, not poisoned
// transport state. A different valid folder must reuse this same healthy socket.
func TestLease_StructuralFolderAbsent_HealthySession(t *testing.T) {
	stub := newStubIMAPServer(t)
	sessions := newSessionsForStub(t, stub, newPoolTestClock())
	poolRetainFor(t, sessions, "ws-a")
	id := sessionIdentity{"agent-a/ws-a", stub.addr(), "gen-1"}
	absent, err := sessions.Acquire(testCtx(t, 3*time.Second), id.leaseRequest("NoSuchFolder", true, false))
	require.NoError(t, err, "structural absence is a select outcome on a healthy lease")
	require.ErrorIs(t, absent.SelectError, ErrFolderAbsent)
	require.NotErrorIs(t, absent.SelectError, context.DeadlineExceeded)
	absent.Release()
	poolAwaitCount(t, stub.openCount, 1, "absent-folder SELECT did not poison the retained socket")
	valid := poolHold(t, sessions, id, "INBOX", true)
	require.Same(t, absent.Client(), valid.Client())
	rows, err := stubFetchRows(t, valid)
	require.NoError(t, err)
	require.Equal(t, []string{"INBOX-row-1", "INBOX-row-2"}, rows)
	require.Equal(t, 1, stub.acceptCount())
	valid.Release()
}

func poolFacadeForStub(t *testing.T, stub *stubIMAPServer, sessions *MailSessions) *Client {
	t.Helper()
	host, port, err := net.SplitHostPort(stub.addr())
	require.NoError(t, err)
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	client, err := NewClient(Account{IMAPHost: host, IMAPPort: n, SMTPHost: host, Username: testIMAPUser, Password: testIMAPPass})
	require.NoError(t, err)
	client.SetSessionSource(sessions) // W1 §3.1 injection, production API still missing
	return client
}
