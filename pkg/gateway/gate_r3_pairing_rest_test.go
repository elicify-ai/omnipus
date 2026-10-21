package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// pairingFactory replaces the whatsapp_native factory for one test: every
// channel the REAL manager reload builds is an attemptChannel (only the
// whatsmeow platform client is faked). gate, when set, blocks construction
// until closed — a reload that is slow to start the connector.
type pairingFactory struct {
	mu     sync.Mutex
	built  []*attemptChannel
	paired bool
	gate   chan struct{}
}

func (pf *pairingFactory) build(_ *config.Config, instanceID string, _ credentials.SecretBundle, _ *bus.MessageBus) (channels.Channel, error) {
	pf.mu.Lock()
	gate, paired := pf.gate, pf.paired
	pf.mu.Unlock()
	if gate != nil {
		<-gate
	}
	ch := newAttemptChannel(instanceID)
	ch.paired = paired
	pf.mu.Lock()
	pf.built = append(pf.built, ch)
	pf.mu.Unlock()
	return ch, nil
}

func (pf *pairingFactory) count() int {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return len(pf.built)
}

func (pf *pairingFactory) latest(t *testing.T) *attemptChannel {
	t.Helper()
	pf.mu.Lock()
	defer pf.mu.Unlock()
	require.NotEmpty(t, pf.built, "instrument: the reload built the WhatsApp connector")
	return pf.built[len(pf.built)-1]
}

type pairingRESTFixture struct {
	*wu7LiveFixture
	t       *testing.T
	factory *pairingFactory
	store   *channels.PairingAttempts
	events  agent.EventSubscription
	chatA   string
	chatB   string
}

// newPairingRESTFixture drives the production route table, reload pipeline
// and channel manager (newWU7LiveFixture); only the WhatsApp platform client
// is faked through the factory.
func newPairingRESTFixture(t *testing.T) *pairingRESTFixture {
	t.Helper()
	factory := &pairingFactory{}
	t.Cleanup(channels.SwapFactoryForTesting("whatsapp_native", factory.build))
	live := newWU7LiveFixture(t)
	live.svc.ChannelManager.SetPairingAttemptObserver(live.al.EmitWhatsAppPairingAttempt)
	f := &pairingRESTFixture{
		wu7LiveFixture: live, t: t, factory: factory,
		store: live.svc.ChannelManager.PairingAttempts(), events: live.al.SubscribeEvents(256),
	}
	f.chatA = f.ownedChat(wu6Owner)
	f.chatB = f.ownedChat(wu6Owner)
	return f
}

func (f *pairingRESTFixture) ownedChat(owner string) string {
	f.t.Helper()
	s, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "omnipus")
	require.NoError(f.t, err)
	require.NoError(f.t, f.al.GetSessionStore().SetMeta(s.ID, session.MetaPatch{Owner: &owner}))
	return s.ID
}

func (f *pairingRESTFixture) put(action, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(http.MethodPut, "/api/v1/channels/whatsapp/"+action, body)
}

// admit runs the real Retry admission (no disable, nothing saved) and returns
// its response and reserved attempt.
func (f *pairingRESTFixture) admit(requestID, sessionID string) (*httptest.ResponseRecorder, string) {
	f.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/whatsapp/enable", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: wu6Owner}))
	attemptID, ok := f.api.admitPairingRetry(rec, req, "whatsapp", requestID, sessionID)
	if ok {
		rec.Code = http.StatusOK
	}
	return rec, attemptID
}

func retryBody(requestID, sessionID string) string {
	raw, _ := json.Marshal(map[string]any{"pairing_retry": map[string]string{"request_id": requestID, "session_id": sessionID}})
	return string(raw)
}

func (f *pairingRESTFixture) await(status channels.PairingStatus) agent.WhatsAppPairingPayload {
	f.t.Helper()
	deadline := time.After(15 * time.Second)
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

// connectOwned starts a chat-owned attempt the way connect_channel does: admit,
// save (the connector is already enabled), commit, then the real reload starts
// it.
func (f *pairingRESTFixture) connectOwned(requestID, sessionID string) agent.WhatsAppPairingPayload {
	f.t.Helper()
	attemptID, err := f.al.AdmitConnectPairing("whatsapp", requestID, sessionID)
	require.NoError(f.t, err)
	require.True(f.t, f.al.CommitPairing("whatsapp", attemptID))
	require.NoError(f.t, waitForReload(f.al, f.svc.reloadOutcome))
	code := f.await(channels.PairingStatusCode)
	require.Equal(f.t, attemptID, f.factory.latest(f.t).currentAttempt(), "the reload's start consumed the admitted attempt")
	return code
}

// endedOwnedAttempt leaves chat A the owner of the connector's latest attempt,
// ended by a QR timeout, and the connector disabled — the state a chat card's
// Retry begins from (its Retry disables, then enables with pairing_retry).
func (f *pairingRESTFixture) endedOwnedAttempt() string {
	f.t.Helper()
	require.Equal(f.t, http.StatusOK, f.put("configure", "{}").Code, "Connectors adds the connector (Configure)")
	require.Equal(f.t, http.StatusOK, f.put("enable", "").Code, "and turns it on")
	unowned := f.await(channels.PairingStatusCode)
	require.Empty(f.t, unowned.RequestID, "a Connectors enable starts an unowned attempt")
	require.Empty(f.t, unowned.SessionID)
	f.factory.latest(f.t).timeout()
	code := f.connectOwned("req-a", f.chatA)
	require.Equal(f.t, "req-a", code.RequestID)
	require.Equal(f.t, f.chatA, code.SessionID)
	owned := f.factory.latest(f.t).currentAttempt()
	f.factory.latest(f.t).timeout()
	require.Equal(f.t, http.StatusOK, f.put("disable", "").Code)
	return owned
}

func (f *pairingRESTFixture) enabledOnDisk() (present, enabled bool) {
	f.t.Helper()
	raw, err := os.ReadFile(f.f1277.configPath)
	require.NoError(f.t, err)
	var doc struct {
		Channels map[string]struct {
			Enabled bool `json:"enabled"`
		} `json:"channels"`
	}
	require.NoError(f.t, json.Unmarshal(raw, &doc))
	inst, ok := doc.Channels["whatsapp"]
	return ok, inst.Enabled
}

func requireRESTConflict(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var body generated.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Code, "the 409 carries the contract's machine code")
	require.Equal(t, "pairing_retry_conflict", *body.Code)
	require.Equal(t, conflictText, body.Error, "the ruled literal conflict text")
}

// TestPairingRetryREST_RetryIsAFreshOwnedAttemptThroughTheRealHandler is gate
// r3 R3-T2 / CHECK-r3 (M1–M4, M10, M14): a chat card's Retry — disable, then
// PUT /enable with pairing_retry — goes through the production route (decode
// → admission → save → commit → real reload → start → release). The reload's
// start consumes a NEW attempt owned by that chat, its frames carry the
// chat's exact ids, and the chat's Stop ends exactly that attempt.
func TestPairingRetryREST_RetryIsAFreshOwnedAttemptThroughTheRealHandler(t *testing.T) {
	f := newPairingRESTFixture(t)
	ended := f.endedOwnedAttempt()

	rec := f.put("enable", retryBody("req-a", f.chatA))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	code := f.await(channels.PairingStatusCode)
	require.Equal(t, "req-a", code.RequestID, "the retried attempt's frames name the chat that retried")
	require.Equal(t, f.chatA, code.SessionID)
	retried := f.factory.latest(t).currentAttempt()
	require.NotEmpty(t, retried)
	require.NotEqual(t, ended, retried, "a Retry is always a new attempt")
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, retried, latest.AttemptID)
	require.NotNil(t, latest.Owner)
	require.Equal(t, channels.PairingOwner{SessionID: f.chatA, RequestID: "req-a"}, *latest.Owner)

	_, _, err := f.al.RequestCancelForSession(context.Background(), f.chatA, wu6Owner, "webchat")
	require.NoError(t, err)
	select {
	case torn := <-f.factory.latest(t).stopCalled:
		require.Equal(t, retried, torn, "the chat's Stop ends exactly its retried attempt")
	case <-time.After(10 * time.Second):
		t.Fatal("the chat's Stop never reached its retried attempt")
	}
	stopped := f.await(channels.PairingStatusStopped)
	require.Equal(t, "req-a", stopped.RequestID)
	require.Equal(t, f.chatA, stopped.SessionID)
}

// TestPairingRetryREST_RefusalsChangeNothing (M2, M3, M12, T2-SF-04): every
// refused Retry leaves the connector as it was — still disabled on disk, no
// connector built, the latest attempt and its owner untouched — and the
// genuine owner's Retry still works afterwards. A handler that skipped the
// admission would enable and start the connector on these refusals.
func TestPairingRetryREST_RefusalsChangeNothing(t *testing.T) {
	f := newPairingRESTFixture(t)
	ended := f.endedOwnedAttempt()
	built := f.factory.count()

	stranger := f.ownedChat("someone-else")
	const damaged = "session_gate_r3_damaged_meta"
	storeDir := f.al.GetSessionStore().BaseDir()
	require.NoError(t, os.MkdirAll(filepath.Join(storeDir, damaged), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, damaged, "meta.json"), []byte("{not json"), 0o600))

	cases := []struct {
		name, body string
		status     int
	}{
		{"malformed retry", `{"pairing_retry":null}`, http.StatusBadRequest},
		{"another user's chat", retryBody("req-a", stranger), http.StatusForbidden},
		{"missing chat", retryBody("req-a", "no-such-session"), http.StatusNotFound},
		{"unreadable chat", retryBody("req-a", damaged), http.StatusServiceUnavailable},
		{"not the attempt's owner", retryBody("req-b", f.chatB), http.StatusConflict},
		{"owner's chat, another request", retryBody("req-other", f.chatA), http.StatusConflict},
	}
	for _, tc := range cases {
		rec := f.put("enable", tc.body)
		require.Equal(t, tc.status, rec.Code, "%s: %s", tc.name, rec.Body.String())
		if tc.status == http.StatusConflict {
			requireRESTConflict(t, rec)
			require.NotContains(t, rec.Body.String(), f.chatA, "a refusal never exposes the owner's ids")
		}
		present, enabled := f.enabledOnDisk()
		require.True(t, present)
		require.False(t, enabled, "%s: a refused Retry enables nothing", tc.name)
		require.Equal(t, built, f.factory.count(), "%s: a refused Retry starts nothing", tc.name)
		latest, ok := f.store.Latest("whatsapp")
		require.True(t, ok)
		require.Equal(t, ended, latest.AttemptID, "%s: the latest attempt is untouched", tc.name)
	}

	require.Equal(t, http.StatusOK, f.put("enable", retryBody("req-a", f.chatA)).Code, "no refusal changed A's ownership")
	require.Equal(t, "req-a", f.await(channels.PairingStatusCode).RequestID)
}

// TestPairingRetryREST_DeletedConnectorIsNotRecreated is gate r3 R3-A03
// (M12): a Retry for a connector deleted from Connectors is a 404 and does not
// write the connector back into the configuration.
func TestPairingRetryREST_DeletedConnectorIsNotRecreated(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	// The Connectors delete, through its real handler (the admin route's
	// dev-bypass gate refuses the fixture's synthetic user).
	deleted := httptest.NewRecorder()
	f.api.deleteChannelInstance(deleted, "whatsapp")
	require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
	present, _ := f.enabledOnDisk()
	require.False(t, present, "instrument: the delete removed the connector")

	rec := f.put("enable", retryBody("req-a", f.chatA))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	present, _ = f.enabledOnDisk()
	require.False(t, present, "the Retry must not recreate a deleted connector")
	_, ok := f.store.Latest("whatsapp")
	require.False(t, ok, "the deleted connector's attempts and eligibility are gone")
}

// TestPairingRetryREST_OrdinaryEnableIsUnowned: a bodyless or {} enable is a
// Connectors start — the attempt it starts is unowned, its frames carry no
// ids and no chat's Stop reaches it (M1).
func TestPairingRetryREST_OrdinaryEnableIsUnowned(t *testing.T) {
	for _, body := range []string{"", "{}"} {
		t.Run("body="+body, func(t *testing.T) {
			f := newPairingRESTFixture(t)
			f.endedOwnedAttempt()
			require.Equal(t, http.StatusOK, f.put("enable", body).Code)
			code := f.await(channels.PairingStatusCode)
			require.Empty(t, code.RequestID, "an ordinary enable grants no chat ownership")
			require.Empty(t, code.SessionID)
			latest, ok := f.store.Latest("whatsapp")
			require.True(t, ok)
			require.Nil(t, latest.Owner)
			_, _, err := f.al.RequestCancelForSession(context.Background(), f.chatA, wu6Owner, "webchat")
			require.NoError(t, err)
			select {
			case <-f.factory.latest(t).stopCalled:
				t.Fatal("the previous owner's Stop reached an unowned attempt")
			case <-time.After(500 * time.Millisecond):
			}
		})
	}
}

// TestPairingRetryREST_LinkedDeviceAdmissionDoesNotLeak (M14): when the
// retried start finds the device already linked, the admission closes with no
// pairing. A later unowned re-pairing is not attributed to the chat, the
// chat's Stop does not reach it, and the chat cannot Retry it.
func TestPairingRetryREST_LinkedDeviceAdmissionDoesNotLeak(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	f.factory.mu.Lock()
	f.factory.paired = true
	f.factory.mu.Unlock()

	require.Equal(t, http.StatusOK, f.put("enable", retryBody("req-a", f.chatA)).Code)
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, channels.PairingPhaseTerminal, latest.Phase)
	require.Equal(t, channels.PairingOutcomeNoPairing, latest.Outcome, "a linked start closes the admission with no pairing")
	// Gate r4 R4-A05: the chat is told the device is linked, never left
	// waiting for a code.
	linked := f.await(channels.PairingStatusLinked)
	require.Equal(t, "req-a", linked.RequestID)
	require.Equal(t, "This WhatsApp device is already linked.", linked.Message)
	// Gate r4 CHECK: a linked device offers no Retry — the no-pairing result is
	// not Retry-eligible, even immediately.
	rec, _ := f.admit("req-a", f.chatA)
	requireRESTConflict(t, rec)

	ch := f.factory.latest(t)
	ch.mu.Lock()
	ch.paired = false
	ch.mu.Unlock()
	require.NoError(t, ch.repair(), "the device logged out and re-pairs")
	code := f.await(channels.PairingStatusCode)
	require.Empty(t, code.RequestID, "the re-pairing is not attributed to the earlier Retry")
	require.Empty(t, code.SessionID)
	_, _, err := f.al.RequestCancelForSession(context.Background(), f.chatA, wu6Owner, "webchat")
	require.NoError(t, err)
	select {
	case <-ch.stopCalled:
		t.Fatal("the chat's Stop reached a re-pairing it does not own")
	case <-time.After(500 * time.Millisecond):
	}
	ch.timeout()
	require.Equal(t, http.StatusOK, f.put("disable", "").Code)
	requireRESTConflict(t, f.put("enable", retryBody("req-a", f.chatA)))
}

// TestPairingRetryREST_SlowReloadKeepsTheReservation (M10): the Retry saved
// but its reload did not confirm the start within the wait. The reply says so
// (500) and the admitted attempt is kept, so when the reload does start the
// connector the chat still owns the new attempt.
func TestPairingRetryREST_SlowReloadKeepsTheReservation(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	f.reloadJoins.joinCurrent(t)
	mark := f.reloadJoins.mark()
	prev := reloadWaitTimeout
	reloadWaitTimeout = 300 * time.Millisecond
	release := orderingHoldFactory(t, f)
	t.Cleanup(func() {
		release()
		f.reloadJoins.joinCurrent(t)
		reloadWaitTimeout = prev // restore only after actual owning-cycle completion
	})

	rec := orderingExpireRetry(t, f)
	owner := f.reloadJoins.next(t, mark)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	var body generated.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "The WhatsApp connection is enabled, but its restart could not be confirmed yet. "+
		"The new code appears in this chat when the pairing starts.", body.Error)
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, channels.PairingPhaseReady, latest.Phase, "the unconfirmed Retry keeps its reservation")

	release()
	orderingJoin(t, owner)
	code := f.await(channels.PairingStatusCode)
	require.Equal(t, "req-a", code.RequestID, "the late start still belongs to the chat that retried")
	require.Equal(t, f.chatA, code.SessionID)
	current, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, latest.AttemptID, current.AttemptID, "the late start consumes only its reservation")
	require.Equal(t, channels.PairingPhaseActive, current.Phase)
	require.Equal(t, mark+1, f.reloadJoins.mark(), "a join must not request another reload")
	require.False(t, f.svc.reloadOutcome.lastFailed())
}

// TestPairingRetryREST_RestartInterruptedPairingIsRetried is the founder's
// restart rule (2026-10-21, replaces M11; T4): after a gateway start the
// unpaired connector's earlier pairing is interrupted and unowned — the chat
// card and Connectors receive the ruled text with no ids — and a chat's Retry
// from that state is a fresh attempt owned by that chat.
func TestPairingRetryREST_RestartInterruptedPairingIsRetried(t *testing.T) {
	f := newPairingRESTFixture(t)
	require.Equal(t, http.StatusOK, f.put("configure", "{}").Code)
	require.Equal(t, http.StatusOK, f.put("enable", "").Code)
	f.await(channels.PairingStatusCode)
	ch := f.factory.latest(t)
	ch.timeout()
	// The gateway's boot start of the still-unpaired connector.
	require.NoError(t, ch.Start(channels.WithPairingStart(context.Background(), "", true)))
	interrupted := f.await(channels.PairingStatusError)
	require.Equal(t, restartInterruptedText, interrupted.Message)
	require.Empty(t, interrupted.RequestID, "no ownership survives a restart")
	require.Empty(t, interrupted.SessionID)
	require.True(t, interrupted.RestartInterrupted, "marked so the reconnect cache keeps it for pages that reconnect after the restart")
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.True(t, latest.RestartInterrupted)
	require.Nil(t, latest.Owner)

	require.Equal(t, http.StatusOK, f.put("disable", "").Code)
	rec := f.put("enable", retryBody("req-b", f.chatB))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	code := f.await(channels.PairingStatusCode)
	require.Equal(t, "req-b", code.RequestID, "the Retry is a fresh attempt owned by the chat that pressed it")
	require.Equal(t, f.chatB, code.SessionID)
}
