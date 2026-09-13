//go:build !race

package agent

// chokePointRaceDetector is false when compiled without -race. See the
// race-tagged sibling for why the 8 MB archive round-trip is gated.
const chokePointRaceDetector = false
