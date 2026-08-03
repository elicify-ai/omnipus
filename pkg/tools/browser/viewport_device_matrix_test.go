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

			// Two window-bounds calls total: the initial apply plus exactly
			// one compensation attempt. Asserted as an exact value, not a
			// loose upper bound — a loose bound would pass equally against a
			// looping implementation and so would guard nothing.
			assert.Equal(t, 2, bounds,
				"a refusing browser must cost the initial apply plus exactly one compensation "+
					"attempt (no loop), got %d", bounds)

			lv.mu.Lock()
			gotH := lv.cssViewportH
			lv.mu.Unlock()
			assert.Equal(t, 300, gotH,
				"when the target is unreachable the cache must hold the REAL viewport, so input mapping "+
					"stays correct even though the panel cannot reach its requested size")
		})
	}
}

// TestSetViewport_SingleCompensationPass_OverwritesUnconditionally pins what
// the shipped code ACTUALLY does, which is not the same as what is desirable.
//
// SetViewport makes exactly ONE compensation attempt and then assigns
// `actualW, actualH = compW2, compH2` unconditionally — there is no
// keep-the-closest comparison against the pre-compensation read-back. So a
// compensation pass that OVERSHOOTS leaves the cache further from the target
// than before, and that cached value is what input-coordinate mapping trusts.
//
// This test previously claimed the opposite ("the CLOSEST read-back must win"),
// which was false: it passed only because its fixture happened to improve on
// the first reading. Asserting a best-of-N mechanism that does not exist gave
// false assurance about behavior under a real overshoot. Documented here as a
// known limitation rather than silently "tested" — see the DSF-2 shrink, still
// open, in this file's header.
func TestSetViewport_SingleCompensationPass_OverwritesUnconditionally(t *testing.T) {
	var reads int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if _, ok := actions[0].(windowBoundsAction); ok {
			return nil
		}
		if lm, ok := actions[0].(layoutMetricsAction); ok {
			reads++
			if reads == 1 {
				*lm.w, *lm.h = 603, 500 // 12 off — outside tolerance, triggers compensation
			} else {
				*lm.w, *lm.h = 603, 300 // compensation OVERSHOOTS: 212 off, far worse
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

	assert.Equal(t, 300, gotH,
		"the compensated read-back is taken unconditionally, even when it is WORSE than the "+
			"pre-compensation value — pinning the real behavior so a future keep-the-closest "+
			"change is a deliberate, visible improvement rather than an accident")
	assert.Equal(t, 2, reads, "exactly one compensation pass: two read-backs total, no loop")
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
