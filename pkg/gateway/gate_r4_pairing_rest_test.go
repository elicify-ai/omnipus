package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// Gate r4 texts, written out as independent test data (agreed with chief).
const (
	inProgressText = "Another WhatsApp pairing of this connection is starting, running or stopping, " +
		"so nothing was changed. Wait for it to finish, or stop it from the chat that started it."
	cancelledText = "The WhatsApp connection is turned on, but its pairing was cancelled before it started. " +
		"Press Retry to start a new one."
)

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) generated.ErrorResponse {
	t.Helper()
	var body generated.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func (f *pairingRESTFixture) stopChat(sessionID string) {
	f.t.Helper()
	_, _, err := f.al.RequestCancelForSession(context.Background(), sessionID, wu6Owner, "webchat")
	require.NoError(f.t, err)
}

// TestOrdinaryEnableREST_RefusedWhileAChatHoldsTheNextAttempt is gate r4
// T2-SF-13 / R4-A02 / security T2-R4-S1 / CR-T2-R4-01 (M2, M14): chat A's
// Retry holds the next attempt (admitted, not yet saved). A Connectors enable
// racing it is refused before any effect — 409 pairing_in_progress, the
// connector still off, nothing started — and A's reservation is intact: when
// A's enable completes, its code carries A's ids and A's Stop ends it.
func TestOrdinaryEnableREST_RefusedWhileAChatHoldsTheNextAttempt(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	built := f.factory.count()
	rec, retry := f.admit("req-a", f.chatA)
	require.Equal(t, http.StatusOK, rec.Code, "instrument: A's Retry holds the next attempt")

	losing := f.put("enable", "")
	require.Equal(t, http.StatusConflict, losing.Code, losing.Body.String())
	body := decodeError(t, losing)
	require.NotNil(t, body.Code)
	require.Equal(t, "pairing_in_progress", *body.Code)
	require.Equal(t, inProgressText, body.Error)
	_, enabled := f.enabledOnDisk()
	require.False(t, enabled, "the losing enable saved nothing")
	require.Equal(t, built, f.factory.count(), "and started nothing")
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, retry, latest.AttemptID, "A's reservation is the latest")
	require.Equal(t, channels.PairingOwner{SessionID: f.chatA, RequestID: "req-a"}, *latest.Owner)

	// A's own enable completes the reservation (the handler's save + commit).
	done := httptest.NewRecorder()
	outcome := f.api.applyChannelEnabled(done, "whatsapp", true, channelEnableOptions{
		requireExisting: true,
		afterCommit:     func(http.ResponseWriter) bool { return f.al.CommitPairing("whatsapp", retry) },
	})
	require.Equal(t, enableConfirmed, outcome, done.Body.String())
	code := f.await(channels.PairingStatusCode)
	require.Equal(t, "req-a", code.RequestID, "the winner's pairing carries the winner's ids")
	require.Equal(t, f.chatA, code.SessionID)
	f.stopChat(f.chatA)
	select {
	case torn := <-f.factory.latest(t).stopCalled:
		require.Equal(t, retry, torn, "the winner's Stop ends exactly its attempt")
	case <-time.After(10 * time.Second):
		t.Fatal("the winner's Stop did not reach its attempt")
	}
}

// TestOrdinaryEnableREST_NeverRecreatesADeletedConnector is gate r4 DOCS
// R4-D2 / R4-A02 (M12): a Connectors Retry's turn-off succeeded, then the
// connector was deleted before its turn-on: the ordinary enable is a 404 and
// the connector is not written back. While a delete is still in progress the
// enable is refused the same way.
func TestOrdinaryEnableREST_NeverRecreatesADeletedConnector(t *testing.T) {
	t.Run("deleted between the turn-off and the turn-on", func(t *testing.T) {
		f := newPairingRESTFixture(t)
		f.endedOwnedAttempt() // ends with the Retry's turn-off
		deleted := httptest.NewRecorder()
		f.api.deleteChannelInstance(deleted, "whatsapp")
		require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())

		rec := f.put("enable", "")
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		present, _ := f.enabledOnDisk()
		require.False(t, present, "the enable did not recreate the deleted connector")
	})
	t.Run("while the delete is in progress", func(t *testing.T) {
		f := newPairingRESTFixture(t)
		f.endedOwnedAttempt()
		built := f.factory.count()
		f.al.BeginPairingDelete("whatsapp")
		rec := f.put("enable", "")
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		_, enabled := f.enabledOnDisk()
		require.False(t, enabled, "nothing was enabled")
		require.Equal(t, built, f.factory.count(), "nothing was started")
		f.al.EndPairingDelete("whatsapp", false)
	})
}

// TestPairingRetryREST_RevokedAfterTheSaveIsReportedTruthfully is gate r4
// T2-SF-15 / R4-A06 (M4, M14): the Retry's enable saved, then the chat's Stop
// revoked the reserved attempt before its commit. The reply is not the
// effect-free 409 ("the channel is now disabled"): it is a 500 saying the
// connection is on and its pairing was cancelled; the setting stays saved,
// nothing starts, and the chat can Retry again.
func TestPairingRetryREST_RevokedAfterTheSaveIsReportedTruthfully(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	built := f.factory.count()
	enableSavedHook = func(string) { f.stopChat(f.chatA) }
	t.Cleanup(func() { enableSavedHook = nil })

	rec := f.put("enable", retryBody("req-a", f.chatA))
	enableSavedHook = nil
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Equal(t, cancelledText, decodeError(t, rec).Error)
	_, enabled := f.enabledOnDisk()
	require.True(t, enabled, "the enabled setting stays saved, as the reply says")
	require.Equal(t, built, f.factory.count(), "no pairing started, no fallback")
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeStopped, latest.Outcome)

	require.Equal(t, http.StatusOK, f.put("disable", "").Code)
	require.Equal(t, http.StatusOK, f.put("enable", retryBody("req-a", f.chatA)).Code, "the chat can Retry again")
	require.Equal(t, "req-a", f.await(channels.PairingStatusCode).RequestID)
}

// TestPairingRetryREST_FailedSaveReleasesTheReservation is pr-test R3-T2
// (residual): the Retry was admitted, then its configuration save failed. The
// reply is the save failure, the reservation is released (not left pending),
// and once storage is repaired the same chat's Retry starts a fresh owned
// attempt.
func TestPairingRetryREST_FailedSaveReleasesTheReservation(t *testing.T) {
	f := newPairingRESTFixture(t)
	ended := f.endedOwnedAttempt()
	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, info.Mode().Perm()) })

	rec := f.put("enable", retryBody("req-a", f.chatA))
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	require.Equal(t, http.StatusInternalServerError, rec.Code, "instrument: the save failed: %s", rec.Body.String())
	_, enabled := f.enabledOnDisk()
	require.False(t, enabled, "nothing was saved")
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.NotEqual(t, ended, latest.AttemptID, "the Retry was admitted")
	require.Equal(t, channels.PairingPhaseTerminal, latest.Phase, "and its reservation released, not left pending")

	require.Equal(t, http.StatusOK, f.put("enable", retryBody("req-a", f.chatA)).Code)
	code := f.await(channels.PairingStatusCode)
	require.Equal(t, "req-a", code.RequestID)
}

// TestPairingRetryREST_StopDuringADelayedStartStartsNoPairing is gate r4
// R4-A01 / pr-test R4-T1 (M2, M7, M14): the Retry committed, its reload is
// slow to start the connector, and the chat presses Stop meanwhile. When the
// reload finally starts the connector, no pairing runs — no fresh unowned
// code the stopped chat could not end — and nothing new is minted.
func TestPairingRetryREST_StopDuringADelayedStartStartsNoPairing(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	prev := reloadWaitTimeout
	reloadWaitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { reloadWaitTimeout = prev })
	gate := make(chan struct{})
	f.factory.mu.Lock()
	f.factory.gate = gate
	f.factory.mu.Unlock()
	released := false
	release := func() {
		if !released {
			released = true
			f.factory.mu.Lock()
			f.factory.gate = nil
			f.factory.mu.Unlock()
			close(gate)
		}
	}
	t.Cleanup(release)

	rec := f.put("enable", retryBody("req-a", f.chatA))
	require.Equal(t, http.StatusInternalServerError, rec.Code, "instrument: the reload is held: %s", rec.Body.String())
	retry, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, channels.PairingPhaseReady, retry.Phase)
	f.stopChat(f.chatA)
	require.Equal(t, "req-a", f.await(channels.PairingStatusStopped).RequestID, "the Stop revoked the waiting attempt")

	release()
	reloadWaitTimeout = prev // the shortened wait only forced the unconfirmed reply; settle with the normal one
	require.NoError(t, waitForReload(f.al, f.svc.reloadOutcome))
	ch := f.factory.latest(t)
	ch.mu.Lock()
	idle, attempt := ch.idleStarts, ch.attemptID
	ch.mu.Unlock()
	require.Equal(t, 1, idle, "the delayed start ran without a pairing")
	require.Empty(t, attempt)
	latest, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, retry.AttemptID, latest.AttemptID, "no fallback attempt was minted")
	require.Equal(t, channels.PairingOutcomeStopped, latest.Outcome)
}

// TestPairingREST_DisableAndDeleteTellTheChat is the gate r4 sibling of
// T2-SF-16 / R4-A01: a disable that revokes a chat's waiting Retry, and a
// delete that cuts off a running pairing, each tell that chat the pairing was
// interrupted — no card waits on a code that cannot come.
func TestPairingREST_DisableAndDeleteTellTheChat(t *testing.T) {
	const interrupted = "This pairing was interrupted because the WhatsApp connection was changed or restarted. " +
		"Press Retry to get a new code."
	t.Run("disable revokes a waiting Retry", func(t *testing.T) {
		f := newPairingRESTFixture(t)
		f.endedOwnedAttempt()
		connect, err := f.al.AdmitConnectPairing("whatsapp", "req-b", f.chatB)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, f.put("disable", "").Code)
		frame := f.await(channels.PairingStatusError)
		require.Equal(t, "req-b", frame.RequestID, "the waiting chat is told")
		require.Equal(t, interrupted, frame.Message)
		got, ok := f.store.Attempt("whatsapp", connect)
		require.True(t, ok)
		require.Equal(t, channels.PairingOutcomeInterrupted, got.Outcome)
	})
	t.Run("delete cuts off a running pairing", func(t *testing.T) {
		f := newPairingRESTFixture(t)
		f.endedOwnedAttempt()
		require.Equal(t, http.StatusOK, f.put("enable", retryBody("req-a", f.chatA)).Code)
		f.await(channels.PairingStatusCode)
		deleted := httptest.NewRecorder()
		f.api.deleteChannelInstance(deleted, "whatsapp")
		require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
		frame := f.await(channels.PairingStatusError)
		require.Equal(t, "req-a", frame.RequestID)
		require.Equal(t, interrupted, frame.Message)
	})
}
