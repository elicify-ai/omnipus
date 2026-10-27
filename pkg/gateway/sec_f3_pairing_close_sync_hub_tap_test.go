package gateway

// F3 (security re-review of 6d613e9e0, 2026-10-25): the F1 fix runs the
// stale-ready decision, the interruption and the successor's reservation under
// ONE held channel ordering mutex, and emits the interrupted attempt's notice
// from inside that boundary. With the gateway's REAL WSHandler constructed as
// in production, event publication is synchronous work on the emitting
// goroutine: hubSyncTap → hubWhatsAppPairing → cachePairing →
// lockPairingDelivery → LockExistingChannel — the SAME connector's ordering
// mutex. An eligible stale-ready admission therefore deadlocks before
// reserving its successor, and later operations on the connector wait behind
// it. The fix's claim that the emission "never blocks and never re-enters a
// pairing lock" is the claim this regression pins the opposite of.
//
// The detector is bounded: the admission plus a follow-up operation on the
// same connector run on their own goroutine and must finish within the bound;
// the timeout IS the deadlock finding. No sleeps pace the path itself — the
// bound exists only so a deadlock fails the test instead of hanging it.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestPairingAdmission_EligibleStaleReadyCloseSurvivesTheSyncHubTap(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Sandbox: config.OmnipusSandboxConfig{AutoApprove: false},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	mgr, err := channels.NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	require.NoError(t, err)
	al.SetChannelManager(mgr)
	store := mgr.PairingAttempts()

	// The gateway's REAL synchronous hub tap — installed by constructing the
	// WSHandler exactly as production does, not a mere event subscriber.
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)

	// Chief ruling C+D (2026-10-26): the admission-time close fires ONLY when
	// the served config no longer contains the connector; present-but-different
	// ⇒ refuse (that state is F1's, pinned by
	// pkg/agent/sec_f1_pairing_stale_snapshot_test.go). The served config here
	// therefore does NOT contain the connector — the gate r6 retirement shape
	// gate_r6_pairing_stale_test.go uses — so this fixture's stale-ready
	// attempt is closed by an eligible admission and the detector below guards
	// exactly that close against the self-deadlock and the lost notice.

	// The event subscription captures the notice's own envelope — its
	// generation and sequence are what AcceptsProgress validates on any later
	// delivery attempt.
	events := al.SubscribeEvents(64)

	// An eligible stale-ready attempt: readied at a revision the served
	// configuration has since replaced (the gate r6 shape — the next
	// admission closes it, tells its chat, and is accepted).
	stale, err := store.Admit("whatsapp", channels.PairingAdmissionConnect,
		&channels.PairingOwner{SessionID: "chat-a", RequestID: "req-a"})
	require.NoError(t, err)
	require.True(t, store.MarkReady("whatsapp", stale.AttemptID, "a-replaced-revision"))

	done := make(chan struct{}, 1)
	var (
		attemptID string
		admitErr  error
		latestID  string
	)
	go func() {
		attemptID, admitErr = al.AdmitConnectPairing("whatsapp", "req-b", "chat-b")
		// A later operation on the same connector: it must not block behind
		// the admission's ordering boundary.
		if latest, ok := store.Latest("whatsapp"); ok {
			latestID = latest.AttemptID
		}
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("F3: the eligible stale-ready admission deadlocked holding the channel order — " +
			"the interruption notification re-entered the pairing lock through the gateway's " +
			"synchronous hub tap (hubSyncTap → cachePairing → LockExistingChannel)")
	}

	require.NoError(t, admitErr, "the eligible stale-ready admission completes with the real sync tap installed")
	require.NotEmpty(t, attemptID, "the successor is reserved")
	require.Equal(t, attemptID, latestID, "the reserved successor is the connector's latest attempt")

	got, ok := store.Attempt("whatsapp", stale.AttemptID)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeInterrupted, got.Outcome,
		"the eligible close still retires the stale attempt (the gate r6 pin, now through the real tap)")

	// The interrupted notice reached the gateway's reconnect cache through
	// that same synchronous tap — delivered, not dropped between the locks.
	cached, ok := handler.lastPairingState.Load("whatsapp")
	require.True(t, ok, "the interrupted notice is delivered to the reconnect cache")
	raw, _ := cached.([]byte)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(raw, &frame), "the cached notice is the pairing frame JSON")
	require.Equal(t, "req-a", frame["request_id"], "the notice names the interrupted attempt's own chat")
	require.Equal(t, string(channels.PairingStatusError), frame["status"], "the notice is the interruption")

	// The notice envelope, as the event bus delivered it — its generation and
	// sequence are what AcceptsProgress validates on any later delivery.
	var noticeGeneration, noticeSequence uint64
	deadline := time.After(10 * time.Second)
	for noticeSequence == 0 {
		select {
		case evt, open := <-events.C:
			require.True(t, open, "the event subscription closed before the notice was delivered")
			if payload, isPairing := evt.Payload.(agent.WhatsAppPairingPayload); isPairing &&
				payload.AttemptID == stale.AttemptID && payload.Status == channels.PairingStatusError {
				noticeGeneration, noticeSequence = payload.Generation, payload.Seq
			}
		case <-deadline:
			t.Fatal("the interruption notice never reached the event bus")
		}
	}

	// ── Expiry (chief ruling C+D clause D): the admission-time notice is
	// narrow and self-expiring — it expires on the next accepted progress.
	// Stamp accepted progress for the reserved successor (ready it, take its
	// token, activate it, then a waiting frame): the notice's cache entry goes
	// with it and the notice's own envelope is no longer accepted for
	// delivery. Expected values are the ruling's, not the implementation's.
	require.True(t, store.MarkReady("whatsapp", attemptID, "a-successor-revision"),
		"precondition: the reserved successor readies for its own start")
	active, err := store.Activate("whatsapp", store.TakeReady("whatsapp", "a-successor-revision"),
		func(string) func(context.Context) error {
			return func(context.Context) error { return nil }
		})
	require.NoError(t, err)
	require.Equal(t, attemptID, active.AttemptID, "precondition: the successor, not the notice, goes active")
	al.EmitWhatsAppPairingAttempt("whatsapp", attemptID, channels.PairingStatusWaiting, "", "")

	_, stillCached := handler.lastPairingState.Load("whatsapp")
	require.False(t, stillCached,
		"clause D: the accepted successor progress expires the notice — its reconnect-cache entry is gone")

	order := store.LockChannel("whatsapp")
	noticeAccepted := order.AcceptsProgress(noticeGeneration, noticeSequence, stale.AttemptID, channels.PairingStatusError)
	order.Unlock()
	require.False(t, noticeAccepted,
		"clause D: the expired notice's envelope is no longer accepted for delivery after the successor's accepted progress")
}
