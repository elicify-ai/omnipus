package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/require"
)

// Dedicated input is mandatory even before an offer or attachment exists.
// The authenticated socket and readLoop remain real; each rejection is an
// explicit completion witness rather than a timeout with no observed input.
func TestBrowserWSRejectsGesturesBeforeDedicatedOffer(t *testing.T) {
	conn := dialAuthedBrowserWS(t)
	for _, kind := range []string{"mouse_move", "mouse_down", "mouse_up", "wheel", "key_down", "key_up", "text"} {
		t.Run(kind, func(t *testing.T) {
			require.NoError(t, conn.WriteJSON(map[string]any{
				"type": "browser_input", "kind": kind,
				"x": 12, "y": 34, "button": "left", "key": "ArrowLeft", "text": "hello",
			}))
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
			var status generated.BrowserStatusFrame
			require.NoError(t, conn.ReadJSON(&status))
			require.Equal(t, "browser_status", status.Type)
			require.Equal(t, "error", status.State)
			require.NotNil(t, status.Message)
			require.Equal(t, "This attachment accepts gestures only on its dedicated input connection.", *status.Message)
			require.NotNil(t, status.OperationOnly)
			require.True(t, *status.OperationOnly, "gesture refusal must not retire the healthy browser socket")
		})
	}
	assertReadLoopStillReads(t, conn)
}

func TestBrowserMediaOfferCannotEnableLegacyGestures(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	require.Nil(t, f.state.dedicatedInput(), "exercise the gap before any dedicated peer exists")
	source, _ := admittedInputRoute(t, f, "media-only-viewer")
	dispatched := 0
	sink := newWebRTCContextInputSinkWithDispatch(true, func(context.Context, *browser.BrowserManager, string, string, browser.LiveInput) error {
		dispatched++
		return nil
	})
	sink(source, "media-only-viewer", []byte(`{"type":"browser_input","kind":"key_down","key":"ArrowLeft"}`))
	require.Zero(t, dispatched, "a real admitted media offer must not authorize the legacy input sink")
	require.NoError(t, source.Err(), "refused media input must preserve media lifetime")
}
