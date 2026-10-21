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

// TestManager_StaleAdmissionIsReportedInterrupted is the gate r4 sibling of
// T2-SF-16 (M14): an admission whose configuration changed after it committed
// never starts, and its chat is told it was interrupted.
func TestManager_StaleAdmissionIsReportedInterrupted(t *testing.T) {
	registerFailOnceFactory(t, "gate-r4-stale", 0)
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r4-stale": {Type: "gate-r4-stale", Enabled: true},
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
	store := m.PairingAttempts()
	stale, err := store.Admit("gate-r4-stale", PairingAdmissionConnect, &PairingOwner{SessionID: "chat-a", RequestID: "req-a"})
	require.NoError(t, err)
	require.True(t, store.MarkReady("gate-r4-stale", stale.AttemptID, "an-older-revision"))
	m.RequestRestart("gate-r4-stale", stale.AttemptID)
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []pairingEvent{{"gate-r4-stale", stale.AttemptID, PairingStatusError,
		"This pairing was interrupted because the WhatsApp connection was changed or restarted. Press Retry to get a new code."}}, events)
}
