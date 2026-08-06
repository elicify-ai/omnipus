package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Device-matrix regression coverage for the live-panel viewport.
//
// WHY THIS FILE EXISTS: every viewport bug the operator hit only manifested at
// deviceScaleFactor 2 (a Retina client), while every pre-existing test ran at
// an implicit DPR 1 — so the whole class was invisible in CI. Measured on the
// operator's own session, the server logged:
//
//	requested 654 -> actual 511   (-143)
//	requested 512 -> actual 369   (-143)
//	requested 575 -> actual 432   (-143)
//
// a flat -143px gap WITH compensated:true. Reconstructing what compensation
// actually asked for shows the real mechanism:
//
//	orig 654 -> compensated ask 797 -> got 511   (286 short)
//	orig 512 -> compensated ask 655 -> got 369   (286 short)
//	orig 575 -> compensated ask 718 -> got 432   (286 short)
//
// The true chrome delta is 286 = 143 x 2 (the deviceScaleFactor), but the
// compensator sizes its correction from the 143 visible in the first read-back
// — so it under-corrects by exactly half, on every attempt, forever. Single-shot
// therefore never converged at DSF 2 and the panel rendered short and shrank.
//
// These tests model that: a deficit applied to EVERY request including the
// compensated one, across the DPR values real clients report.
func TestSetViewport_DeviceMatrix_BoundedWhenChromeRefuses(t *testing.T) {
	for _, dpr := range []float64{1, 2, 3} {
		t.Run("dpr"+trimFloat(dpr), func(t *testing.T) {
			var bounds int
			runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
				if _, ok := actions[0].(windowBoundsAction); ok {
					bounds++
					return nil
				}
				if lm, ok := actions[0].(layoutMetricsAction); ok {
					*lm.w, *lm.h = 603, 300 // immovable: never grows, whatever we ask
					return nil
				}
				return nil
			}
			reg, lv := newViewportTestLiveView(runCDP)

			applied, err := reg.SetViewport("s1", 603, 900, dpr)
			require.NoError(t, err)
			require.True(t, applied, "an unreachable target must still apply, not error")

			// Exactly two window-bounds calls: the initial apply plus ONE
			// compensation attempt. Not iterated — the v52 diagnostics showed a
			// second setWindowBounds changes nothing (post-compensation
			// read-back == pre-compensation read-back), so a loop would repeat
			// a no-op. Asserted as an exact value: a loose upper bound would
			// pass against a loop and guard nothing.
			assert.Equal(t, 2, bounds,
				"compensation must be a single pass — the initial apply plus one attempt, got %d", bounds)

			lv.mu.Lock()
			gotH := lv.cssViewportH
			lv.mu.Unlock()
			assert.Equal(t, 300, gotH,
				"when the target is unreachable the cache must hold the REAL viewport, so input mapping "+
					"stays correct even though the panel cannot reach its requested size")
		})
	}
}

// TestSetViewport_IgnoredResize_KeepsTheTrueViewport pins the MEASURED failure
// (v52 diagnostics, deviceScaleFactor 1):
//
//	requested 587 -> first read-back 444 -> asked 730 -> still 444
//	requested 564 -> first read-back 421 -> asked 707 -> still 421
//
// The compensating setWindowBounds changes NOTHING — the post-compensation
// read-back equals the pre-compensation one exactly. Chrome ignores the resize
// rather than partially honoring it, which is why iterating cannot help and a
// convergence loop was twice built and twice reverted.
//
// What MUST hold regardless: the cached CSS viewport records the tab's TRUE
// size. That value feeds input-coordinate mapping, so getting it right is the
// difference between "the panel looks small" (cosmetic) and "clicks land in the
// wrong place" (the operator's actual complaint).
func TestSetViewport_IgnoredResize_KeepsTheTrueViewport(t *testing.T) {
	for _, dpr := range []float64{1, 1.5, 2, 3} {
		t.Run("dpr"+trimFloat(dpr), func(t *testing.T) {
			const trueH = 444 // what the tab really is, whatever we ask for
			runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
				if _, ok := actions[0].(windowBoundsAction); ok {
					return nil // accepted, and silently ignored — the measured behavior
				}
				if lm, ok := actions[0].(layoutMetricsAction); ok {
					*lm.w, *lm.h = 603, trueH
					return nil
				}
				return nil
			}
			reg, lv := newViewportTestLiveView(runCDP)

			applied, err := reg.SetViewport("s1", 603, 587, dpr)
			require.NoError(t, err)
			require.True(t, applied, "an ignored resize must still report applied, not error")

			lv.mu.Lock()
			gotH := lv.cssViewportH
			lv.mu.Unlock()
			assert.Equal(t, trueH, gotH,
				"the cache must hold the tab's TRUE height (%d), never the height we asked for — "+
					"input coordinates are mapped through this value, so a wrong number here is "+
					"exactly the mis-aimed-click bug", trueH)
		})
	}
}

func trimFloat(f float64) string {
	switch f {
	case 1:
		return "1"
	case 1.5:
		return "1.5"
	case 2:
		return "2"
	case 3:
		return "3"
	}
	return "x"
}
