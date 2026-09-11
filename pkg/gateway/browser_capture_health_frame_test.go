package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureHealthFrameWirePreservesBothGenerations(t *testing.T) {
	cs, _, url := ingestWireFixture(t)
	conn := ingestWireConnect(t, cs, url)
	ingestWireRead(t, conn, "browser_capture_control")
	require.NoError(t, conn.WriteJSON(map[string]any{
		"type": "browser_capture_control", "action": "ping", "capture_generation": 1, "target_id": "page-a",
		"capture_health": map[string]any{"generation": 47, "track_state": "live", "track_muted": false, "peer_state": "connected", "sample_timestamp_ms": 123},
	}))
	require.Eventually(t, func() bool { return cs.CaptureHealth().SampleTimestampMS == 123 }, time.Second, time.Millisecond)
	sample := cs.CaptureHealth()
	require.Equal(t, int64(47), sample.Generation)
	require.Equal(t, uint64(1), sample.CaptureGeneration)
	require.Equal(t, "page-a", sample.TargetID)
	require.Equal(t, uint64(1), sample.BindingEpoch)
}

func TestCaptureHealthFrameWireStaleEnvelopeKeepsOnlySocketAlive(t *testing.T) {
	for _, scenario := range []string{"missing generation", "stale generation", "missing target", "wrong target", "bare ping"} {
		t.Run(scenario, func(t *testing.T) {
			cs, _, url := ingestWireFixture(t)
			conn := ingestWireConnect(t, cs, url)
			ingestWireRead(t, conn, "browser_capture_control")
			frame := map[string]any{"type": "browser_capture_control", "action": "ping", "capture_generation": 1, "target_id": "page-a", "capture_health": map[string]any{"generation": 47, "track_state": "live", "track_muted": false, "peer_state": "connected", "sample_timestamp_ms": 123}}
			require.NoError(t, conn.WriteJSON(frame))
			require.Eventually(t, func() bool { return cs.CaptureHealth().SampleTimestampMS == 123 }, time.Second, time.Millisecond)
			before := cs.CaptureHealth()
			switch scenario {
			case "missing generation":
				delete(frame, "capture_generation")
			case "stale generation":
				frame["capture_generation"] = 2
			case "missing target":
				delete(frame, "target_id")
			case "wrong target":
				frame["target_id"] = "page-b"
			}
			frame["capture_health"] = map[string]any{"generation": 48, "track_state": "ended", "track_muted": false, "peer_state": "failed", "sample_timestamp_ms": 200}
			if scenario == "bare ping" {
				delete(frame, "capture_health")
			}
			last := cs.LastPingAt()
			require.NoError(t, conn.WriteJSON(frame))
			require.Eventually(t, func() bool { return cs.LastPingAt().After(last) }, time.Second, time.Millisecond, "valid socket ping survives frame transition")
			require.Equal(t, before, cs.CaptureHealth(), "old or missing frame identity must not refresh stored evidence")
		})
	}
}
