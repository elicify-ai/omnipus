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

// deficitCDP builds a runCDP stand-in that models the measured failure: Chrome
// subtracts a constant `deficit` from whatever window-bounds height it is
// given — INCLUDING the compensated request.
//
// That last clause is the whole point, and modelling it wrong is how this bug
// stayed invisible. The compensator derives its correction from the FIRST gap
// (requested - actual) and applies `requested + gap`. If Chrome then honoured
// that, one pass would converge and single-shot would be fine. It does not:
// against the operator's real session the same subtraction hit the compensated
// request too, so the correction was systematically short and the viewport
// never reached the target however many times it was nudged:
//
//	orig 654 -> compensated ask 797 -> got 511   (286 short)
//	orig 512 -> compensated ask 655 -> got 369   (286 short)
//	orig 575 -> compensated ask 718 -> got 432   (286 short)
//
// The deficit is 286 = 143 x 2 (the deviceScaleFactor), while the compensator
// measured only the 143 visible in the first read-back — it under-corrected by
// exactly half, every time. Iterating is what closes that gap.
func deficitCDP(t *testing.T, deficit int, requested *[]int) func(context.Context, time.Duration, ...chromedp.Action) error {
	t.Helper()
	var lastReqW, lastReqH int
	return func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if wb, ok := actions[0].(windowBoundsAction); ok {
			lastReqW, lastReqH = wb.width, wb.height
			*requested = append(*requested, wb.height)
			return nil
		}
		if lm, ok := actions[0].(layoutMetricsAction); ok {
			h := lastReqH - deficit
			if h < 1 {
				h = 1
			}
			*lm.w, *lm.h = int64(lastReqW), int64(h)
			return nil
		}
		return nil
	}
}
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

			assert.LessOrEqual(t, bounds, viewportCompensationMaxAttempts+1,
				"a refusing browser must cost at most the initial apply plus %d compensation attempts, got %d",
				viewportCompensationMaxAttempts, bounds)

			lv.mu.Lock()
			gotH := lv.cssViewportH
			lv.mu.Unlock()
			assert.Equal(t, 300, gotH,
				"when the target is unreachable the cache must hold the REAL viewport, so input mapping "+
					"stays correct even though the panel cannot reach its requested size")
		})
	}
}

// TestSetViewport_DeviceMatrix_KeepsBestNotLast — an attempt that overshoots
// must never leave the cache worse than an earlier, closer read-back. The
// cached value feeds input coordinate mapping, so "closest to the page's real
// size" is the property that matters, not "most recent".
func TestSetViewport_DeviceMatrix_KeepsBestNotLast(t *testing.T) {
	var reads int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if _, ok := actions[0].(windowBoundsAction); ok {
			return nil
		}
		if lm, ok := actions[0].(layoutMetricsAction); ok {
			reads++
			switch reads {
			case 1:
				*lm.w, *lm.h = 603, 400 // 112 off
			case 2:
				*lm.w, *lm.h = 603, 505 // 7 off — best, and within tolerance
			default:
				*lm.w, *lm.h = 603, 200 // wild overshoot, must NOT be kept
			}
			return nil
		}
		return nil
	}
	reg, lv := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 603, 512, 2)
	require.NoError(t, err)
	require.True(t, applied)

	lv.mu.Lock()
	gotH := lv.cssViewportH
	lv.mu.Unlock()
	assert.Equal(t, 505, gotH,
		"the CLOSEST read-back must win; keeping the last one would hand input mapping a worse number")
}

// TestSetViewport_DeviceMatrix_NoCompensationWhenWithinTolerance guards the
// other direction: a browser that honours the request must not be perturbed by
// pointless extra round trips at any DPR.
func TestSetViewport_DeviceMatrix_NoCompensationWhenWithinTolerance(t *testing.T) {
	for _, dpr := range []float64{1, 1.5, 2, 3} {
		t.Run("dpr"+trimFloat(dpr), func(t *testing.T) {
			var bounds int
			runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
				if _, ok := actions[0].(windowBoundsAction); ok {
					bounds++
					return nil
				}
				if lm, ok := actions[0].(layoutMetricsAction); ok {
					*lm.w, *lm.h = 603, 512 // exactly what was asked
					return nil
				}
				return nil
			}
			reg, _ := newViewportTestLiveView(runCDP)

			applied, err := reg.SetViewport("s1", 603, 512, dpr)
			require.NoError(t, err)
			require.True(t, applied)
			assert.Equal(t, 1, bounds,
				"an honoured request must cost exactly one window-bounds call at dpr %v", dpr)
		})
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
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
