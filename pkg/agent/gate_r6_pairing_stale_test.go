package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestPairingAdmission_ClosesAStaleReadyAttemptVisibly is gate r5 R5-A05 (M2,
// M14): a ready attempt whose committed configuration was since replaced can
// never start. The next admission closes it as interrupted — telling its chat
// — and is accepted; an older reload is never the one that decides.
func TestPairingAdmission_ClosesAStaleReadyAttemptVisibly(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	store := pairingAttemptsFor(t, al)
	stale, err := store.Admit("whatsapp", channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: "chat-a", RequestID: "req-a"})
	require.NoError(t, err)
	require.True(t, store.MarkReady("whatsapp", stale.AttemptID, "a-replaced-revision"))
	events := al.SubscribeEvents(16)

	next, err := al.AdmitConnectPairing("whatsapp", "req-b", "chat-b")
	require.NoError(t, err, "the stale attempt no longer blocks a new admission")
	got, ok := store.Attempt("whatsapp", stale.AttemptID)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeInterrupted, got.Outcome)
	frame := pairingEvents(t, events, "whatsapp", channels.PairingStatusError)
	last := frame[len(frame)-1]
	require.Equal(t, "req-a", last.RequestID, "the stale attempt's chat is told")
	require.Equal(t, channels.PairingInterruptedMessage, last.Message)
	latest, _ := store.Latest("whatsapp")
	require.Equal(t, next, latest.AttemptID)
}

// TestSettlePairing_ClosesOnlyAnUnstartedAttemptVisibly is gate r5 R5-A05's
// settle path: after its own reload was confirmed, an admission no start
// consumed is closed and its chat told; an attempt the start took is left.
func TestSettlePairing_ClosesOnlyAnUnstartedAttemptVisibly(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	store := pairingAttemptsFor(t, al)
	started, _ := startOwnedAttempt(t, store, "whatsapp.started", "chat-s", "req-s")
	al.SettlePairing("whatsapp.started", started)
	require.True(t, store.IsCurrent("whatsapp.started", started), "a started attempt is left to its start")

	unstarted, err := store.Admit("whatsapp", channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: "chat-a", RequestID: "req-a"})
	require.NoError(t, err)
	require.True(t, store.MarkReady("whatsapp", unstarted.AttemptID, "rev"))
	events := al.SubscribeEvents(16)
	al.SettlePairing("whatsapp", unstarted.AttemptID)
	got, ok := store.Attempt("whatsapp", unstarted.AttemptID)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeInterrupted, got.Outcome)
	frames := pairingEvents(t, events, "whatsapp", channels.PairingStatusError)
	require.Equal(t, "req-a", frames[len(frames)-1].RequestID)
	require.Equal(t, channels.PairingInterruptedMessage, frames[len(frames)-1].Message)
}
