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
	"context"
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

// TestWatchWarmCaptureIdle_HandoverAfterUnwatchedRunResetsAdaptation is the F2
// guarantee: a viewer adopting a capture that has been encoding unwatched must
// not inherit whatever resolution that unwatched run settled on — and must get
// that guarantee WITHOUT a capture rebuild.
func TestWatchWarmCaptureIdle_HandoverAfterUnwatchedRunResetsAdaptation(t *testing.T) {
	// 0 means "any age qualifies", i.e. this handover is the
	// warmed-long-enough-to-have-adapted case.
	withWarmCaptureAdaptResetMinAge(t, 0)

	cs := newFakeWarmCapture()
	cs.viewers.Store(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Hour, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned after a viewer attached")
	}
	if got := cs.recaptures.Load(); got != 1 {
		t.Fatalf("expected exactly ONE handover recapture so the viewer starts at full quality, got %d — "+
			"without it the first panel open keeps the resolution an unwatched, boot-contended warm-up settled on", got)
	}
	if got := cs.stops.Load(); got != 0 {
		t.Fatalf("a capture a viewer is watching must never be stopped, got %d stop(s)", got)
	}
}

// TestWatchWarmCaptureIdle_HandoverBeforeAdaptationCouldHappenIsFree protects
// the measured win. A panel opened seconds after boot cannot have inherited an
// adaptation — the loop only starts on the PeerConnection's first 'connected'
// transition and needs two further 2s samples before it can step — so the
// handover must cost that viewer nothing at all.
func TestWatchWarmCaptureIdle_HandoverBeforeAdaptationCouldHappenIsFree(t *testing.T) {
	// An hour: no capture in this test has been warming that long, so every
	// handover here is the "arrived early" case.
	withWarmCaptureAdaptResetMinAge(t, time.Hour)

	cs := newFakeWarmCapture()
	cs.viewers.Store(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Hour, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned after a viewer attached")
	}
	if got := cs.recaptures.Load(); got != 0 {
		t.Fatalf("a viewer who arrived before any adaptation was possible must not pay for a rebuild, got %d recapture(s) — "+
			"that rebuild is the boot-warm-up's whole latency win being spent on resetting nothing", got)
	}
	if got := cs.stops.Load(); got != 0 {
		t.Fatalf("a capture a viewer is watching must never be stopped, got %d stop(s)", got)
	}
}

// TestWatchWarmCaptureIdle_IdleStopDoesNotRecapture — the OTHER exit. A
// capture nobody ever watched is stopped, not rebuilt: sending a recapture to
// an encoder page that is about to be torn down is pure waste, and would burn
// exactly the CPU the idle stop exists to release.
func TestWatchWarmCaptureIdle_IdleStopDoesNotRecapture(t *testing.T) {
	withWarmCaptureAdaptResetMinAge(t, 0)

	cs := newFakeWarmCapture()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Millisecond, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned")
	}
	if got := cs.stops.Load(); got != 1 {
		t.Fatalf("expected the viewerless warm capture to be stopped exactly once, got %d", got)
	}
	if got := cs.recaptures.Load(); got != 0 {
		t.Fatalf("an idle-stopped capture must not be recaptured on the way out, got %d", got)
	}
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
