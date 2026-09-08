package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Capture the context produced by the actual successful offer handler, rather
// than recreating its input routing or error-publication callback in the test.
func admittedInputRoute(t *testing.T, f handlerContextFixture, viewer string) (context.Context, webRTCInputRoute) {
	t.Helper()
	_, err := f.manager.Live().AttachContext(context.Background(), "panel", viewer, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { f.manager.Live().Detach("panel", viewer) })
	epoch := f.state.beginWebRTCOffer()
	f.handler.handleWebRTCOffer(f.conn, f.state, viewer, "user", f.offer(t, nil), f.cfg, epoch)
	f.relay.requestMu.Lock()
	parent := f.relay.parent
	f.relay.requestMu.Unlock()
	require.NotNil(t, parent)
	route, ok := parent.Value(webRTCInputRouteKey{}).(webRTCInputRoute)
	require.True(t, ok)
	require.NotNil(t, route.report)
	require.NotNil(t, f.state.webrtc)
	for len(f.conn.sendCh) > 0 {
		<-f.conn.sendCh
	}
	return parent, route
}
