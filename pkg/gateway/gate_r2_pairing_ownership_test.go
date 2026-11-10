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

// conflictText is the ruled 409 text (architect ruling arch-retry-contract.md
// and the binding pairing-ownership model), written out here as independent
// test data — never read from the implementation's constant (CHECK-r3 R3-F3).
const conflictText = "This chat can no longer retry this pairing. Open the WhatsApp connector to start a new pairing."

type ownershipFixture struct {
	t        *testing.T
	al       *agent.AgentLoop
	mgr      *channels.Manager
	api      *restAPI
	ch       *attemptChannel
	chatA    string
	chatB    string
	storeDir string
	events   agent.EventSubscription
}

func newOwnershipFixture(t *testing.T) *ownershipFixture {
	t.Helper()
	cfg := secretCardAgentConfig("omnipus", "Omnipus", false)
	cfg.Channels = map[string]config.ChannelInstanceConfig{ownershipChannelID: {Type: "whatsapp", Enabled: true}}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &secretScriptProvider{toolName: "bash", toolArgs: map[string]any{"command": "echo unused"}})
	mgr, err := channels.NewManager(&config.Config{}, nil, msgBus, nil)
	require.NoError(t, err)
	ch := newAttemptChannel(ownershipChannelID)
	registerAttemptChannel(t, al, mgr, ch)
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
		t: t, al: al, mgr: mgr, api: &restAPI{agentLoop: al}, ch: ch, chatA: a.ID, chatB: b.ID,
		storeDir: store.BaseDir(), events: al.SubscribeEvents(128),
	}
}

// connect starts a pairing attempt owned by (requestID, sessionID) through
// the real admission and start path, and returns its code frame.
func (f *ownershipFixture) connect(requestID, sessionID string) agent.WhatsAppPairingPayload {
	f.t.Helper()
	admitAndStartConnect(f.t, f.al, f.mgr, f.ch, requestID, sessionID)
	return f.await(channels.PairingStatusCode)
}

// connectorsStart starts an unowned attempt the way a Connectors enable
// does: its own admission, committed, then the start consumes it (gate r4:
// no start without its own token).
func (f *ownershipFixture) connectorsStart() agent.WhatsAppPairingPayload {
	f.t.Helper()
	attemptID, err := f.al.AdmitEnablePairing(ownershipChannelID)
	require.NoError(f.t, err)
	require.True(f.t, f.al.CommitPairing(ownershipChannelID, attemptID))
	startAdmitted(f.t, f.al, f.mgr, f.ch)
	return f.await(channels.PairingStatusCode)
}

// cutOff ends the running attempt the way a disable or reload stop of the
// native channel does: interrupted, with its frame.
func (f *ownershipFixture) cutOff() {
	f.t.Helper()
	attemptID := f.ch.currentAttempt()
	_, applied := f.mgr.PairingAttempts().Complete(ownershipChannelID, attemptID, channels.PairingOutcomeInterrupted)
	require.True(f.t, applied)
	f.ch.mu.Lock()
	f.ch.attemptID, f.ch.running = "", false
	f.ch.mu.Unlock()
}

func (f *ownershipFixture) timeout() agent.WhatsAppPairingPayload {
	f.t.Helper()
	f.ch.timeout()
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

// admit runs the REST Retry admission as user, returning the response (200
// when admitted) and the reserved attempt id. The complete route is driven by
// gate_r3_pairing_rest_test.go.
func (f *ownershipFixture) admit(user, requestID, sessionID string) (*httptest.ResponseRecorder, string) {
	f.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/whatsapp/enable", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: user}))
	attemptID, ok := f.api.admitPairingRetry(rec, req, ownershipChannelID, requestID, sessionID)
	if ok {
		rec.Code = http.StatusOK
	}
	return rec, attemptID
}

func requireConflict(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var body generated.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Code)
	require.Equal(t, "pairing_retry_conflict", *body.Code)
	require.Equal(t, conflictText, body.Error, "the ruled literal conflict text")
}

// TestPairingRetry_ExplicitRetryIsAFreshAttemptOfThatChat is the positive
// control (BDD-8.8, K10; gate r3 M4): A's pairing times out; A's Retry is
// admitted as a NEW attempt whose frames carry A's ids, so A's Stop ends it.
func TestPairingRetry_ExplicitRetryIsAFreshAttemptOfThatChat(t *testing.T) {
	f := newOwnershipFixture(t)
	first := f.connect("req-a", f.chatA)
	require.Equal(t, "req-a", first.RequestID)
	firstAttempt := f.ch.currentAttempt()
	require.Equal(t, "req-a", f.timeout().RequestID)

	rec, retry := f.admit(wu6Owner, "req-a", f.chatA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEqual(t, firstAttempt, retry, "a Retry is a fresh attempt, never the old one")
	require.True(t, f.al.CommitPairing(ownershipChannelID, retry))
	startAdmitted(t, f.al, f.mgr, f.ch)
	code := f.await(channels.PairingStatusCode)
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
	f.connect("req-a", f.chatA)
	f.timeout()

	before, ok := f.mgr.PairingAttempts().Latest(ownershipChannelID)
	require.True(t, ok)
	f.subscribe("req-a", f.chatA)
	// Gate r4 CHECK: a passive subscribe reserves NOTHING — no Retry
	// admission, no new pair_ id; the attempt store is unchanged.
	after, ok := f.mgr.PairingAttempts().Latest(ownershipChannelID)
	require.True(t, ok)
	require.Equal(t, before, after, "a reconnect only observes; it never admits an attempt")
	code := f.connectorsStart()
	require.Empty(t, code.RequestID, "a Connectors-started attempt is unowned")
	require.Empty(t, code.SessionID)
	f.stop(f.chatA)
	f.requireStopReached(false, "an old chat's Stop must not reach a later Connectors-started pairing")
}

// TestPairingRetry_OldChatCannotRetryAfterANewerAttempt is gate r3 T2-R3-S1
// / CR-T2-R3-02 / R3-T1 (M3). Corrected oracle: the former
// TestPairingRetry_StaleOwnerDoesNotCarryIntoALaterAttempt expected A's Retry
// to be admitted (HTTP 200) after a newer Connectors attempt; under the
// architect ruling and the binding model it is REFUSED (409), whether the
// newer attempt is still running or has ended.
func TestPairingRetry_OldChatCannotRetryAfterANewerAttempt(t *testing.T) {
	f := newOwnershipFixture(t)
	f.connect("req-a", f.chatA)
	f.cutOff()                  // A was disabled mid-attempt
	code := f.connectorsStart() // and Connectors started a new pairing
	require.Empty(t, code.RequestID, "a stale owner must not label a later attempt")
	f.stop(f.chatA)
	f.requireStopReached(false, "a stale owner's Stop must not reach a later attempt")

	rec, _ := f.admit(wu6Owner, "req-a", f.chatA)
	requireConflict(t, rec)
	f.timeout()
	rec, _ = f.admit(wu6Owner, "req-a", f.chatA)
	requireConflict(t, rec)
}

// TestPairingRetry_NoOpStopKeepsTheOwnership is gate r2 CR-T2-04 (M3): a Stop
// that finds no pairing in flight changes nothing, so the chat that owned the
// ended attempt can still Retry. A Stop of an admitted attempt that never
// started revokes it with a stopped frame carrying its ids, and the chat can
// Retry that too.
func TestPairingRetry_NoOpStopKeepsTheOwnership(t *testing.T) {
	f := newOwnershipFixture(t)
	f.connect("req-a", f.chatA)
	f.timeout()
	f.stop(f.chatA)
	f.requireStopReached(false, "nothing was in flight")
	rec, _ := f.admit(wu6Owner, "req-a", f.chatA)
	require.Equal(t, http.StatusOK, rec.Code, "the no-op Stop kept A's ownership")

	g := newOwnershipFixture(t)
	attemptID, err := g.al.AdmitConnectPairing(ownershipChannelID, "req-a", g.chatA)
	require.NoError(t, err)
	g.stop(g.chatA)
	revoked := g.await(channels.PairingStatusStopped)
	require.Equal(t, "req-a", revoked.RequestID, "a revoked admission's stopped frame names its own request")
	require.False(t, g.al.CommitPairing(ownershipChannelID, attemptID), "a revoked admission never becomes startable")
	rec, _ = g.admit(wu6Owner, "req-a", g.chatA)
	require.Equal(t, http.StatusOK, rec.Code, "the chat may deliberately Retry its stopped pairing")
}

// TestPairingRetry_CompetingClaimsKeepTheFirst covers the serial orderings of
// T2-R3-S1's concurrent case and T2-SF-12 (M2, M5): whichever chat holds the
// next attempt first keeps it; the loser is refused, never silently adopted.
// The overlapping-call proof is TestPairingAttempts_ConcurrentAdmissionsHaveOneWinner.
func TestPairingRetry_CompetingClaimsKeepTheFirst(t *testing.T) {
	t.Run("connect first", func(t *testing.T) {
		f := newOwnershipFixture(t)
		f.connect("req-a", f.chatA)
		f.timeout()
		winner, err := f.al.AdmitConnectPairing(ownershipChannelID, "req-b", f.chatB)
		require.NoError(t, err)
		rec, _ := f.admit(wu6Owner, "req-a", f.chatA)
		requireConflict(t, rec)
		// Restored (gate r4 CHECK): the refused Retry left B's claim intact —
		// the winning connect's next code still carries B's ids and owner.
		require.True(t, f.al.CommitPairing(ownershipChannelID, winner))
		startAdmitted(t, f.al, f.mgr, f.ch)
		code := f.await(channels.PairingStatusCode)
		require.Equal(t, "req-b", code.RequestID, "B's claim survives A's competing Retry")
		require.Equal(t, f.chatB, code.SessionID)
		latest, ok := f.mgr.PairingAttempts().Latest(ownershipChannelID)
		require.True(t, ok)
		require.Equal(t, winner, latest.AttemptID)
		require.Equal(t, channels.PairingOwner{SessionID: f.chatB, RequestID: "req-b"}, *latest.Owner)
	})
	t.Run("retry first", func(t *testing.T) {
		f := newOwnershipFixture(t)
		f.connect("req-a", f.chatA)
		f.timeout()
		rec, retry := f.admit(wu6Owner, "req-a", f.chatA)
		require.Equal(t, http.StatusOK, rec.Code)
		_, err := f.al.AdmitConnectPairing(ownershipChannelID, "req-b", f.chatB)
		require.ErrorIs(t, err, channels.ErrPairingConflict, "B's connect is refused while A's Retry holds the next attempt")
		require.True(t, f.al.CommitPairing(ownershipChannelID, retry))
		startAdmitted(t, f.al, f.mgr, f.ch)
		require.Equal(t, "req-a", f.await(channels.PairingStatusCode).RequestID,
			"A's admitted Retry is not overwritten by B's later claim")
	})
	t.Run("connect after the first code", func(t *testing.T) {
		f := newOwnershipFixture(t)
		f.connect("req-a", f.chatA)
		_, err := f.al.AdmitConnectPairing(ownershipChannelID, "req-b", f.chatB)
		require.ErrorIs(t, err, channels.ErrPairingConflict, "an unchanged connect cannot take over an active attempt (R3-A01)")
		f.stop(f.chatB)
		f.requireStopReached(false, "B owns nothing")
		f.stop(f.chatA)
		f.requireStopReached(true, "A still owns its active attempt")
	})
}

// TestPairingRetry_AdmissionRefusals: the REST Retry admission refuses before
// any ownership change — another user (403), another request (409, the ruled
// literal, no ids), a missing session (404) and an unreadable session (503).
// A's genuine Retry is still admitted afterwards.
func TestPairingRetry_AdmissionRefusals(t *testing.T) {
	f := newOwnershipFixture(t)
	f.connect("req-a", f.chatA)
	f.timeout()

	rec, _ := f.admit("someone-else", "req-a", f.chatA)
	require.Equal(t, http.StatusForbidden, rec.Code)
	conflict, _ := f.admit(wu6Owner, "req-not-the-owner", f.chatA)
	requireConflict(t, conflict)
	require.NotContains(t, conflict.Body.String(), "req-a", "a refusal never exposes another owner's ids")
	rec, _ = f.admit(wu6Owner, "req-a", "no-such-session")
	require.Equal(t, http.StatusNotFound, rec.Code)

	const damaged = "session_gate_r2_damaged_meta"
	require.NoError(t, os.MkdirAll(filepath.Join(f.storeDir, damaged), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(f.storeDir, damaged, "meta.json"), []byte("{not json"), 0o600))
	_, readErr := f.al.GetSessionStore().GetMeta(damaged)
	require.Error(t, readErr, "instrument: the damaged session's metadata really is unreadable")
	require.NotErrorIs(t, readErr, os.ErrNotExist, "instrument: unreadable, not missing")
	rec, _ = f.admit(wu6Owner, "req-a", damaged)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec, _ = f.admit(wu6Owner, "req-a", f.chatA)
	require.Equal(t, http.StatusOK, rec.Code, "no refusal changed A's ownership")
}

// TestPairingRetry_DeletedConnectorIsRefused is gate r3 R3-A03 (M12) at the
// admission: a Retry for a connector no longer in the configuration is a 404
// before anything is reserved. The full route is driven by
// TestPairingRetryREST_DeletedConnectorIsNotRecreated.
func TestPairingRetry_DeletedConnectorIsRefused(t *testing.T) {
	gone := newOwnershipFixture(t)
	gone.connect("req-a", gone.chatA)
	gone.timeout()
	require.NoError(t, gone.al.MutateConfig(func(c *config.Config) error {
		delete(c.Channels, ownershipChannelID)
		return nil
	}))
	rec, attemptID := gone.admit(wu6Owner, "req-a", gone.chatA)
	require.Equal(t, http.StatusNotFound, rec.Code, "a deleted connector is not recreated by a Retry (R3-A03)")
	require.Empty(t, attemptID, "nothing was reserved")
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
// CR-T2-R2-02 (gate r3 M7): the pairing teardown can block on the platform
// client, so the Stop must stamp its durable fence and ask the turn to stop
// BEFORE it. The attempt's teardown blocks until released and records what
// had already happened when it was entered.
func TestPairingStop_FenceAndCancelPrecedeASlowPairingTeardown(t *testing.T) {
	f := newOwnershipFixture(t)
	lifecycle := f.al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle, "instrument: the real loop wires a lifecycle store")
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: f.chatA, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: "omnipus",
	}))
	f.connect("req-a", f.chatA)
	before, err := lifecycle.Load(f.chatA)
	require.NoError(t, err)
	require.Nil(t, before.StopNote, "instrument: no Stop has been stamped yet")

	var cancelInitiated, fencedAtTeardown, cancelledAtTeardown atomic.Bool
	entered := make(chan struct{})
	release := make(chan struct{})
	f.ch.onTeardown = func() {
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

// TestPairingStop_NeverActsOnASuccessor is gate r3 T2-R3-S2 / T3 (M6–M8,
// M13). Window 1: the Stop selected A, then — before its effect — A timed out
// and A's Retry started a successor with the SAME request and session ids;
// the delayed effect is a no-op and the successor keeps running. Window 2:
// while A's teardown runs, no successor can start (the stopping attempt
// excludes it), and the stopped frame names A.
func TestPairingStop_NeverActsOnASuccessor(t *testing.T) {
	t.Run("selection replaced before the effect", func(t *testing.T) {
		f := newOwnershipFixture(t)
		f.connect("req-a", f.chatA)
		var successor string
		hooks := func(string) agent.CancelHooks {
			return agent.CancelHooks{CancelPendingApprovals: func(string, string) {
				if successor != "" {
					return
				}
				// After the Stop selected A and before its deferred effect.
				f.ch.timeout()
				_, retry := f.admit(wu6Owner, "req-a", f.chatA)
				require.NotEmpty(t, retry)
				require.True(t, f.al.CommitPairing(ownershipChannelID, retry))
				startAdmitted(t, f.al, f.mgr, f.ch)
				successor = f.ch.currentAttempt()
			}}
		}
		_, err := f.al.StopSession(context.Background(), agent.StopRequest{
			SessionID: f.chatA, Channel: "webchat",
			By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: wu6Owner}, HooksFor: hooks,
		})
		require.NoError(t, err)
		require.NotEmpty(t, successor, "instrument: the successor started inside the Stop window")
		f.requireStopReached(false, "a delayed Stop of A must not tear down its successor")
		require.True(t, f.mgr.PairingAttempts().IsCurrent(ownershipChannelID, successor), "the successor keeps running")
	})
	t.Run("no successor while A is torn down", func(t *testing.T) {
		f := newOwnershipFixture(t)
		f.connect("req-a", f.chatA)
		var admitErr, repairErr error
		f.ch.onTeardown = func() {
			_, admitErr = f.al.AdmitConnectPairing(ownershipChannelID, "req-b", f.chatB)
			_, repairErr = f.mgr.PairingAttempts().ActivateRepair(ownershipChannelID,
				func(string) func(context.Context) error { return nil })
		}
		f.stop(f.chatA)
		f.requireStopReached(true, "A's own Stop tears A down")
		require.ErrorIs(t, admitErr, channels.ErrPairingConflict, "no successor is admitted while A's client is torn down")
		require.ErrorIs(t, repairErr, channels.ErrPairingStopping, "nor does one start")
		stopped := f.await(channels.PairingStatusStopped)
		require.Equal(t, "req-a", stopped.RequestID, "the stopped frame names A")
	})
}
