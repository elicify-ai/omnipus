package email

// W1 §4.10.3 / §8 row 21's W1-observable scope; US-8/AS-4, B-W1-30,
// MC-W1-23, CX-9. Hole F2 of the CHECK mutation audit (omnipus-uat/receipts/
// check-runtime-report.md): mutant M9 — Retain:true on the watcher's STATUS
// lease request — survived the pack because TestWatcher_CycleNeverRetainsSocket
// only exercises modes where the socket closes for the WRONG reason
// (retention unwired; another workspace's panel). This is the missing mode:
// retention ENABLED and an observer bound to the watcher's OWN workspace.
// §4.10.3: a watcher cycle may share a healthy idle socket while a panel is
// open, but never causes retention. The assertion is the retained-socket
// count after each cycle (pool counter and server-side open count), not
// merely that a close happened. The trailing positive control proves the
// armed machinery DOES retain eligible (panel-class) work, so the cycle
// assertions cannot pass with retention silently inert. Mutation-kill proof
// (M9 dies) is the separate CHECK instance's deferred probe, not claimed
// here. Dirty-mark consumers, W2 cache files and scheduler fairness remain
// other lanes' work.

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// facadeWithScope mirrors the pack's poolFacadeForStub and adds the
// production construction shape (§4.1): the facade carries its pair key and
// generation, so the lease's presence workspace is the watcher's own rather
// than the endpoint-only identity's empty pair.
func facadeWithScope(t *testing.T, stub *stubIMAPServer, sessions *MailSessions, pair, generation string) *Client {
	t.Helper()
	host, port, err := net.SplitHostPort(stub.addr())
	require.NoError(t, err)
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	client, err := NewClient(Account{IMAPHost: host, IMAPPort: n, SMTPHost: host, Username: testIMAPUser, Password: testIMAPPass})
	require.NoError(t, err)
	client.SetSessionSource(sessions) // W1 §3.1 injection
	client.SetSessionScope(pair, generation)
	return client
}

func TestWatcher_CycleOwnWorkspacePanelOpen_NeverRetains(t *testing.T) {
	stub, clock := newStubIMAPServer(t), newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)

	client := facadeWithScope(t, stub, sessions, "agent-w/ws-w", "gen-1")
	dir := t.TempDir()
	watcher, err := NewWatcher(WatcherConfig{AgentID: "agent-w", WorkspaceID: "ws-w",
		Transport: client, StateDir: dir, Budget: NewMailBudget(dir), Now: clock.Now})
	require.NoError(t, err)

	// Arm retention exactly as the production integration activation does,
	// for the watcher's OWN workspace — the mode the existing test cannot
	// express and the only one that can catch a watcher Retain:true.
	sessions.EnableRetention()
	sessions.Presence().Bind("authenticated-test-conn", "panel-ws-w", "ws-w")
	require.True(t, sessions.RetentionEnabled(), "retention genuinely armed for this test")
	require.Equal(t, 1, sessions.Presence().Count("ws-w"), "the watcher's own workspace has a live observer")

	for cycle := 1; cycle <= 3; cycle++ {
		clock.advance(time.Minute)
		require.NoError(t, watcher.cycleIfDue(testCtx(t, 3*time.Second), clock.Now()))
		poolAwaitCount(t, stub.openCount, 0, "the cycle's socket is released even with its own panel open")
		require.Zero(t, sessions.OpenSockets(),
			"§4.10.3: watcher work never causes retention — the retained-socket count stays 0")
		require.Equal(t, cycle, stub.acceptCount(),
			"each cycle dials its own request-scoped session (nothing retained to reuse)")
		state := watcherStateForPoolTest(t, dir)
		require.Equal(t, clock.Now().Format(time.RFC3339), state.LastSuccessAt, "the cycle was executed, not just called")
	}

	entries := stub.logEntries()
	require.Equal(t, 3, countStr(entries, " STATUS "), "the one-STATUS probe shape is unchanged in this mode")
	require.Zero(t, countStr(entries, " SELECT "), "STATUS-style watcher lease takes no selected state")
	require.Zero(t, countStr(entries, " STORE "), "the watcher still never mutates flags")

	// Positive control — the retention machinery IS armed and functioning:
	// an eligible (panel-class) lease on the same workspace retains after
	// release. Without this, the cycle assertions above could pass with
	// retention silently inert (closing everything for the wrong reason).
	panel := poolHold(t, sessions, sessionIdentity{"agent-w/ws-w", stub.addr(), "gen-1"}, "INBOX", true)
	panel.Release()
	require.Equal(t, 1, sessions.OpenSockets(), "eligible panel work IS retained — the arming was real")
	require.Equal(t, 1, stub.openCount(), "the retained panel socket is genuinely open server-side")
}
