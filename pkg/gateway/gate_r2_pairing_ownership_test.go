package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// ownershipChannelID is a real WhatsApp channel id, so the REST Retry
// admission (which accepts only WhatsApp pairing) resolves it.
const ownershipChannelID = "whatsapp"

// ownershipTestChannel models the WhatsApp channel's pairing: inFlight says
// whether a QR loop is running, so StopPairing can be a no-op.
type ownershipTestChannel struct {
	inFlight   atomic.Bool
	stopCalled chan struct{}
	// onStopPairing, when set, runs inside StopPairing before it returns —
	// the controllably slow platform teardown.
	onStopPairing func()
}

func (c *ownershipTestChannel) Name() string                                    { return ownershipChannelID }
func (c *ownershipTestChannel) Start(context.Context) error                     { return nil }
func (c *ownershipTestChannel) Stop(context.Context) error                      { return nil }
func (c *ownershipTestChannel) Send(context.Context, bus.OutboundMessage) error { return nil }
func (c *ownershipTestChannel) IsRunning() bool                                 { return true }
func (c *ownershipTestChannel) IsAllowed(string) bool                           { return true }
func (c *ownershipTestChannel) IsAllowedSender(bus.SenderInfo) bool             { return true }
func (c *ownershipTestChannel) ReasoningChannelID() string                      { return "" }
func (c *ownershipTestChannel) StopPairing(context.Context) bool {
	if !c.inFlight.Load() {
		return false
	}
	if c.onStopPairing != nil {
		c.onStopPairing()
	}
	c.inFlight.Store(false)
	c.stopCalled <- struct{}{}
	return true
}

type ownershipFixture struct {
	t        *testing.T
	al       *agent.AgentLoop
	api      *restAPI
	ch       *ownershipTestChannel
	chatA    string
	chatB    string
	storeDir string
	events   agent.EventSubscription
}

func newOwnershipFixture(t *testing.T) *ownershipFixture {
	t.Helper()
	cfg := secretCardAgentConfig("omnipus", "Omnipus", false)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &secretScriptProvider{toolName: "bash", toolArgs: map[string]any{"command": "echo unused"}})
	mgr, err := channels.NewManager(cfg, nil, msgBus, nil)
	require.NoError(t, err)
	ch := &ownershipTestChannel{stopCalled: make(chan struct{}, 4)}
	mgr.RegisterChannel(ownershipChannelID, ch)
	al.SetChannelManager(mgr)
	store := al.GetSessionStore()
	a, err := store.NewSession(session.SessionTypeChat, "webchat", "omnipus")
	require.NoError(t, err)
	b, err := store.NewSession(session.SessionTypeChat, "webchat", "omnipus")
	require.NoError(t, err)
	owner := wu6Owner
	require.NoError(t, store.SetMeta(a.ID, session.MetaPatch{Owner: &owner}))
	require.NoError(t, store.SetMeta(b.ID, session.MetaPatch{Owner: &owner}))
	return &ownershipFixture{
		t: t, al: al, api: &restAPI{agentLoop: al}, ch: ch, chatA: a.ID, chatB: b.ID,
		storeDir: store.BaseDir(), events: al.SubscribeEvents(128),
	}
}

// attempt emits what the real channel emits when a pairing attempt starts
// ("waiting") and shows its first code, and returns the code frame's ids.
func (f *ownershipFixture) attempt() agent.WhatsAppPairingPayload {
	f.t.Helper()
	f.ch.inFlight.Store(true)
	f.al.EmitWhatsAppPairing(ownershipChannelID, channels.PairingStatusWaiting, "", "")
	f.al.EmitWhatsAppPairing(ownershipChannelID, channels.PairingStatusCode, "QR-CODE", "")
	return f.await(channels.PairingStatusCode)
}

func (f *ownershipFixture) timeout() agent.WhatsAppPairingPayload {
	f.t.Helper()
	f.ch.inFlight.Store(false)
	f.al.EmitWhatsAppPairing(ownershipChannelID, channels.PairingStatusTimeout, "", "expired")
	return f.await(channels.PairingStatusTimeout)
}

func (f *ownershipFixture) await(status channels.PairingStatus) agent.WhatsAppPairingPayload {
	f.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case evt := <-f.events.C:
			if p, ok := evt.Payload.(agent.WhatsAppPairingPayload); ok && evt.Kind == agent.EventKindWhatsAppPairing && p.Status == status {
				return p
			}
		case <-deadline:
			f.t.Fatalf("no %q pairing event arrived", status)
		}
	}
}

func (f *ownershipFixture) stop(chat string) {
	f.t.Helper()
	_, _, err := f.al.RequestCancelForSession(context.Background(), chat, wu6Owner, "webchat")
	require.NoError(f.t, err)
}

func (f *ownershipFixture) requireStopReached(want bool, msg string) {
	f.t.Helper()
	select {
	case <-f.ch.stopCalled:
		require.True(f.t, want, msg)
	case <-time.After(2 * time.Second):
		require.False(f.t, want, msg)
	}
}

// subscribe sends the real whatsapp_pairing_subscribe frame a chat card sends
// on every reconnect, with that chat's ids, through the read loop.
func (f *ownershipFixture) subscribe(requestID, sessionID string) {
	f.t.Helper()
	h := makeMinimalHandler()
	h.agentLoop = f.al
	wc, _ := makeForwarderTestConn(16)
	wc.userID = wu6Owner
	reader := &wsHandlerReadLoop{h: h, ctx: context.Background(), wc: wc, chatID: "chat"}
	raw, err := json.Marshal(generated.WhatsAppPairingSubscribeFrame{
		Type: string(generated.WsFrameTypeWhatsappPairingSubscribe), ChannelId: ownershipChannelID,
		Active: true, RequestId: &requestID, SessionId: &sessionID,
	})
	require.NoError(f.t, err)
	reader.dispatchFrame(raw, wsTypeOnly{Type: string(generated.WsFrameTypeWhatsappPairingSubscribe)})
}

// admit runs the REST Retry admission as user, returning the refusal status
// (200 when admitted).
func (f *ownershipFixture) admit(user, requestID, sessionID string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/whatsapp/enable", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: user}))
	if f.api.admitPairingRetry(rec, req, ownershipChannelID, requestID, sessionID) {
		rec.Code = http.StatusOK
	}
	return rec
}

// TestPairingRetry_ExplicitRetryClaimsTheNextAttempt is the positive control
// (BDD-8.8, K10): chat A's pairing times out; A's explicit Retry is admitted
// and the next attempt's frames carry A's ids, so A's Stop ends it and the
// stopped frame names A's request.
func TestPairingRetry_ExplicitRetryClaimsTheNextAttempt(t *testing.T) {
	f := newOwnershipFixture(t)
	f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
	require.Equal(t, "req-a", f.attempt().RequestID)
	require.Equal(t, "req-a", f.timeout().RequestID)

	require.Equal(t, http.StatusOK, f.admit(wu6Owner, "req-a", f.chatA).Code)
	code := f.attempt()
	require.Equal(t, "req-a", code.RequestID, "the retried attempt is correlated to the chat that retried it")
	require.Equal(t, f.chatA, code.SessionID)
	f.stop(f.chatA)
	f.requireStopReached(true, "A's Stop must end A's retried pairing")
	stopped := f.await(channels.PairingStatusStopped)
	require.Equal(t, "req-a", stopped.RequestID)
	require.Equal(t, f.chatA, stopped.SessionID)
}

// TestPairingRetry_PassiveReconnectNeverReclaims is gate r2 T2-R2-S1 /
// R2-A02: after A's pairing ended, A's page reconnects and re-subscribes with
// its ids (no Retry). A later attempt started from Connectors is unowned: its
// frames carry no ids and A's Stop cannot end it.
func TestPairingRetry_PassiveReconnectNeverReclaims(t *testing.T) {
	f := newOwnershipFixture(t)
	f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
	f.attempt()
	f.timeout()

	f.subscribe("req-a", f.chatA)
	code := f.attempt() // Connectors disable/enable: no claim
	require.Empty(t, code.RequestID, "a Connectors-started attempt is unowned")
	require.Empty(t, code.SessionID)
	f.stop(f.chatA)
	f.requireStopReached(false, "an old chat's Stop must not reach a later Connectors-started pairing")
}

// TestPairingRetry_StaleOwnerDoesNotCarryIntoALaterAttempt: A owns an
// attempt that ends without a terminal event (the channel was disabled); the
// next, Connectors-started attempt is unowned, and A can still Retry.
func TestPairingRetry_StaleOwnerDoesNotCarryIntoALaterAttempt(t *testing.T) {
	f := newOwnershipFixture(t)
	f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
	f.attempt()
	code := f.attempt() // disabled mid-attempt, then restarted from Connectors
	require.Empty(t, code.RequestID, "a stale owner must not label a later attempt")
	f.stop(f.chatA)
	f.requireStopReached(false, "a stale owner's Stop must not reach a later attempt")
	require.Equal(t, http.StatusOK, f.admit(wu6Owner, "req-a", f.chatA).Code, "A remains eligible to Retry its own pairing")
}

// TestPairingRetry_NoOpStopKeepsTheOwnership is gate r2 CR-T2-04: a Stop that
// finds no pairing in flight must not delete the only ownership record; A's
// later Retry is still admitted and correlated.
func TestPairingRetry_NoOpStopKeepsTheOwnership(t *testing.T) {
	f := newOwnershipFixture(t)
	f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
	f.al.SettlePairingRequest(ownershipChannelID, "req-a", f.chatA) // the start began no attempt
	f.stop(f.chatA)
	f.requireStopReached(false, "nothing was in flight")
	require.Equal(t, http.StatusOK, f.admit(wu6Owner, "req-a", f.chatA).Code, "the no-op Stop kept A's ownership")
	require.Equal(t, "req-a", f.attempt().RequestID)
}

// TestPairingRetry_CompetingClaimsKeepTheFirst covers the concurrent case of
// T2-R2-S1: whichever chat claims the next attempt first keeps it.
func TestPairingRetry_CompetingClaimsKeepTheFirst(t *testing.T) {
	t.Run("connect first", func(t *testing.T) {
		f := newOwnershipFixture(t)
		f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
		f.attempt()
		f.timeout()
		f.al.NotePairingRequest(ownershipChannelID, "req-b", f.chatB)
		rec := f.admit(wu6Owner, "req-a", f.chatA)
		require.Equal(t, http.StatusConflict, rec.Code)
		require.Equal(t, "req-b", f.attempt().RequestID, "B's claim survives A's competing Retry")
	})
	t.Run("retry first", func(t *testing.T) {
		f := newOwnershipFixture(t)
		f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
		f.attempt()
		f.timeout()
		require.Equal(t, http.StatusOK, f.admit(wu6Owner, "req-a", f.chatA).Code)
		f.al.NotePairingRequest(ownershipChannelID, "req-b", f.chatB)
		require.Equal(t, "req-a", f.attempt().RequestID, "A's admitted Retry is not overwritten by B's later claim")
	})
}

// TestPairingRetry_AdmissionRefusals: the REST Retry admission refuses before
// any ownership change — another user (403), another request (409 with the
// contract's fixed, id-free text), a missing session (404) and an unreadable
// session (503). A's genuine Retry is still admitted afterwards.
func TestPairingRetry_AdmissionRefusals(t *testing.T) {
	f := newOwnershipFixture(t)
	f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
	f.attempt()
	f.timeout()

	require.Equal(t, http.StatusForbidden, f.admit("someone-else", "req-a", f.chatA).Code)
	conflict := f.admit(wu6Owner, "req-not-the-owner", f.chatA)
	require.Equal(t, http.StatusConflict, conflict.Code)
	var body generated.ErrorResponse
	require.NoError(t, json.Unmarshal(conflict.Body.Bytes(), &body))
	require.NotNil(t, body.Code)
	require.Equal(t, "pairing_retry_conflict", *body.Code)
	require.Equal(t, pairingRetryConflictText, body.Error)
	require.NotContains(t, conflict.Body.String(), "req-a", "a refusal never exposes another owner's ids")
	require.Equal(t, http.StatusNotFound, f.admit(wu6Owner, "req-a", "no-such-session").Code)

	// A session that exists on disk but whose metadata cannot be read (the
	// store's in-memory cache never held it, so the read reaches the file).
	const damaged = "session_gate_r2_damaged_meta"
	require.NoError(t, os.MkdirAll(filepath.Join(f.storeDir, damaged), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(f.storeDir, damaged, "meta.json"), []byte("{not json"), 0o600))
	_, readErr := f.al.GetSessionStore().GetMeta(damaged)
	require.Error(t, readErr, "instrument: the damaged session's metadata really is unreadable")
	require.NotErrorIs(t, readErr, os.ErrNotExist, "instrument: unreadable, not missing")
	require.Equal(t, http.StatusServiceUnavailable, f.admit(wu6Owner, "req-a", damaged).Code)

	require.Equal(t, http.StatusOK, f.admit(wu6Owner, "req-a", f.chatA).Code, "no refusal changed A's ownership")
}

// TestChannelEnableRequest_Decoding pins the contract: an absent body or {}
// is an ordinary enable; null, a null or partial pairing_retry, unknown
// fields, blank or oversized ids are 400 — never downgraded to an enable.
func TestChannelEnableRequest_Decoding(t *testing.T) {
	long := strings.Repeat("x", 129)
	cases := map[string]struct {
		body  string
		ok    bool
		retry bool
	}{
		"absent":         {"", true, false},
		"empty object":   {"{}", true, false},
		"retry":          {`{"pairing_retry":{"request_id":"r","session_id":"s"}}`, true, true},
		"null body":      {"null", false, false},
		"null retry":     {`{"pairing_retry":null}`, false, false},
		"partial retry":  {`{"pairing_retry":{"request_id":"r"}}`, false, false},
		"unknown field":  {`{"pairing_retry":{"request_id":"r","session_id":"s"},"x":1}`, false, false},
		"unknown nested": {`{"pairing_retry":{"request_id":"r","session_id":"s","x":1}}`, false, false},
		"blank id":       {`{"pairing_retry":{"request_id":"   ","session_id":"s"}}`, false, false},
		"oversized id":   {`{"pairing_retry":{"request_id":"` + long + `","session_id":"s"}}`, false, false},
		"wrong type":     {`{"pairing_retry":{"request_id":1,"session_id":"s"}}`, false, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/whatsapp/enable", strings.NewReader(tc.body))
			got, ok := decodeChannelEnableRequest(rec, req)
			require.Equal(t, tc.ok, ok, "body %q: %s", tc.body, rec.Body.String())
			if !tc.ok {
				require.Equal(t, http.StatusBadRequest, rec.Code)
				return
			}
			require.Equal(t, tc.retry, got != nil && got.PairingRetry != nil)
		})
	}
}

// TestPairingStop_FenceAndCancelPrecedeASlowPairingTeardown is gate r2
// CR-T2-R2-02: the pairing teardown can block on the platform client, so the
// Stop must stamp its durable fence and ask the turn to stop BEFORE it. The
// fake's StopPairing blocks until released and records what had already
// happened when it was entered.
func TestPairingStop_FenceAndCancelPrecedeASlowPairingTeardown(t *testing.T) {
	f := newOwnershipFixture(t)
	lifecycle := f.al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle, "instrument: the real loop wires a lifecycle store")
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: f.chatA, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: "omnipus",
	}))
	f.al.NotePairingRequest(ownershipChannelID, "req-a", f.chatA)
	f.attempt()
	before, err := lifecycle.Load(f.chatA)
	require.NoError(t, err)
	require.Nil(t, before.StopNote, "instrument: no Stop has been stamped yet")

	var cancelInitiated atomic.Bool
	var fencedAtTeardown, cancelledAtTeardown atomic.Bool
	entered := make(chan struct{})
	release := make(chan struct{})
	f.ch.onStopPairing = func() {
		// The accepted Stop writes Stop and StopNote; landing the record
		// Stopped clears Stop but keeps StopNote, so either proves the fence.
		if rec, err := lifecycle.Load(f.chatA); err == nil && (rec.Stop != nil || rec.StopNote != nil) {
			fencedAtTeardown.Store(true)
		}
		cancelledAtTeardown.Store(cancelInitiated.Load())
		close(entered)
		<-release
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.al.StopSession(context.Background(), agent.StopRequest{
			SessionID: f.chatA,
			By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: wu6Owner},
			Channel:   "webchat",
			HooksFor: func(string) agent.CancelHooks {
				return agent.CancelHooks{CancelPendingApprovals: func(string, string) { cancelInitiated.Store(true) }}
			},
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the Stop never reached the pairing teardown")
	}
	require.True(t, fencedAtTeardown.Load(), "the durable Stop fence must be stamped before the pairing teardown")
	require.True(t, cancelledAtTeardown.Load(), "the turn must be asked to stop before the pairing teardown")
	close(release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the Stop did not finish after the teardown was released")
	}
	require.Equal(t, "req-a", f.await(channels.PairingStatusStopped).RequestID)
}
