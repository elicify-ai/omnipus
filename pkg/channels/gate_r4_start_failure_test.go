package channels

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

type pairingEvent struct {
	channel, attempt string
	status           PairingStatus
	message          string
}

// TestManager_FailedPairingStartIsReportedToItsAttempt is gate r4 T2-SF-16:
// an admitted pairing whose start fails ends start_failed and its chat is told
// why at once — an error event for exactly that attempt with a value-free
// cause and repair — instead of waiting for a code that cannot arrive.
func TestManager_FailedPairingStartIsReportedToItsAttempt(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r4-failstart", 2)
	registerHealthyFactory(t, "gate-r4-healthy")
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r4-failstart": {Type: "gate-r4-failstart", Enabled: true},
		"gate-r4-healthy":   {Type: "gate-r4-healthy", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	var mu sync.Mutex
	var events []pairingEvent
	m.SetPairingAttemptObserver(func(channel, attempt string, status PairingStatus, _ string, message string) {
		mu.Lock()
		events = append(events, pairingEvent{channel, attempt, status, message})
		mu.Unlock()
	})
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	require.False(t, built()[0].IsRunning(), "instrument: the boot start failed")

	store := m.PairingAttempts()
	admitted, err := store.Admit("gate-r4-failstart", PairingAdmissionConnect, &PairingOwner{SessionID: "chat-a", RequestID: "req-a"})
	require.NoError(t, err)
	require.True(t, store.MarkReady("gate-r4-failstart", admitted.AttemptID, ChannelRevision(cfg, "gate-r4-failstart")))
	// Fixture: the direct ready admission must carry its accepted config identity.
	finalManagerAcceptedSnapshot(t, store, cfg, "gate-r4-failstart", admitted.AttemptID)
	m.RequestRestart("gate-r4-failstart", admitted.AttemptID)
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))

	got, ok := store.Attempt("gate-r4-failstart", admitted.AttemptID)
	require.True(t, ok)
	require.Equal(t, PairingOutcomeStartFailed, got.Outcome)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []pairingEvent{{"gate-r4-failstart", admitted.AttemptID, PairingStatusError,
		"The WhatsApp pairing could not start. Details are in the server log. Press Retry to try again."}}, events,
		"exactly one error for that attempt, with the cause class and repair")
}

// TestPairingStartFailureMessage_NamesTheCauseClass pins the three value-free
// causes: local session data, the network, anything else. No raw error text.
func TestPairingStartFailureMessage_NamesTheCauseClass(t *testing.T) {
	local := fmt.Errorf("open whatsapp store: %w: %w", ErrLocalState, errors.New("open /private/x.db: permission denied"))
	require.Equal(t, "WhatsApp could not open its local session data. "+
		"Check that the Omnipus data directory is readable and writable, then press Retry.", PairingStartFailureMessage(local))
	require.Equal(t, "WhatsApp could not be reached. Check the network connection, then press Retry.",
		PairingStartFailureMessage(fmt.Errorf("connect: %w", context.DeadlineExceeded)))
	other := PairingStartFailureMessage(errors.New(`server said {"error":"banned"}`))
	require.Equal(t, "The WhatsApp pairing could not start. Details are in the server log. Press Retry to try again.", other)
	require.NotContains(t, other, "banned")
}

// TestManager_AnOlderReloadNeverInterruptsANewerAttempt is gate r5 R5-A05
// (M2, M4, M8, M10): a reload built from an older configuration snapshot
// (rA) starts after A was stopped and a newer connect B committed at its own
// revision (rB). That older reload neither consumes nor interrupts B — it is
// not B's operation; B stays startable and B's own reload starts it. (Gate r4
// sibling, re-aimed: a stale admission is no longer interrupted by an older
// reload; it is closed by its own operation or by the next admission.)
func TestManager_AnOlderReloadNeverInterruptsANewerAttempt(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r5-older", 0)
	cfgA := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r5-older": {Type: "gate-r5-older", Enabled: true, WorkspaceID: "ws-a"},
	})
	cfgB := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r5-older": {Type: "gate-r5-older", Enabled: true, WorkspaceID: "ws-b"},
	})
	require.NotEqual(t, ChannelRevision(cfgA, "gate-r5-older"), ChannelRevision(cfgB, "gate-r5-older"), "instrument: two revisions")
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfgA, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	var mu sync.Mutex
	var events []pairingEvent
	m.SetPairingAttemptObserver(func(channel, attempt string, status PairingStatus, _ string, message string) {
		mu.Lock()
		events = append(events, pairingEvent{channel, attempt, status, message})
		mu.Unlock()
	})
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	store := m.PairingAttempts()

	a, err := store.Admit("gate-r5-older", PairingAdmissionConnect, &PairingOwner{SessionID: "chat-a", RequestID: "req-a"})
	require.NoError(t, err)
	require.True(t, store.MarkReady("gate-r5-older", a.AttemptID, ChannelRevision(cfgA, "gate-r5-older")))
	// Fixture: A's direct accepted write belongs to A's snapshot, never B's.
	finalManagerAcceptedSnapshot(t, store, cfgA, "gate-r5-older", a.AttemptID)
	_, revoked := store.BeginStop(a)
	require.True(t, revoked, "Stop revoked A")
	// Fixture: start A's valid cancelled/idle predecessor before B is accepted;
	// an obsolete reload can then preserve that real idle trace without starting.
	m.RequestRestart("gate-r5-older", "")
	require.NoError(t, m.Reload(context.Background(), cfgA, credentials.SecretBundle{}))
	idleCount := len(built())
	b, err := store.Admit("gate-r5-older", PairingAdmissionConnect, &PairingOwner{SessionID: "chat-b", RequestID: "req-b"})
	require.NoError(t, err)
	require.True(t, store.MarkReady("gate-r5-older", b.AttemptID, ChannelRevision(cfgB, "gate-r5-older")))
	// Fixture: B's distinct accepted write is carried only by B's snapshot.
	finalManagerAcceptedSnapshot(t, store, cfgB, "gate-r5-older", b.AttemptID)

	m.RequestRestart("gate-r5-older", "") // the older operation's restart
	require.NoError(t, m.Reload(context.Background(), cfgA, credentials.SecretBundle{}))
	all := built()
	require.Len(t, all, idleCount, "the obsolete snapshot creates no resource over the accepted winner")
	require.Equal(t, []startIntent{{}}, all[len(all)-1].startIntents(), "the older reload starts without a token")
	require.True(t, store.IsReady("gate-r5-older", b.AttemptID), "B is neither consumed nor interrupted")
	mu.Lock()
	require.Empty(t, events, "nothing is reported for B")
	mu.Unlock()

	require.NoError(t, m.Reload(context.Background(), cfgB, credentials.SecretBundle{}))
	all = built()
	require.Equal(t, []startIntent{{token: b.AttemptID}}, all[len(all)-1].startIntents(), "B's own reload starts B")
}
