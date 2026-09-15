package gateway

import (
	"testing"
	"time"
)

// withWarmCaptureAdaptResetMinAge swaps the age gate for the duration of a
// test. Restores the production value even if the test fails.
func withWarmCaptureAdaptResetMinAge(t *testing.T, d time.Duration) {
	t.Helper()
	prev := warmCaptureAdaptResetMinAge
	warmCaptureAdaptResetMinAge = d
	t.Cleanup(func() { warmCaptureAdaptResetMinAge = prev })
}

// TestWarmCaptureHandoverDetectionIsPrompt pins the detection interval. The
// handover is no longer a passive observation — it performs an action on the
// viewer's behalf, and every second of lag lands that action deeper into a
// session the user is already watching instead of during the first, still
// settling moment of an open.
func TestWarmCaptureHandoverDetectionIsPrompt(t *testing.T) {
	if warmCaptureIdleCheckInterval > time.Second {
		t.Fatalf("warm-capture viewer polling is %s — at more than 1s the handover rebuild lands mid-session, "+
			"which is a visible blip in the middle of use rather than during the open", warmCaptureIdleCheckInterval)
	}
}
