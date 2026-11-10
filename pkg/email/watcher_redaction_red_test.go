package email

// W1 §4.12 / §8 T29; B-W1-35, MC-W1-27, US-8/AS-6, CX-13 (grill-1 C-1).
// Hole F1 of the CHECK mutation audit (omnipus-uat/receipts/check-runtime-
// report.md): mutant M12 — persisting err.Error() into the watcher state —
// survived the whole pack because no test asserted the redaction obligation.
// This test drives a REAL cycle (Cycle → classifyMailError → recordFailure →
// save) so the persisted disk state is the oracle. The negative assertion
// (raw provider text absent from the whole file) is paired with positive
// controls (safe class, failure count, future next-attempt all present), so
// it cannot pass by recording nothing. Expected values derive from the spec,
// never from the implementation. Mutation-kill proof (M12 dies) is the
// separate CHECK instance's deferred probe, not claimed here.

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"
)

// redactionRawProviderError is the scripted provider failure (B-W1-35's
// given): a raw error embedding a hostname, a folder name and an account
// prefix. Its lowercase text hits classifyMailError's documented auth rule
// ("auth") and none of the earlier rules (no "dns"/"resolve"/"no such host",
// not a net.Error, no ECONNREFUSED), so the closed class is deterministically
// auth_failed (MC-8 enum; §4.12 "the closed class from classifyMailError").
const redactionRawProviderError = "imap LOGIN failed: [AUTHENTICATIONFAILED] invalid credentials for " +
	"acct-piper@imap.hostile-mail.example.net — folder INBOX/Sent-Items-2026 unreachable on " +
	"host imap.hostile-mail.example.net:993"

func TestWatcher_RecordFailurePersistsOnlySafeClass(t *testing.T) {
	stub, clock := newStubIMAPServer(t), newPoolTestClock()
	sessions := newSessionsForStub(t, stub, clock)
	watcher, dir := newWatcherOverPool(t, stub, sessions, clock)

	// Scripted raw failure at the dial seam (the sanctioned test-owned var):
	// the cycle's probe fails with the marker-laden provider text.
	rawErr := errors.New(redactionRawProviderError)
	prev := imapDial
	imapDial = func(context.Context, string, *tls.Config) (*imapclient.Client, error) {
		return nil, rawErr
	}
	t.Cleanup(func() { imapDial = prev })

	now := clock.Now()
	err := watcher.cycleIfDue(testCtx(t, 3*time.Second), now)
	require.Error(t, err, "the scripted provider failure must surface, not vanish")
	require.ErrorIs(t, err, rawErr)
	require.Zero(t, stub.acceptCount(), "the failed dial never reached the server")
	poolAwaitCount(t, stub.openCount, 0, "no socket lingers after the failed probe")

	// Positive control — the failure WAS recorded, as the safe class plus
	// safe metadata only (§4.12: class, next-attempt time, failure count).
	state := watcherStateForPoolTest(t, dir)
	require.Equal(t, "error", state.State, "the cycle is recorded as failed")
	require.Equal(t, "auth_failed", state.LastErrorClass, "the persisted class is the closed enum value")
	require.Empty(t, state.LastErrorText, "FR-W1-23: the raw-text field stays empty")
	require.Equal(t, 1, state.Attempt, "the consecutive-failure count is safe metadata and is recorded")
	require.NotEmpty(t, state.NextAttemptAt, "the next-attempt time is safe metadata and is recorded")
	next, perr := time.Parse(time.RFC3339, state.NextAttemptAt)
	require.NoError(t, perr)
	// auth_failed goes straight to the 15-min backoff cap (§4.10) with the
	// documented ±20% jitter (§2.1: factor 0.8–1.2) — the next attempt sits
	// 12–18 minutes out from the failure's clock reading.
	delta := next.Sub(now)
	require.GreaterOrEqual(t, delta, 12*time.Minute, "auth_failed caps at 15 min, jitter no lower than 0.8×")
	require.LessOrEqual(t, delta, 18*time.Minute, "auth_failed caps at 15 min, jitter no higher than 1.2×")
	require.Equal(t, "backoff", state.EffectiveState(now.Add(time.Minute)),
		"failed + future next attempt renders as backoff (the ONE derivation point)")

	// The negative assertion MC-W1-27 exists for: the raw provider text
	// appears NOWHERE in the persisted file bytes. The whole file is scanned,
	// not one field — a raw fragment in any key or value is exactly the
	// durable-disclosure defect §4.12 exists to prevent.
	path := filepath.Join(dir, "email-watch", keyFor("agent-w", "ws-w")+".json")
	b, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	for _, marker := range []string{
		redactionRawProviderError,
		"AUTHENTICATIONFAILED",
		"acct-piper",
		"imap.hostile-mail.example.net",
		"INBOX/Sent-Items-2026",
	} {
		require.NotContains(t, string(b), marker,
			"§4.12/MC-W1-27: raw provider text must never persist to the watcher state file")
	}
	require.Contains(t, string(b), `"last_error_class":"auth_failed"`,
		"the safe classified representation is what the file carries")
}
