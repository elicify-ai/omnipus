//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestStart_GatewayRestartInterruptsAnUnpairedPairing is the founder's restart
// rule (2026-10-21, replaces gate r3 M11; T4). The attempt store lives in
// memory, so a gateway restart loses every attempt and its owner. The boot
// start of an enabled connector that is not yet paired (an empty local
// session store) does not resume a pairing and infers no owner: it records an
// interrupted, unowned attempt, tells the chat card and Connectors with the
// ruled text and no ids, and does not show a code. A Retry from any chat then
// starts a fresh attempt owned by that chat.
func TestStart_GatewayRestartInterruptsAnUnpairedPairing(t *testing.T) {
	ch, err := NewWhatsAppNativeChannel(config.WhatsAppConfig{Enabled: true}, bus.NewMessageBus(), t.TempDir())
	require.NoError(t, err)
	native, ok := ch.(*WhatsAppNativeChannel)
	require.True(t, ok, "unexpected concrete type")
	store := channels.NewPairingAttempts()
	native.SetPairingAttempts(store, "whatsapp")
	type frame struct {
		attemptID   string
		status      channels.PairingStatus
		qr, message string
	}
	var frames []frame
	native.SetPairingAttemptObserver(func(attemptID string, status channels.PairingStatus, qr, message string) {
		frames = append(frames, frame{attemptID, status, qr, message})
	})

	require.NoError(t, native.Start(channels.WithPairingStart(context.Background(), "", true)))
	t.Cleanup(func() { _ = native.Stop(context.Background()) })

	latest, found := store.Latest("whatsapp")
	require.True(t, found, "the boot start recorded the interrupted pairing")
	require.Equal(t, channels.PairingPhaseTerminal, latest.Phase)
	require.Equal(t, channels.PairingOutcomeInterrupted, latest.Outcome)
	require.True(t, latest.RestartInterrupted)
	require.Nil(t, latest.Owner, "no ownership survives a restart or is inferred")
	require.Equal(t, []frame{{latest.AttemptID, channels.PairingStatusError, "",
		"A server restart interrupted this pairing. Press Retry to get a new code."}}, frames,
		"one visible error with the ruled text, and no code")
	require.False(t, native.IsRunning(), "nothing resumed")

	retry, err := store.Admit("whatsapp", channels.PairingAdmissionRetry,
		&channels.PairingOwner{SessionID: "chat-b", RequestID: "req-b"})
	require.NoError(t, err, "a chat's Retry is admitted after the restart")
	require.NotEqual(t, latest.AttemptID, retry.AttemptID, "as a fresh attempt")
	require.Equal(t, &channels.PairingOwner{SessionID: "chat-b", RequestID: "req-b"}, retry.Owner)
}
