// Annotate-a-region crop helpers for BrowserLiveView (ADR-039 D-B1/B2).
// Extracted so the live-view component can shrink without changing behaviour.

import { scaleCropToImagePixels, type FrameCropRect } from '@/lib/browserLiveCoords'
import type { BrowserAnnotationFrame } from '@/lib/browserAnnotationFrame'

/** A finalized region selection, cropped to a File and ready to send (ADR-039 D-B1/B2). */
export interface PendingAnnotation { // not-wire-format: local annotate-popover state, never serialized across the gateway/SPA boundary
  file: File
  previewUrl: string
  /** Device (CSS) pixel point — center of the crop — for the D-B3 inspect call. */
  point: { x: number; y: number } | null
  frame: BrowserAnnotationFrame | null
}

/**
 * Shared canvas-crop implementation for annotate-a-region (ADR-039 D-B1/B2),
 * used by BOTH the JPEG `<img>` sink and the WebRTC build's `<video>` sink
 * (W1-F) — draws `source` (already confirmed by the caller to have a live
 * decoded frame available: `img.complete`/`naturalWidth` or a video's
 * `readyState`/`videoWidth`) into an offscreen canvas and returns a cropped
 * PNG File with the exact same output contract regardless of sink.
 *
 * Reuses `scaleCropToImagePixels` (browserLiveCoords.ts) for the
 * frame-space→natural-pixel-space scale correction in BOTH cases rather than
 * duplicating it: for the img sink this corrects for the screencast JPEG's
 * fixed downscale cap (see cropFrameToFile's own doc comment); for the video
 * sink there is no such cap, but the SAME correction still guards against a
 * recapture-driven resolution change landing between when the crop rect was
 * computed (drag-start `frameWidth`/`frameHeight`) and when this draw
 * actually runs (the video's CURRENT `naturalWidth`/`naturalHeight`) — see
 * browserLiveCoords.test.ts's "video-mode reuse" coverage. A no-drift call
 * (the common case for both sinks) is a scale-1 no-op either way.
 *
 * Exceptions from drawImage/getContext (e.g. IndexSizeError on a degenerate
 * zero-width/height rect, or a tainted canvas) are swallowed to null — this
 * is awaited from finalizeSelection, itself invoked fire-and-forget (`void
 * finalizeSelection(...)` from the pointerup handler), so an uncaught
 * rejection here would surface as an unhandled promise rejection with no
 * toast and a frozen selection box; returning null instead routes through
 * finalizeSelection's existing `if (!file) return fail()` path.
 */
export async function drawCropToPngFile(
  source: CanvasImageSource,
  naturalWidth: number,
  naturalHeight: number,
  rect: FrameCropRect,
  frameWidth: number,
  frameHeight: number,
): Promise<File | null> {
  const src = scaleCropToImagePixels(rect, frameWidth, frameHeight, naturalWidth, naturalHeight)
  const { x: sx, y: sy, width: sw, height: sh } = src
  try {
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(sw)
    canvas.height = Math.round(sh)
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.drawImage(source, sx, sy, sw, sh, 0, 0, canvas.width, canvas.height)
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
    if (!blob) return null
    return new File([blob], 'annotation.png', { type: 'image/png' })
  } catch {
    return null
  }
}
