package gateway

// Round-2 finding F2 — the handover half of the fix.
//
// The defect: warm-boot step 2 starts a REAL capture at boot, with no viewer,
// by default, for up to five minutes. The encoder page runs a bounded
// resolution-adaptation loop that steps the picture down whenever the encoder
// reports it is CPU-limited — and a boot-warmed capture is a full software
// encode running during the busiest minute of the process's life, watched by
// nobody. On a hosted Linux box it can reach the loop's hard floor (a QUARTER
// of the pixels) within seconds. The user's FIRST panel open then rendered at
// that resolution: a decision taken about a stream no human ever saw.
//
// The gateway's half of the fix is the only half that can exist here, because
// viewers are the one thing the encoder page cannot see: when a real viewer
// adopts a capture that has been warming unwatched for longer than
// warmCaptureAdaptResetMinAge, tell the encoder to RESET ITS ADAPTATION so the
// viewer starts at full quality. A viewer who arrives before any adaptation
// could have happened must NOT be sent one — that is the case the warm-up was
// built for (6,655ms -> 1,041ms to first frame).
//
// CHANGED 2026-08-19: the handover used to force a capture REBUILD. Measured
// on the hosted box, that cost ~17s to first frame against ~4s without it,
// which made keeping a warm capture alive past its idle window worse than
// letting it stop — the opposite of the point. adapt_reset restores full
// quality without tearing the capture down.

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
