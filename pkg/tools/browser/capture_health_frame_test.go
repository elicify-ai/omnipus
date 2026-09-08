package browser

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureHealthFrameKeepsSocketLivenessSeparateFromEvidence(t *testing.T) {
	for _, scenario := range []string{"current", "missing generation", "stale generation", "missing target", "wrong target", "bare ping", "unmeasured layout"} {
		t.Run(scenario, func(t *testing.T) {
			cs, _ := adapterFixture(t)
			epoch := adapterBind(t, cs, context.Background())
			original := CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "live", PeerState: "connected", SampleTimestampMS: 100}
			require.True(t, cs.RecordIngestHeartbeat(epoch, &original))
			before := cs.CaptureHealth()
			late := CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 48, TrackState: "ended", SampleTimestampMS: 200}
			var sample *CaptureHealthObservation = &late
			switch scenario {
			case "missing generation":
				late.CaptureGeneration = 0
			case "stale generation":
				late.CaptureGeneration = 2
			case "missing target":
				late.TargetID = ""
			case "wrong target":
				late.TargetID = "page-b"
			case "bare ping":
				sample = nil
			case "unmeasured layout":
				pending, err := cs.BeginFrameTransition("page-a", 0, 0, 1)
				require.NoError(t, err)
				late.CaptureGeneration = pending.Generation
				before = CaptureHealthObservation{}
				require.Equal(t, before, cs.CaptureHealth(), "new frame retires old evidence before any later heartbeat")
			}
			cs.mu.Lock()
			cs.lastPingAt = time.Unix(100, 0)
			cs.mu.Unlock()
			require.True(t, cs.RecordIngestHeartbeat(epoch, sample), "current socket remains alive even while a new frame is pending")
			require.True(t, cs.LastPingAt().After(time.Unix(100, 0)))
			got := cs.CaptureHealth()
			if scenario == "current" {
				require.Equal(t, int64(48), got.Generation, "local attempt identity stays distinct")
				require.Equal(t, uint64(1), got.CaptureGeneration)
				require.Equal(t, "page-a", got.TargetID)
				require.Equal(t, epoch, got.BindingEpoch)
				require.Equal(t, float64(200), got.SampleTimestampMS)
			} else {
				require.Equal(t, before, got, "unqualified evidence must not alter retained sample or its freshness")
			}
		})
	}
}

func TestCaptureHealthFrameStaleSampleCannotClaimNewFrameRecovery(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, relay := adapterFixture(t)
	epoch := adapterBind(t, cs, context.Background())
	failed := CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "ended"}
	require.True(t, cs.RecordIngestHeartbeat(epoch, &failed))
	sample := cs.CaptureHealth()
	_, err := cs.BeginFrameTransition("page-b", 800, 600, 1)
	require.NoError(t, err)
	require.False(t, cs.ReportCaptureFailureForObservation(sample), "old frame evidence cannot trigger recovery of its replacement")
	require.Zero(t, relay.recaptureCount())
}
