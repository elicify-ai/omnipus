package gateway

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// attemptChannel is a pairing channel that runs its QR pairing as store-tracked
// attempts, exactly as the native WhatsApp channel does (gate r3 model): its
// Start consumes the start's admitted token (or mints an unowned attempt),
// registers the attempt's own teardown handle and reports progress with the
// attempt id. Only the platform (whatsmeow) is faked.
type attemptChannel struct {
	id         string
	mu         sync.Mutex
	store      *channels.PairingAttempts
	observer   func(attemptID string, status channels.PairingStatus, qr, message string)
	attemptID  string
	running    bool
	starts     int
	stopCalled chan string
	// onTeardown, when set, runs inside an attempt's teardown — a
	// controllably slow platform client.
	onTeardown func()
	// paired models a device already linked: a start needs no pairing.
	paired bool
}

// restartInterruptedText is the ruled text for a pairing a server restart cut
// off (founder ruling 2026-10-21), written out as independent test data.
const restartInterruptedText = "A server restart interrupted this pairing. Press Retry to get a new code."

func newAttemptChannel(id string) *attemptChannel {
	return &attemptChannel{id: id, stopCalled: make(chan string, 8)}
}

func (c *attemptChannel) Name() string                                    { return c.id }
func (c *attemptChannel) Stop(context.Context) error                      { c.setRunning(false); return nil }
func (c *attemptChannel) Send(context.Context, bus.OutboundMessage) error { return nil }
func (c *attemptChannel) IsAllowed(string) bool                           { return true }
func (c *attemptChannel) IsAllowedSender(bus.SenderInfo) bool             { return true }
func (c *attemptChannel) ReasoningChannelID() string                      { return "" }
func (c *attemptChannel) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}
func (c *attemptChannel) setRunning(v bool) {
	c.mu.Lock()
	c.running = v
	c.mu.Unlock()
}

func (c *attemptChannel) SetPairingAttempts(store *channels.PairingAttempts, instance string) {
	c.mu.Lock()
	c.store = store
	c.mu.Unlock()
}

func (c *attemptChannel) SetPairingAttemptObserver(fn func(attemptID string, status channels.PairingStatus, qr, message string)) {
	c.mu.Lock()
	c.observer = fn
	c.mu.Unlock()
}

// Start begins a new pairing attempt and shows its first code.
func (c *attemptChannel) Start(ctx context.Context) error {
	token, boot := channels.PairingStartFrom(ctx)
	c.mu.Lock()
	store, paired := c.store, c.paired
	c.mu.Unlock()
	switch {
	case paired:
		// Already linked, as the native channel: an admitted start needs no
		// pairing and closes its admission.
		if token != "" {
			store.CloseNoPairing(c.id, token)
		}
		c.setRunning(true)
		return nil
	case boot && token == "":
		// A gateway start of an unpaired connector, as the native channel:
		// the earlier pairing was interrupted; nothing resumes.
		rec := store.RecordRestartInterrupted(c.id)
		c.emitFor(rec.AttemptID, channels.PairingStatusError, "", restartInterruptedText)
		return nil
	}
	rec, err := store.Activate(c.id, token, func(attemptID string) func(context.Context) error {
		return func(ctx context.Context) error { return c.teardown(attemptID) }
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.attemptID, c.running = rec.AttemptID, true
	c.starts++
	c.mu.Unlock()
	c.emit(channels.PairingStatusWaiting, "", "")
	c.emit(channels.PairingStatusCode, "QR-"+rec.AttemptID, "")
	return nil
}

func (c *attemptChannel) teardown(attemptID string) error {
	c.mu.Lock()
	current, hook := c.attemptID, c.onTeardown
	c.mu.Unlock()
	if current != attemptID {
		return nil
	}
	if hook != nil {
		hook()
	}
	c.mu.Lock()
	c.running, c.attemptID = false, ""
	c.mu.Unlock()
	c.stopCalled <- attemptID
	return nil
}

func (c *attemptChannel) currentAttempt() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attemptID
}

func (c *attemptChannel) emit(status channels.PairingStatus, qr, message string) {
	c.mu.Lock()
	attemptID := c.attemptID
	c.mu.Unlock()
	c.emitFor(attemptID, status, qr, message)
}

func (c *attemptChannel) emitFor(attemptID string, status channels.PairingStatus, qr, message string) {
	c.mu.Lock()
	obs := c.observer
	c.mu.Unlock()
	if obs != nil {
		obs(attemptID, status, qr, message)
	}
}

// timeout ends the current attempt the way the platform's QR expiry does.
func (c *attemptChannel) timeout() {
	c.mu.Lock()
	store, attemptID := c.store, c.attemptID
	c.mu.Unlock()
	if _, applied := store.Complete(c.id, attemptID, channels.PairingOutcomeTimeout); !applied {
		return
	}
	c.emit(channels.PairingStatusTimeout, "", "the QR code expired before it was scanned")
	c.mu.Lock()
	c.attemptID, c.running = "", false
	c.mu.Unlock()
}

// registerAttemptChannel puts ch under the loop's channel manager with the
// production pairing wiring (attempt store and attempt observer).
func registerAttemptChannel(t *testing.T, al *agent.AgentLoop, mgr *channels.Manager, ch *attemptChannel) {
	t.Helper()
	mgr.SetPairingAttemptObserver(al.EmitWhatsAppPairingAttempt)
	mgr.RegisterChannel(ch.id, ch)
}

// startAdmitted does what the manager's start does for an admitted attempt:
// it consumes the token ready at the connector's current revision and starts
// the channel with it.
func startAdmitted(t *testing.T, al *agent.AgentLoop, mgr *channels.Manager, ch *attemptChannel) {
	t.Helper()
	token := mgr.PairingAttempts().TakeReady(ch.id, channels.ChannelRevision(al.GetConfig(), ch.id))
	require.NotEmpty(t, token, "instrument: an admitted, committed attempt is ready to start")
	require.NoError(t, ch.Start(channels.WithPairingStart(context.Background(), token, false)))
}

// admitAndStartConnect admits an owned connect attempt for (requestID,
// sessionID), commits it and starts it — the real admission path a connect
// takes, with the reload's start done by startAdmitted.
func admitAndStartConnect(t *testing.T, al *agent.AgentLoop, mgr *channels.Manager, ch *attemptChannel, requestID, sessionID string) string {
	t.Helper()
	attemptID, err := al.AdmitConnectPairing(ch.id, requestID, sessionID)
	require.NoError(t, err)
	require.True(t, al.CommitPairing(ch.id, attemptID), "the admitted attempt becomes startable")
	startAdmitted(t, al, mgr, ch)
	require.Equal(t, attemptID, ch.currentAttempt(), "the start consumed exactly the admitted attempt")
	return attemptID
}
