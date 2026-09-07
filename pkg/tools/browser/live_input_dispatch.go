package browser

import (
	"context"
)

func (lv *LiveView) dispatchInputCommand(ctx, targetCtx context.Context, viewerID string, in LiveInput) error {
	// Root-cause doc Fault 3: x/y arrive in the CLIENT's capture-frame pixel
	// space, which is no longer guaranteed to equal the tab's CSS pixel
	// space now that SetViewport (Fault 1 fix, above) can resize the tab
	// independently of what the encoder's downscaling happens to produce.
	// Only pointer-position kinds carry a meaningful position to rescale —
	// wheel's DeltaX/DeltaY are scroll deltas, not positions, and key/text
	// carry no coordinates at all.
	switch in.Kind {
	case "mouse_move", "mouse_down", "mouse_up", "wheel":
		if in.HasXY && in.CaptureWidth > 0 && in.CaptureHeight > 0 {
			rx, ry, ok := lv.rescaleToCSSViewport(ctx, in.X, in.Y, in.CaptureWidth, in.CaptureHeight)
			if !ok {
				if err := ctx.Err(); err != nil {
					return realInputError("browser live: input canceled: %w", err)
				}
				// DROP rather than dispatch at an unmapped coordinate — see
				// rescaleToCSSViewport's doc comment: unscaled coordinates
				// land ~34% off (measured), i.e. on the wrong element, and a
				// mis-aimed click can navigate away, delete or submit.
				//
				// Classification matters as much as the drop. A one-off miss
				// is a transient the user retries past, so it stays benign and
				// silent. But a SUSTAINED streak means the CDP transport is
				// wedged or the tab is dead — and LiveInputErrorReal's own doc
				// comment names exactly that as the thing that must reach the
				// user ("a dead browser looked identical to a healthy, idle
				// one", ADR-038 finding #4). Without this escalation a crashed
				// tab would swallow every click forever with no error, since
				// pointer kinds bail out here and never reach the real-error
				// CDP dispatch below.
				lv.mu.Lock()
				failures := lv.viewportFetchFailures
				lv.mu.Unlock()
				if failures >= viewportFetchFailureEscalation {
					return realInputError(
						"browser live: cannot read the tab's CSS viewport after %d consecutive attempts — the browser tab may have crashed or the CDP transport is wedged",
						failures,
					)
				}
				return benignInputError(
					"browser live: viewport unknown, dropped %s to avoid a mis-aimed dispatch",
					in.Kind,
				)
			}
			in.X, in.Y = rx, ry
		}
	}

	return lv.dispatchTrackedInput(ctx, targetCtx, viewerID, in)
}
