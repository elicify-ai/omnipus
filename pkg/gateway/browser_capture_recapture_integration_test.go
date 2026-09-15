package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestCaptureIngestWirePendingGeometryKeepsSocket(t *testing.T) {
	cs, _, url := ingestWireFixture(t)
	_, err := cs.BeginFrameTransition("page-b", 0, 0, 1)
	require.NoError(t, err)
	conn := ingestWireConnect(t, cs, url)
	before := cs.LastPingAt()
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"browser_capture_control","action":"ping"}`)))
	require.Eventually(t, func() bool { return cs.LastPingAt().After(before) }, time.Second, time.Millisecond,
		"waiting for measured geometry must keep the authenticated socket and heartbeat reader alive")
	frame, err := cs.BeginFrameTransition("page-b", 756, 413, 1.25)
	require.NoError(t, err)
	require.True(t, cs.RecaptureFrameContext(context.Background(), frame))
	require.Equal(t, map[string]any{
		"type": "browser_capture_control", "action": "recapture", "capture_generation": float64(3),
		"target_id": "page-b", "expected_width": float64(756), "expected_height": float64(413), "capture_scale": 1.25,
	}, ingestWireRead(t, conn, "browser_capture_control"))
}
