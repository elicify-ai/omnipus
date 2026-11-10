package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// DESIGN §3 reload/restart/settlement identity, §5 T4. The real manager and
// native adapter run; only the external HTTP dial fails deliberately. This
// detects which exact reservation/resource reaches Start without inventing a
// successful native login. Successful text/media publication is not claimed.
func TestChannelOrdering_StaleReloadAndSettlementLeaveSuccessorAlone(t *testing.T) {
	for _, variant := range []string{"same settings", "different revisions", "identical-config ABA", "delete and recreate"} {
		t.Run(variant, func(t *testing.T) {
			f, store, _ := orderingWriterFixture(t)
			mgr := f.svc.ChannelManager
			// whatsmeow clones *http.Transport; the broader fixture's custom
			// RoundTripper is not that type. Install the process-edge instrument
			// before the first native construction, not inside the native code.
			originalTransport := http.DefaultTransport
			transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("ordering external transport refused")
			}}
			http.DefaultTransport = transport
			t.Cleanup(func() { http.DefaultTransport = originalTransport; transport.CloseIdleConnections() })
			// Establish the real idle native resource/hash. With no admitted token
			// an unpaired native Start does not contact the platform.
			require.NoError(t, mgr.Reload(context.Background(), f.al.GetConfig(), f.svc.bundle))
			oldResource, exists := mgr.GetChannel("whatsapp")
			require.True(t, exists)
			originalRevision := channels.ChannelRevision(f.al.GetConfig(), "whatsapp")

			a := orderingReady(t, f, "req-a", orderingChat(t, f))
			captured, releaseOld, joinedOld := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(joinedOld)
				close(captured) // A retained exactly its own restart/settlement token.
				<-releaseOld
				mgr.RequestRestart("whatsapp", a.AttemptID)
				f.al.SettlePairing("whatsapp", a.AttemptID)
			}()
			var once sync.Once
			release := func() { once.Do(func() { close(releaseOld) }) }
			t.Cleanup(func() { release(); orderingJoin(t, joinedOld) })
			orderingWaitStage(t, captured)
			require.True(t, store.Release("whatsapp", a.AttemptID, channels.PairingOutcomeInterrupted))

			switch variant {
			case "different revisions":
				rec := f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", `{"allow_from":["new-settings"]}`)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.NotEqual(t, originalRevision, channels.ChannelRevision(f.al.GetConfig(), "whatsapp"))
			case "identical-config ABA":
				require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", `{"allow_from":["intermediate"]}`).Code)
				require.NotEqual(t, originalRevision, channels.ChannelRevision(f.al.GetConfig(), "whatsapp"))
				require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", `{"allow_from":[]}`).Code)
				require.Equal(t, originalRevision, channels.ChannelRevision(f.al.GetConfig(), "whatsapp"), "instrument: settings returned to the same hash")
			case "delete and recreate":
				// Drive the real deletion handler, not the fixture's dev-bypass
				// authorization route (which intentionally refuses DELETE).
				deleted := httptest.NewRecorder()
				f.api.deleteChannelInstance(deleted, "whatsapp")
				require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
				require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", "{}").Code)
				require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/enable", "").Code)
			}

			b := orderingReady(t, f, "req-b", orderingChat(t, f))
			mgr.RequestRestart("whatsapp", b.AttemptID)
			release()
			orderingJoin(t, joinedOld)
			stillReady, exists := store.Attempt("whatsapp", b.AttemptID)
			require.True(t, exists)
			require.Equal(t, b, stillReady, "A's settlement must not release B")

			require.NoError(t, mgr.Reload(context.Background(), f.al.GetConfig(), f.svc.bundle))
			current, exists := store.Latest("whatsapp")
			require.True(t, exists)
			assert.Equal(t, b.AttemptID, current.AttemptID, "no fallback token is minted")
			assert.Equal(t, b.Owner, current.Owner)
			assert.Equal(t, channels.PairingPhaseTerminal, current.Phase, "B's own native start must run and report the controlled external failure, not stay ready because A overwrote its restart")
			assert.ErrorContains(t, mgr.LastStartError("whatsapp"), "ordering external transport refused", "positive control: actual native transport was reached")
			newResource, exists := mgr.GetChannel("whatsapp")
			require.True(t, exists)
			assert.NotSame(t, oldResource, newResource, "only B's accepted restart replaces the old native resource")
		})
	}
}
