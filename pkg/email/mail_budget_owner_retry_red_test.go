package email

// W1 §4.11 / §8 rows 23–24; B-W1-32/33, MC-W1-24/25. Audit hole F3 (partial):
// one-owner-per-operation and retry-bypasses-backoff-only were absent from
// the runtime pack. The W1 lane owns the watcher path — the only gated path
// fully inside pkg/email — and the structural half of MC-W1-24: Acquire
// carries no budget parameter, so a wrapper+client double take is unwritable
// rather than merely forbidden by comment. The REST wrapper leg
// (pkg/gateway/rest_mail_budget.go::mailBudgetWrap) is w5-integration's and
// the tool wrapper leg (pkg/tools/email.go::gateMailDial) is
// w4-features/W10's; their one-owner proof rides their own lanes' tests, not
// this file. Expected values derive from the spec (§4.4's 5 s acquisition
// window, §4.11's retry rule), never from the implementation.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBudget_OneOwnerPerOperation asserts MC-W1-24's two W1-observable
// halves: the frozen Acquire/TryAcquire seam shape (no budget parameter —
// the type-system guarantee) and the watcher path holding exactly one
// account slot for its gated operation.
func TestBudget_OneOwnerPerOperation(t *testing.T) {
	t.Run("acquire_signature_carries_no_budget", func(t *testing.T) {
		// §3.1's frozen shape: Acquire(ctx, LeaseRequest) (Lease, error) —
		// and the watcher's TryAcquire twin. MC-W1-24: "the structural half
		// is that Acquire's signature carries no budget parameter, so a
		// double acquire is unwritable".
		ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
		reqType := reflect.TypeOf(LeaseRequest{})
		budgetType := reflect.TypeOf(&MailBudget{})
		for _, name := range []string{"Acquire", "TryAcquire"} {
			method, ok := reflect.TypeOf(&MailSessions{}).MethodByName(name)
			require.True(t, ok, "%s exists on the published seam", name)
			require.Equal(t, 3, method.Type.NumIn(), "%s(ctx, LeaseRequest): receiver plus exactly two parameters", name)
			require.Equal(t, ctxType, method.Type.In(1), "%s takes the operation's context", name)
			require.Equal(t, reqType, method.Type.In(2), "%s takes the lease request", name)
			for i := 1; i < method.Type.NumIn(); i++ {
				require.NotEqual(t, budgetType, method.Type.In(i),
					"MC-W1-24: %s carries no budget parameter — the session source never acquires an account slot", name)
			}
		}
	})

	t.Run("watcher_cycle_holds_exactly_one_slot", func(t *testing.T) {
		stub, clock := newStubIMAPServer(t), newPoolTestClock()
		// The greeting stall parks the cycle's dial mid-establishment, so
		// the slot TryCall holds stays observable for a deterministic window.
		stub.setMode(stubStallGreeting)
		sessions := newSessionsForStub(t, stub, clock)
		dir := t.TempDir()
		budget := NewMailBudget(dir)
		client := poolFacadeForStub(t, stub, sessions)
		watcher, err := NewWatcher(WatcherConfig{AgentID: "agent-w", WorkspaceID: "ws-w",
			Transport: client, StateDir: dir, Budget: budget, Now: clock.Now})
		require.NoError(t, err)

		probe := swapCountingDial(t)
		slots := budget.gateFor(client.AccountKey()).slots
		done := make(chan error, 1)
		go func() { done <- watcher.cycleIfDue(testCtx(t, time.Second), clock.Now()) }()

		waitFor(t, time.Second, "the gated cycle started its dial",
			func() bool { return probe.starts.Load() == 1 })
		waitFor(t, time.Second, "the watcher operation holds its one account slot during the dial",
			func() bool { return len(slots) == 1 })
		// While the dial is stalled the cycle holds exactly one slot: a
		// second slot take by the same operation is the double-acquire
		// defect MC-W1-24 exists to prevent.
		for i := 0; i < 100; i++ {
			require.Equal(t, 1, len(slots),
				"MC-W1-24: the watcher operation holds exactly one slot — never two, never zero mid-dial")
			time.Sleep(3 * time.Millisecond)
		}
		// End the parked dial inside the test (the stub's own cleanup close
		// is once-guarded) so the cycle's failure recording happens while
		// its state dir still exists.
		stub.close()
		cycleErr := expectResult(t, done, 3*time.Second, "the stalled cycle ends once the peer closes")
		require.Error(t, cycleErr, "the greeting-stalled dial fails visibly when the peer is gone")
		waitFor(t, time.Second, "the account slot is released when the operation ends",
			func() bool { return len(slots) == 0 })
	})
}

// TestBudget_RetryBypassesBackoffOnly asserts MC-W1-25: retry=true bypasses
// ONLY the backoff gate. The control call proves the backoff is real; the
// retried call then waits out the bounded acquisition window behind a
// saturated pool and ends in the typed pool-busy outcome — no dial, no
// ceiling bypass, no backoff refusal.
func TestBudget_RetryBypassesBackoffOnly(t *testing.T) {
	stub, clock := newStubIMAPServer(t), newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)
	dir := t.TempDir()
	budget := NewMailBudget(dir)

	// A real persisted backoff window for the pair — the same watcher state
	// file backoffRefusal reads (§4.4's "Retry bypasses ONLY this").
	pairDir := filepath.Join(dir, "email-watch")
	require.NoError(t, os.MkdirAll(pairDir, 0o700))
	state := fmt.Sprintf(`{"agent_id":"retry-agent","workspace_id":"retry-ws","watcher_state":"error","last_error_class":"timeout","next_attempt_at":%q,"attempt":1}`,
		time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339))
	require.NoError(t, os.WriteFile(filepath.Join(pairDir, keyFor("retry-agent", "retry-ws")+".json"), []byte(state), 0o600))

	req := MailBudgetRequest{Account: fmt.Sprintf("%s|%s", stub.addr(), testIMAPUser),
		AgentID: "retry-agent", WorkspaceID: "retry-ws", Operation: "listMailMessages",
		Params: map[string]any{"folder": "INBOX", "limit": 20.0}, Purpose: "read_live"}

	// Control: without Retry the typed backoff refusal fires and the
	// executor never runs — the gate is genuinely armed, so the Retry half
	// of the scenario is a real bypass, not an untested assumption.
	var backoff *MailBackoffError
	err := budget.Call(testCtx(t, time.Second), req, func(context.Context) error {
		return errors.New("budget: a backoff-refused operation must never run")
	})
	require.ErrorAs(t, err, &backoff, "control: the persisted backoff refuses without Retry")

	// Saturate all eight global sockets with foreground work (§4.2).
	for i := 0; i < 8; i++ {
		poolHold(t, sessions, sessionIdentity{fmt.Sprintf("fg-%d/ws-%d", i, i), stub.addr(), "gen-1"}, "INBOX", false)
	}
	poolAwaitCount(t, stub.openCount, 8, "foreground holds every global socket")

	started := time.Now()
	retryReq := req
	retryReq.Retry = true // the human Retry bypass: §4.4/§4.11 — the backoff gate ONLY
	err = budget.Call(testCtx(t, 15*time.Second), retryReq, func(ctx context.Context) error {
		_, aerr := sessions.Acquire(ctx,
			sessionIdentity{"retry-agent/retry-ws", stub.addr(), "gen-1"}.leaseRequest("INBOX", false, false))
		return aerr
	})
	elapsed := time.Since(started)
	poolBusy(t, err) // the typed pool-busy class — not ErrMailBusy, not a timeout
	require.NotErrorAs(t, err, &backoff,
		"MC-W1-25: Retry bypassed the backoff gate — the outcome is capacity, not backoff")
	require.GreaterOrEqual(t, elapsed, 4*time.Second,
		"§4.4: the bounded 5 s acquisition window was genuinely waited, not skipped")
	require.Equal(t, 8, stub.acceptCount(),
		"MC-W1-25: retry never bypasses the pool ceilings — no ninth dial was attempted")
	require.Equal(t, 8, sessions.OpenSockets(),
		"the refused retry added no socket — exactly the eight foreground holds remain")
}
