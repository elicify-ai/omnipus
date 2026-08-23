package webrtc

import "time"

// ADR-069 Finding 2: the encoder congestion-controls against the WRONG link.
//
// The capture page's PeerConnection is loopback (gateway <-> its own headless
// Chrome): infinite bandwidth, zero loss. Chrome therefore encodes for that
// hop, up to whatever ceiling applyVideoSenderConstraints set, and the gateway
// relays the result to a viewer whose real path cannot carry it. Measured from
// macOS to the ams UAT box: 27.6% packet loss, 1-6 fps, 7 freezes.
//
// The viewer leg already receives the missing evidence -- RTCP receiver
// reports carry fraction-lost and jitter -- it was just being dropped. This
// file turns those reports into a bitrate target the encoder can act on.
//
// The policy is AIMD, the same shape every congestion controller uses: back
// off fast when the receiver reports loss, recover slowly when it does not.
// It is a PURE function of (previous target, sample) so it can be tested
// exhaustively without a network, a browser, or a clock.
const (
	// bitrateFloor is the lowest target we will ever ask for. Below this the
	// picture is not worth showing, and a viewer this constrained is better
	// served by an honest failure than by a slideshow.
	bitrateFloor = 300_000

	// bitrateCeiling bounds the recovery climb. It matches the encoder's own
	// maximum (6 Mbps base * 4, applyVideoSenderConstraints) so the controller
	// can never ask for more than the encoder would have chosen unprompted.
	bitrateCeiling = 24_000_000

	// lossBackoffThreshold is the fraction of packets lost, over one report
	// interval, above which we treat the link as congested. RTCP encodes
	// fraction-lost as 0-255; 5% is the conventional "this is real congestion,
	// not a blip" line used by REMB/GCC implementations.
	lossBackoffThreshold = 0.05

	// lossRecoveryThreshold is the fraction below which we consider the link
	// healthy enough to climb again. The gap between the two thresholds is
	// deliberate hysteresis: without it a link sitting exactly at the boundary
	// oscillates between backing off and climbing every report.
	lossRecoveryThreshold = 0.02

	// backoffFactor is the multiplicative decrease. 0.7 halves the bitrate in
	// two consecutive bad reports rather than one, which keeps a single lost
	// burst from collapsing an otherwise fine stream.
	backoffFactor = 0.7

	// recoveryStep is the additive increase per healthy report.
	recoveryStep = 250_000

	// bitrateUpdateMinInterval throttles how often a new target is pushed to
	// the encoder. Every push costs a setParameters on the sender; RTCP
	// receiver reports arrive roughly every second, and reacting to each one
	// would be both noisy and pointless.
	bitrateUpdateMinInterval = 2 * time.Second
)

// bitrateSample is one viewer's RTCP evidence for one interval.
type bitrateSample struct {
	// FractionLost is 0..1 (RTCP's 0-255 byte, already normalized).
	FractionLost float64
}

// nextBitrateTarget applies the AIMD policy. prev <= 0 means "no target yet",
// which starts at the ceiling: absent evidence of congestion we must not
// throttle a link that is perfectly capable, and the encoder's own ceiling is
// exactly the no-evidence default.
func nextBitrateTarget(prev int, s bitrateSample) int {
	if prev <= 0 {
		prev = bitrateCeiling
	}
	switch {
	case s.FractionLost >= lossBackoffThreshold:
		next := int(float64(prev) * backoffFactor)
		if next < bitrateFloor {
			next = bitrateFloor
		}
		return next
	case s.FractionLost <= lossRecoveryThreshold:
		next := prev + recoveryStep
		if next > bitrateCeiling {
			next = bitrateCeiling
		}
		return next
	default:
		// Between the thresholds: hold. This is the hysteresis band.
		return prev
	}
}

// normalizeFractionLost converts RTCP's 8-bit fraction to 0..1.
func normalizeFractionLost(raw uint8) float64 {
	return float64(raw) / 256.0
}
