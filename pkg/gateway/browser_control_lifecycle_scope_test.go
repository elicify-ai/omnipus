package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

func TestBrowserControlLifecycleQueuedStatusTracksCurrentOwnership(t *testing.T) {
	for _, kind := range []string{"controlling", "released"} {
		t.Run(kind, func(t *testing.T) {
			wc, state := newControlTestFixtures(t)
			t.Cleanup(state.mgr.Live().Shutdown)
			scope, cancel := context.WithCancel(context.Background())
			defer cancel()
			acquired, released := browserControlLifecycleCallbacks(wc, state.mgr, state.panelSessionID, state.sessionID, "human", "user", scope)
			if kind == "controlling" {
				require.True(t, state.mgr.Live().TakeControl(state.panelSessionID, "human"))
				acquired()
			} else {
				released()
			}
			var frame browserOutboundFrame
			select {
			case frame = <-wc.sendCh:
			default:
				t.Fatal("current status was not queued")
			}
			var status generated.BrowserStatusFrame
			require.NoError(t, json.Unmarshal(frame.data, &status))
			require.Equal(t, kind, status.State)
			require.True(t, wc.canSendFrame(frame), "valid queued status must remain deliverable")
			if kind == "controlling" {
				state.mgr.Live().ReleaseStoodDown(state.panelSessionID)
			} else {
				require.True(t, state.mgr.Live().TakeControl(state.panelSessionID, "human"))
			}
			require.False(t, wc.canSendFrame(frame), "writer must discard status invalidated after enqueue")
			// A delayed callback must also fail publication admission after transition.
			if kind == "controlling" {
				acquired()
			} else {
				released()
			}
			require.Empty(t, wc.sendCh)
		})
	}
}
