package webrtc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ADR-062 Finding 2. Before this controller existed the gateway dropped every
// RTCP receiver report on the floor, so the encoder congestion-controlled
// against the loopback ingest hop and happily produced 24 Mbps for a viewer
// path measured at 355 kbps with 27.6% loss.

func TestNextBitrateTarget_BacksOffOnLoss(t *testing.T) {
	got := nextBitrateTarget(10_000_000, bitrateSample{FractionLost: 0.20})
	require.Less(t, got, 10_000_000, "sustained loss must reduce the target")
	require.GreaterOrEqual(t, got, bitrateFloor)
}

func TestNextBitrateTarget_NeverBelowFloor(t *testing.T) {
	target := bitrateCeiling
	for i := 0; i < 100; i++ {
		target = nextBitrateTarget(target, bitrateSample{FractionLost: 0.9})
	}
	require.Equal(t, bitrateFloor, target,
		"a permanently broken link must settle at the floor, not collapse toward zero")
}

func TestNextBitrateTarget_RecoversButNeverAboveCeiling(t *testing.T) {
	target := bitrateFloor
	for i := 0; i < 500; i++ {
		target = nextBitrateTarget(target, bitrateSample{FractionLost: 0})
	}
	require.Equal(t, bitrateCeiling, target,
		"a healthy link must climb back to, and stop at, the encoder's own ceiling")
}

// The hysteresis band is what keeps a link sitting near the threshold from
// oscillating between backing off and climbing on every report.
func TestNextBitrateTarget_HoldsInsideHysteresisBand(t *testing.T) {
	const start = 5_000_000
	mid := (lossBackoffThreshold + lossRecoveryThreshold) / 2
	require.Equal(t, start, nextBitrateTarget(start, bitrateSample{FractionLost: mid}))
}

func TestNextBitrateTarget_NoEvidenceStartsAtCeiling(t *testing.T) {
	require.Equal(t, bitrateCeiling, nextBitrateTarget(0, bitrateSample{FractionLost: 0}),
		"absent evidence of congestion we must not throttle a capable link")
}

func TestNormalizeFractionLost(t *testing.T) {
	require.InDelta(t, 0.0, normalizeFractionLost(0), 0.001)
	require.InDelta(t, 0.5, normalizeFractionLost(128), 0.01)
}

// noteViewerLoss is the seam the RTCP drain calls. It must push a new target
// to the encoder when the target moves, and must throttle: every push costs a
// setParameters on the sender, and receiver reports arrive about every second.
func TestNoteViewerLoss_PushesTargetAndThrottles(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	var pushes []int
	s.SetOnBitrateTarget(func(bps int) { pushes = append(pushes, bps) })

	base := time.Now()
	// First bad report: target moves down, and the encoder is told.
	first := s.noteViewerLoss(0.30, base)
	require.Less(t, first, bitrateCeiling)
	require.Len(t, pushes, 1, "a moved target must reach the encoder")

	// Immediately after: still bad, target moves again, but the push is
	// throttled rather than firing on every report.
	s.noteViewerLoss(0.30, base.Add(100*time.Millisecond))
	require.Len(t, pushes, 1, "pushes must be throttled, not sent per report")

	// Past the throttle window: allowed again.
	s.noteViewerLoss(0.30, base.Add(bitrateUpdateMinInterval+time.Second))
	require.Len(t, pushes, 2)
	require.Less(t, pushes[1], pushes[0], "continued loss must keep reducing the target")
}

func TestNoteViewerLoss_HealthyLinkDoesNotPushConstantly(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	var pushes int
	s.SetOnBitrateTarget(func(int) { pushes++ })

	// Already at the ceiling with a clean link: the target cannot move, so
	// nothing should ever be sent.
	base := time.Now()
	for i := 0; i < 10; i++ {
		s.noteViewerLoss(0, base.Add(time.Duration(i)*bitrateUpdateMinInterval*2))
	}
	require.Zero(t, pushes, "a healthy link pinned at the ceiling must generate no traffic")
}
