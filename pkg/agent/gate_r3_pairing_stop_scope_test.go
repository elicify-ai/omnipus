package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// pairingAttemptsFor wires a real channel manager (and so a real attempt
// store) into al.
func pairingAttemptsFor(t *testing.T, al *AgentLoop) *channels.PairingAttempts {
	t.Helper()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	mgr, err := channels.NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	require.NoError(t, err)
	al.SetChannelManager(mgr)
	return mgr.PairingAttempts()
}

// startOwnedAttempt admits, commits and activates an attempt of instance owned
// by sessionID; the returned channel receives the attempt id when its own
// teardown handle runs.
func startOwnedAttempt(t *testing.T, store *channels.PairingAttempts, instance, sessionID, requestID string) (string, chan string) {
	t.Helper()
	torn := make(chan string, 2)
	admitted, err := store.Admit(instance, channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: sessionID, RequestID: requestID})
	require.NoError(t, err)
	require.True(t, store.MarkReady(instance, admitted.AttemptID, "rev"))
	active, err := store.Activate(instance, store.TakeReady(instance, "rev"), func(attemptID string) func(context.Context) error {
		return func(context.Context) error {
			torn <- attemptID
			return nil
		}
	})
	require.NoError(t, err)
	return active.AttemptID, torn
}

// startUnownedAttempt admits an ordinary (Connectors) enable of instance,
// commits it and activates it: an unowned attempt started with its own token.
func startUnownedAttempt(t *testing.T, store *channels.PairingAttempts, instance string, newCancel func(string) func(context.Context) error) channels.PairingAttempt {
	t.Helper()
	admitted, err := store.Admit(instance, channels.PairingAdmissionEnable, nil)
	require.NoError(t, err)
	require.True(t, store.MarkReady(instance, admitted.AttemptID, "rev"))
	active, err := store.Activate(instance, admitted.AttemptID, newCancel)
	require.NoError(t, err)
	return active
}

func requireTornDown(t *testing.T, torn chan string, attemptID string, want bool, msg string) {
	t.Helper()
	select {
	case got := <-torn:
		require.True(t, want, msg)
		require.Equal(t, attemptID, got)
	case <-time.After(500 * time.Millisecond):
		require.False(t, want, msg)
	}
}

// TestStopScope_StopAllEndsAHelpersPairingPlainStopDoesNot is gate r3
// CR-T2-R3-03 (M9): a helper that started an owned QR pairing is reached by
// its parent's Stop-all, which tears down exactly that attempt with the
// helper's own ids; a plain Stop of the parent cleans only the parent's own
// attempts; an unrelated chat's attempt survives both.
func TestStopScope_StopAllEndsAHelpersPairingPlainStopDoesNot(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	store := pairingAttemptsFor(t, al)
	root := rootLaunchWithGoal(t, al)
	helper, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: root.SessionID, TargetAgentID: testDefaultAgentID, Task: "connect WhatsApp",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "helper-pairing"},
	})
	require.NoError(t, err)

	rootAttempt, rootTorn := startOwnedAttempt(t, store, "whatsapp.root", root.SessionID, "req-root")
	helperAttempt, helperTorn := startOwnedAttempt(t, store, "whatsapp.helper", helper.SessionID, "req-helper")
	unrelated, unrelatedTorn := startOwnedAttempt(t, store, "whatsapp.other", "unrelated-chat", "req-other")
	events := al.SubscribeEvents(64)

	stop := func(tree bool) {
		_, err := al.StopSession(context.Background(), StopRequest{
			SessionID: root.SessionID, Tree: tree, Channel: "webchat",
			By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "root-owner"},
		})
		require.NoError(t, err)
	}
	stop(false)
	requireTornDown(t, rootTorn, rootAttempt, true, "a plain Stop ends the requesting session's own pairing")
	requireTornDown(t, helperTorn, helperAttempt, false, "a plain Stop must not reach a helper's pairing")

	stop(true)
	requireTornDown(t, helperTorn, helperAttempt, true, "Stop-all must end the helper's pairing")
	requireTornDown(t, unrelatedTorn, unrelated, false, "an unrelated chat's pairing survives")
	require.True(t, store.IsCurrent("whatsapp.other", unrelated))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-events.C:
			p, ok := evt.Payload.(WhatsAppPairingPayload)
			if !ok || p.ChannelID != "whatsapp.helper" || p.Status != channels.PairingStatusStopped {
				continue
			}
			require.Equal(t, "req-helper", p.RequestID, "the stopped frame names the helper's own request")
			require.Equal(t, helper.SessionID, p.SessionID)
			return
		case <-deadline:
			t.Fatal("no stopped frame for the helper's pairing")
		}
	}
}
