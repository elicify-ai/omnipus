package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestOrdinaryEnableREST_AnUnconfirmedRestartIsReportedTruthfully is gate r5
// R5-A04 (M10, M14): a Connectors enable (absent body or {}) whose reload
// outlasts the wait is not reported as a plain success: it is the 500
// "enabled, but its restart could not be confirmed yet" result, the unowned
// reservation is kept, and its code still appears once the reload starts it.
func TestOrdinaryEnableREST_AnUnconfirmedRestartIsReportedTruthfully(t *testing.T) {
	const unconfirmed = "The WhatsApp connection is enabled, but its restart could not be confirmed yet. " +
		"The new code appears here when the pairing starts."
	for _, body := range []string{"", "{}"} {
		t.Run("body="+body, func(t *testing.T) {
			f := newPairingRESTFixture(t)
			f.endedOwnedAttempt() // ends turned off, as a Connectors Retry's turn-off does
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

			rec := f.put("enable", body)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			require.Equal(t, unconfirmed, decodeError(t, rec).Error)
			latest, ok := f.store.Latest("whatsapp")
			require.True(t, ok)
			require.Equal(t, channels.PairingPhaseReady, latest.Phase, "the reservation is kept")
			require.Nil(t, latest.Owner)

			release()
			code := f.await(channels.PairingStatusCode)
			require.Empty(t, code.RequestID, "the late code is the unowned Connectors pairing")
			reloadWaitTimeout = prev // the shortened wait only forced the unconfirmed reply; settle with the normal one
			require.NoError(t, waitForReload(f.al, f.svc.reloadOutcome))
		})
	}
}

// TestDisableREST_AFailedDisableRevokesNothing is the REST sibling of gate r5
// R5-A03 / T2-SF-18 (M2): a disable whose save fails changes nothing — a
// chat's waiting admission stays ready and nothing is published. (An
// accepted disable revoking it is TestPairingREST_DisableAndDeleteTellTheChat.)
func TestDisableREST_AFailedDisableRevokesNothing(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	require.Equal(t, http.StatusOK, f.put("enable", "").Code)
	f.await(channels.PairingStatusCode)
	f.factory.latest(t).timeout()
	connect, err := f.al.AdmitConnectPairing("whatsapp", "req-b", f.chatB)
	require.NoError(t, err)

	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, info.Mode().Perm()) })
	rec := f.put("disable", "")
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	require.Equal(t, http.StatusInternalServerError, rec.Code, "instrument: the save failed: %s", rec.Body.String())

	got, ok := f.store.Attempt("whatsapp", connect)
	require.True(t, ok)
	require.Equal(t, channels.PairingPhasePending, got.Phase, "the refused disable revoked nothing")
	quiet := time.After(300 * time.Millisecond)
	for {
		select {
		case evt := <-f.events.C:
			if p, isPairing := evt.Payload.(agent.WhatsAppPairingPayload); isPairing && p.RequestID == "req-b" {
				t.Fatalf("the refused disable published %+v", p)
			}
		case <-quiet:
			return
		}
	}
}
