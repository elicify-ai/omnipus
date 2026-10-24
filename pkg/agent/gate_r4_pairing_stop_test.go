package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// startOwnedWith admits, commits and activates an attempt of instance owned
// by (sessionID, requestID) with the given teardown.
func startOwnedWith(t *testing.T, store *channels.PairingAttempts, instance, sessionID, requestID string,
	teardown func(context.Context) error,
) string {
	t.Helper()
	admitted, err := store.Admit(instance, channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: sessionID, RequestID: requestID})
	require.NoError(t, err)
	require.True(t, store.MarkReady(instance, admitted.AttemptID, "rev"))
	_, err = store.Activate(instance, admitted.AttemptID, func(string) func(context.Context) error { return teardown })
	require.NoError(t, err)
	return admitted.AttemptID
}

func pairingEvents(t *testing.T, sub EventSubscription, channel string, until channels.PairingStatus) []WhatsAppPairingPayload {
	t.Helper()
	var out []WhatsAppPairingPayload
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-sub.C:
			p, ok := evt.Payload.(WhatsAppPairingPayload)
			if !ok || p.ChannelID != channel {
				continue
			}
			out = append(out, p)
			if p.Status == until {
				return out
			}
		case <-deadline:
			t.Fatalf("no %q pairing event for %s; got %+v", until, channel, out)
		}
	}
}

// TestStopScope_StopAllEndsACompletedHelpersPairing is gate r4 R4-A04 /
// security T2-R4-S2 / CR-T2-R3-03 (M9): a helper started an owned pairing and
// its model turn COMPLETED while the phone was still unpaired. The parent's
// Stop all ends exactly that attempt with the helper's ids; a plain Stop of
// the parent does not; an unrelated chat's pairing survives.
func TestStopScope_StopAllEndsACompletedHelpersPairing(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	store := pairingAttemptsFor(t, al)
	root := rootLaunchWithGoal(t, al)
	helper, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: root.SessionID, TargetAgentID: testDefaultAgentID, Task: "connect WhatsApp",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "helper-pairing-done"},
	})
	require.NoError(t, err)
	lifecycle := al.GetSessionLifecycleStore()
	rec, err := lifecycle.Load(helper.SessionID)
	require.NoError(t, err)
	rec.State = session.LifecycleCompleted
	require.NoError(t, lifecycle.Persist(rec))
	done, err := lifecycle.Load(helper.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, done.State, "instrument: the helper's turn completed")

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
	requireTornDown(t, helperTorn, helperAttempt, false, "a plain Stop of the parent does not reach a helper's pairing")
	stop(true)
	requireTornDown(t, helperTorn, helperAttempt, true, "Stop all ends a completed helper's pairing")
	requireTornDown(t, unrelatedTorn, unrelated, false, "an unrelated chat's pairing survives")
	stopped := pairingEvents(t, events, "whatsapp.helper", channels.PairingStatusStopped)
	last := stopped[len(stopped)-1]
	require.Equal(t, "req-helper", last.RequestID)
	require.Equal(t, helper.SessionID, last.SessionID)
}

// selectionRacer runs before the durable Stop is stamped and its turn
// callbacks run — after StopSession captured its selection.
type selectionRacer struct {
	*SteerCanceller
	before func()
}

func (r selectionRacer) StopTurnsWithCause(ctx context.Context, sessionID string, by steer.Principal, subtree bool,
	cause session.StopCause, stopTurn GenerationCancelFunc,
) (steer.CancelReport, error) {
	r.before()
	return r.SteerCanceller.StopTurnsWithCause(ctx, sessionID, by, subtree, cause, stopTurn)
}

// TestStopScope_ALaterRetryIsNeverAddedToAnEarlierStop is gate r4 security
// T2-R3-S2(a) (M7): a Stop's selection is frozen at its admission. A expires
// and the same chat's explicit Retry starts B — same session and request ids —
// after the selection but before the Stop's turn callback runs. B is new
// intent: it survives.
func TestStopScope_ALaterRetryIsNeverAddedToAnEarlierStop(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	store := pairingAttemptsFor(t, al)
	root := rootLaunchWithGoal(t, al)
	a, _ := startOwnedAttempt(t, store, "whatsapp", root.SessionID, "req-root")

	var b string
	var bTorn atomic.Bool
	racer := selectionRacer{SteerCanceller: al.steerCanceller(), before: func() {
		_, ended := store.Complete("whatsapp", a, channels.PairingOutcomeTimeout)
		require.True(t, ended)
		retry, err := store.Admit("whatsapp", channels.PairingAdmissionRetry,
			&channels.PairingOwner{SessionID: root.SessionID, RequestID: "req-root"})
		require.NoError(t, err)
		require.True(t, store.MarkReady("whatsapp", retry.AttemptID, "rev"))
		_, err = store.Activate("whatsapp", retry.AttemptID, func(string) func(context.Context) error {
			return func(context.Context) error { bTorn.Store(true); return nil }
		})
		require.NoError(t, err)
		b = retry.AttemptID
	}}
	_, err := al.StopSession(context.Background(), StopRequest{
		SessionID: root.SessionID, Channel: "webchat", Canceller: racer,
		By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "root-owner"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, b, "instrument: the Retry started inside the window")
	require.False(t, bTorn.Load(), "the earlier Stop never reaches the later Retry")
	require.True(t, store.IsCurrent("whatsapp", b), "B keeps running")
}

// TestStopPairing_RevokedResultNeverOverwritesASuccessor is gate r4 R4-A03 /
// CR-T2-R4-02 / security T2-R3-S2(b) (M8, M13): Stop revokes A before it
// started; in the window before A's stopped frame is published, the same chat
// Retries and B (same ids, different attempt) publishes its code. A's stopped
// frame is not published over B — B's code is the last word — while A's
// audit row still records A's own stop.
func TestStopPairing_RevokedResultNeverOverwritesASuccessor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(auditBootTestConfig(), msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })
	require.NotNil(t, al.AuditLogger(), "instrument: a real audit logger")
	store := pairingAttemptsFor(t, al)
	owner := &channels.PairingOwner{SessionID: "chat-a", RequestID: "req-a"}
	a, err := store.Admit("whatsapp", channels.PairingAdmissionConnect, owner)
	require.NoError(t, err)
	events := al.SubscribeEvents(64)
	var b string
	al.pairingRevokeHook = func() {
		retry, admitErr := store.Admit("whatsapp", channels.PairingAdmissionRetry, owner)
		require.NoError(t, admitErr)
		require.True(t, store.MarkReady("whatsapp", retry.AttemptID, "rev"))
		_, activateErr := store.Activate("whatsapp", retry.AttemptID, func(string) func(context.Context) error { return nil })
		require.NoError(t, activateErr)
		b = retry.AttemptID
		al.EmitWhatsAppPairingAttempt("whatsapp", b, channels.PairingStatusCode, "QR-B", "")
	}
	al.stopSelectedPairings(store.OwnedBy("chat-a"))
	al.pairingRevokeHook = nil
	require.NotEmpty(t, b, "instrument: B published inside the window")
	al.EmitWhatsAppPairingAttempt("whatsapp", b, channels.PairingStatusTimeout, "", "expired") // a closing marker

	got := pairingEvents(t, events, "whatsapp", channels.PairingStatusTimeout)
	for i, p := range got {
		require.NotEqual(t, channels.PairingStatusStopped, p.Status, "event %d: A's stopped result must not follow B's code", i)
	}
	require.Equal(t, "QR-B", got[0].QR)
	require.Equal(t, "req-a", got[0].RequestID)

	raw, err := os.ReadFile(filepath.Join(home, "system", "audit.jsonl"))
	require.NoError(t, err)
	found := false
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		var entry map[string]any
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry["event"] != "channel.pairing" {
			continue
		}
		details, _ := entry["details"].(map[string]any)
		if details["attempt_id"] == a.AttemptID && details["status"] == "stopped" {
			found = true
		}
	}
	require.True(t, found, "A's own stop is still audited (M13)")
}

// TestStopPairing_AFailedTeardownIsVisibleAndRetried is gate r4 T2-SF-14 (M7):
// the selected attempt's teardown fails. Its chat is told at once (no
// fabricated stopped), the attempt stays stopping, the teardown is retried on
// that attempt's own handle, and once it succeeds the attempt ends stopped
// with its stopped frame.
func TestStopPairing_AFailedTeardownIsVisibleAndRetried(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	store := pairingAttemptsFor(t, al)
	prev := pairingStopRetryDelays
	pairingStopRetryDelays = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() { pairingStopRetryDelays = prev })
	var calls atomic.Int32
	attempt := startOwnedWith(t, store, "whatsapp", "chat-a", "req-a", func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("the WhatsApp client did not finish stopping: context deadline exceeded")
		}
		return nil
	})
	events := al.SubscribeEvents(64)
	al.stopSelectedPairings(store.OwnedBy("chat-a"))

	got := pairingEvents(t, events, "whatsapp", channels.PairingStatusStopped)
	require.Equal(t, channels.PairingStatusError, got[0].Status, "the failure is shown first")
	require.Equal(t, "Stopping this pairing has not finished yet. Omnipus keeps trying; "+
		"a new pairing can start once it has stopped.", got[0].Message)
	require.Equal(t, "req-a", got[0].RequestID)
	require.Equal(t, channels.PairingStatusStopped, got[len(got)-1].Status)
	require.Equal(t, int32(2), calls.Load(), "the retry ran that attempt's own handle")
	rec, ok := store.Attempt("whatsapp", attempt)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeStopped, rec.Outcome)
}
