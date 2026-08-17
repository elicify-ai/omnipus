package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// The UAT install measured on 2026-08-15: a 633x686 panel produced a
// 1266x1372 capture while Page.getLayoutMetrics reported a 633x543 layout
// viewport. Ground truth (hit-testing the live tab through /browser/inspect:
// elements resolved up to y=660, none from y=686) proved the tab's real
// viewport was ~686 CSS tall, so the capture was right and the layout-metrics
// read was wrong. Mapping input through the wrong number sent every click
// ~21% of the frame height too high and made the bottom fifth of the page
// unreachable — the operator's "mouse clicks are not working" on the hosted
// install, invisible on macOS where the read-back agrees with the capture.
func TestViewportBasisPrefersCaptureWhenLayoutViewportDisagrees(t *testing.T) {
	// Updated 2026-08-16: the basis is no longer inferred from the capture's
	// own dimensions divided by the recorded scale (that inference assumed the
	// capture always depicts the tab 1:1, which encoder.js documents as false —
	// it letterboxes). It now asks the TAB, once, and believes whichever side
	// the tab backs. Here the tab reports the 686 that /browser/inspect proved
	// live, i.e. it backs the capture, so the stale cache is refreshed and the
	// clicks land in the same place this test always demanded.
	lv := &LiveView{
		sessionID: "s1",
		viewers:   make(map[string]struct{}),
		runCDP: func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if lm, ok := actions[0].(layoutMetricsAction); ok {
				*lm.w, *lm.h = 633, 686 // ground truth, per the hit-test above
			}
			return nil
		},
	}
	lv.cssViewportW, lv.cssViewportH = 633, 543
	lv.cssViewportScale = 2

	const capW, capH = 1266.0, 1372.0

	// Driven through the REAL input path, not the helper: with the cache
	// populated rescaleToCSSViewport issues no CDP call, so a plain context
	// is enough. Calling the helper directly would still pass if the call
	// site were removed, which is exactly the wiring this pins.
	_, ry, ok := lv.rescaleToCSSViewport(context.Background(), 633, 1032, capW, capH)
	if !ok {
		t.Fatal("rescaleToCSSViewport refused a point with the viewport cached")
	}
	// A click aimed at the "Check out our API" link, which the capture shows
	// at y=1032, must land on it at CSS y=516 — not at 408, four rows up,
	// which is where the stale 543 basis sent it.
	if ry < 515 || ry > 517 {
		t.Fatalf("click at capture y=1032 mapped to CSS y=%v, want ~516", ry)
	}

	// The bottom of the frame must reach the bottom of the page.
	_, bottom, ok := lv.rescaleToCSSViewport(context.Background(), 633, capH, capW, capH)
	if !ok {
		t.Fatal("rescaleToCSSViewport refused the frame's bottom edge")
	}
	if bottom < 685 || bottom > 687 {
		t.Fatalf("bottom of the capture mapped to CSS y=%v, want ~686 (the page bottom must be clickable)", bottom)
	}
}

// The Fault-3 case this guard must NOT disturb: the encoder downscales the
// stream under CPU pressure, so the capture is far smaller than the page but
// still the SAME surface (aspect preserved). The layout viewport stays the
// correct basis there — switching to capture/scale would mis-map by the
// downscale factor.
func TestViewportBasisKeepsLayoutViewportWhenEncoderDownscales(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}
	lv.cssViewportW, lv.cssViewportH = 1280, 720
	lv.cssViewportScale = 2

	w, h := lv.viewportBasisForCapture(context.Background(), 320, 180, 1280, 720)
	if w != 1280 || h != 720 {
		t.Fatalf("basis = %vx%v, want the layout viewport 1280x720 for an aspect-preserving downscale", w, h)
	}
	rx, ry, ok := lv.rescaleToCSSViewport(context.Background(), 160, 90, 320, 180)
	if !ok {
		t.Fatal("rescaleToCSSViewport refused a point with the viewport cached")
	}
	if rx != 640 || ry != 360 {
		t.Fatalf("centre of a downscaled capture mapped to (%v,%v), want (640,360)", rx, ry)
	}
}

// With no recorded device scale factor there is nothing better to switch to,
// so the pre-existing behaviour (map through the layout viewport) must hold
// rather than the code guessing a scale.
func TestViewportBasisKeepsLayoutViewportWithoutRecordedScale(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}
	lv.cssViewportW, lv.cssViewportH = 633, 543

	w, h := lv.viewportBasisForCapture(context.Background(), 1266, 1372, 633, 543)
	if w != 633 || h != 543 {
		t.Fatalf("basis = %vx%v, want the cached 633x543 when no scale is recorded", w, h)
	}
}

// invalidateCSSViewportCache must clear the scale too: a scale left behind
// from a previous viewport would be applied to a capture it no longer
// describes.
func TestInvalidateCSSViewportCacheClearsScale(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}
	lv.cssViewportW, lv.cssViewportH = 633, 686
	lv.cssViewportScale = 2

	lv.invalidateCSSViewportCache()

	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.cssViewportScale != 0 {
		t.Fatalf("cssViewportScale = %v after invalidation, want 0", lv.cssViewportScale)
	}
}
