// browserLiveCoords — pure coordinate-mapping + input-classification helpers
// for the live interactive browser panel (ADR-038 D5/D6).
//
// Kept dependency-free (no DOM types beyond structural shapes) so it can be
// unit-tested without mounting a component or a real WebSocket.

/** Structural subset of DOMRect — accepts the real thing or a plain object in tests. */
export interface RectLike { // not-wire-format: internal DOMRect subset for local pointer→device coord math, never serialized across the gateway/SPA boundary
  left: number
  top: number
  width: number
  height: number
}

export interface DeviceCoords { // not-wire-format: local {x,y} result of coordinate mapping computed in the browser, never sent on the wire (BrowserInputFrame is the wire type)
  x: number
  y: number
}

/**
 * Maps a client-space pointer coordinate (from a PointerEvent/MouseEvent/
 * WheelEvent's clientX/clientY) to the CSS-pixel coordinate space CDP's
 * Input.dispatchMouseEvent/dispatchKeyEvent expect, matching the dimensions
 * of the latest `browser_screencast` frame.
 *
 * Formula (ADR-038 D5): the frame container is sized to exactly match the
 * screencast frame's aspect ratio (no letterboxing — see BrowserLiveView),
 * so the first step is a simple linear scale from the container's rendered
 * CSS box to the frame's device pixel box:
 *
 *   frameX = (clientX - rect.left) * (frameWidth / rect.width)
 *   frameY = (clientY - rect.top)  * (frameHeight / rect.height)
 *
 * The screencast frame itself is captured at DeviceWidth×DeviceHeight
 * (Page.startScreencast), which is CSS-pixel size × pageScaleFactor. CDP's
 * dispatch coordinates, however, are always plain CSS pixels — so when
 * `pageScale` (the frame's `page_scale`, i.e. pageScaleFactor) differs from
 * 1, `frameX`/`frameY` must be divided by it to land back in the space the
 * backend's CDP call expects:
 *
 *   deviceX = frameX / pageScale
 *   deviceY = frameY / pageScale
 *
 * An unset, zero, or negative `pageScale` is treated as 1 (no-op) — CDP
 * only reports pageScaleFactor via Page.getLayoutMetrics, so a frame from a
 * backend build that doesn't thread it through must not corrupt the mapping.
 *
 * Returns null when the container has no measurable size yet (rect.width/
 * height <= 0 — e.g. first render before layout) rather than dividing by
 * zero. Result is clamped to [0, frameWidth/pageScale] / [0, frameHeight/
 * pageScale] — the page_scale-adjusted frame edge, not the raw device-pixel
 * edge — so a pointer event that fires a pixel or two outside the container
 * (common during fast drags) never sends an out-of-bounds coordinate to the
 * backend.
 */
export function mapClientToDevice(
  clientX: number,
  clientY: number,
  rect: RectLike,
  frameWidth: number,
  frameHeight: number,
  pageScale?: number,
): DeviceCoords | null {
  if (rect.width <= 0 || rect.height <= 0 || frameWidth <= 0 || frameHeight <= 0) {
    return null
  }
  const scale = pageScale !== undefined && pageScale > 0 ? pageScale : 1
  const rawX = ((clientX - rect.left) * (frameWidth / rect.width)) / scale
  const rawY = ((clientY - rect.top) * (frameHeight / rect.height)) / scale
  const maxX = frameWidth / scale
  const maxY = frameHeight / scale
  return {
    x: Math.min(Math.max(rawX, 0), maxX),
    y: Math.min(Math.max(rawY, 0), maxY),
  }
}

/**
 * CDP-style keyboard/mouse modifier bitmask (matches Chrome DevTools
 * Protocol's Input.dispatchMouseEvent/dispatchKeyEvent `modifiers` field —
 * also the exact bound the generated BrowserInputFrame.modifiers schema
 * enforces, z.number().int().min(0).max(15)):
 *   Alt = 1, Ctrl = 2, Meta/Command = 4, Shift = 8
 */
export function computeModifiers(e: {
  altKey: boolean
  ctrlKey: boolean
  metaKey: boolean
  shiftKey: boolean
}): number {
  let m = 0
  if (e.altKey) m |= 1
  if (e.ctrlKey) m |= 2
  if (e.metaKey) m |= 4
  if (e.shiftKey) m |= 8
  return m
}

export type BrowserInputButton = 'none' | 'left' | 'middle' | 'right' | 'back' | 'forward'

/** Maps a DOM MouseEvent.button value to the BrowserInputFrame button enum. */
export function mapMouseButton(button: number): BrowserInputButton {
  switch (button) {
    case 0:
      return 'left'
    case 1:
      return 'middle'
    case 2:
      return 'right'
    case 3:
      return 'back'
    case 4:
      return 'forward'
    default:
      return 'none'
  }
}

// ── Annotate-a-region crop mapping (ADR-039 D-B1/B2/B3) ────────────────────

export interface FrameCropRect { // not-wire-format: local crop-rectangle result in screencast-frame pixel space, never serialized across the gateway/SPA boundary
  x: number
  y: number
  width: number
  height: number
}

/**
 * Maps a client-space pointer coordinate to the NATURAL PIXEL space of the
 * screencast frame's `<img>` (i.e. frame.width × frame.height — the raw JPEG
 * dimensions CDP captured via Page.startScreencast). This is the coordinate
 * space `ctx.drawImage(img, sx, sy, sw, sh, …)` expects when cropping the
 * currently-rendered `<img>` element (annotate-and-discuss, ADR-039 D-B1/B2).
 *
 * Deliberately distinct from mapClientToDevice: that function additionally
 * divides by pageScaleFactor to land in the CSS-pixel space CDP's
 * Input.dispatch* calls expect. Cropping the raw bitmap needs the
 * UN-divided natural-pixel space instead — reusing mapClientToDevice's output
 * here would crop the wrong region whenever pageScaleFactor != 1.
 *
 * Returns null when the container/frame has no measurable size (mirrors
 * mapClientToDevice's guard). Result is clamped to [0, frameWidth] /
 * [0, frameHeight].
 */
export function mapClientToFramePixels(
  clientX: number,
  clientY: number,
  rect: RectLike,
  frameWidth: number,
  frameHeight: number,
): DeviceCoords | null {
  if (rect.width <= 0 || rect.height <= 0 || frameWidth <= 0 || frameHeight <= 0) {
    return null
  }
  const rawX = (clientX - rect.left) * (frameWidth / rect.width)
  const rawY = (clientY - rect.top) * (frameHeight / rect.height)
  return {
    x: Math.min(Math.max(rawX, 0), frameWidth),
    y: Math.min(Math.max(rawY, 0), frameHeight),
  }
}

/**
 * Converts a point already in the screencast frame's natural-pixel space
 * (see mapClientToFramePixels) into the device (CSS) pixel space
 * `BrowserInspectRequest.x`/`.y` expect (ADR-039 D-B3) — the SAME
 * page_scale-adjusted space mapClientToDevice's mouse-dispatch coordinates
 * use, so an inspect request always agrees with where a click would land.
 */
export function framePixelToDeviceCoords(x: number, y: number, pageScale?: number): DeviceCoords {
  const scale = pageScale !== undefined && pageScale > 0 ? pageScale : 1
  return { x: x / scale, y: y / scale }
}

/**
 * Scales a crop rectangle from screencast-frame METADATA space
 * (frameWidth×frameHeight = CDP Metadata.DeviceWidth/DeviceHeight, the full
 * viewport device-pixel size the rect was computed in) into the decoded
 * `<img>`'s NATURAL-pixel space (naturalWidth×naturalHeight).
 *
 * Why this is needed (UAT blank-crop finding): the screencast JPEG is captured
 * with BOTH `WithMaxWidth(screencastMaxWidth=1280)` AND
 * `WithMaxHeight(screencastMaxHeight=720)` (live.go), so the decoded bitmap is
 * DOWNSCALED whenever the device size exceeds the screencast max bound on
 * EITHER axis — not just a wide viewport, but also a narrow-tall (portrait/
 * mobile) one hitting the height cap. Passing the unscaled rect straight to
 * `ctx.drawImage(img, sx, sy, sw, sh, …)` then reads an out-of-bounds /
 * misaligned region of the smaller bitmap, so drawImage draws nothing and the
 * crop comes out blank (transparent → white or black).
 *
 * A non-positive frame or natural dimension falls back to scale 1 (no-op), and
 * width/height are floored at 1 so the destination canvas is never 0×0
 * (drawImage would throw IndexSizeError).
 */
export function scaleCropToImagePixels(
  rect: FrameCropRect,
  frameWidth: number,
  frameHeight: number,
  naturalWidth: number,
  naturalHeight: number,
): FrameCropRect {
  const scaleX = frameWidth > 0 && naturalWidth > 0 ? naturalWidth / frameWidth : 1
  const scaleY = frameHeight > 0 && naturalHeight > 0 ? naturalHeight / frameHeight : 1
  return {
    x: rect.x * scaleX,
    y: rect.y * scaleY,
    width: Math.max(1, rect.width * scaleX),
    height: Math.max(1, rect.height * scaleY),
  }
}

/**
 * Computes a crop rectangle (in frame natural-pixel space, see
 * mapClientToFramePixels) from a drag gesture's start/end points.
 *
 * A drag shorter than `minDragSize` in BOTH axes is treated as a click
 * rather than a rectangle — Codex parity (ADR-039 D-B1/B2: "the user drags
 * a rectangle (or clicks…)") synthesizes a fixed-size box (`clickBoxSize`)
 * centered on the point instead of discarding a tap-to-annotate gesture.
 * The synthesized box is clamped to stay fully inside
 * [0,frameWidth] × [0,frameHeight].
 *
 * Returns null only when the frame itself has no measurable area (a
 * pathological 0×0 frame) — a normal click or drag always yields a rect.
 *
 * Also synthesizes the click box for an exact axis-aligned drag (dx or dy
 * rounds to exactly 0 — e.g. a perfectly vertical or horizontal drag) even
 * though the OTHER axis clears minDragSize: a rectangle with a zero
 * dimension is not a valid crop (canvas.drawImage throws IndexSizeError on
 * sw/sh === 0), so any degenerate width/height, not just "both axes below
 * threshold", must fall back to the fixed box.
 */
export function computeCropRect(
  start: DeviceCoords,
  end: DeviceCoords,
  frameWidth: number,
  frameHeight: number,
  opts?: { minDragSize?: number; clickBoxSize?: number },
): FrameCropRect | null {
  if (frameWidth <= 0 || frameHeight <= 0) return null
  const minDragSize = opts?.minDragSize ?? 6
  const clickBoxSize = Math.max(1, Math.min(opts?.clickBoxSize ?? 48, frameWidth, frameHeight))
  const dx = Math.abs(end.x - start.x)
  const dy = Math.abs(end.y - start.y)
  const width = Math.round(dx)
  const height = Math.round(dy)

  if ((dx < minDragSize && dy < minDragSize) || width <= 0 || height <= 0) {
    const half = clickBoxSize / 2
    const x = Math.min(Math.max(start.x - half, 0), Math.max(frameWidth - clickBoxSize, 0))
    const y = Math.min(Math.max(start.y - half, 0), Math.max(frameHeight - clickBoxSize, 0))
    return {
      x: Math.round(x),
      y: Math.round(y),
      width: Math.round(clickBoxSize),
      height: Math.round(clickBoxSize),
    }
  }

  const x = Math.round(Math.min(start.x, end.x))
  const y = Math.round(Math.min(start.y, end.y))
  return { x, y, width, height }
}

/**
 * True when a keydown/keyup event represents a single printable character
 * that should be forwarded as a `text` input frame (Input.insertText on the
 * backend) rather than a `key_down`/`key_up` pair. A held Ctrl/Meta/Alt
 * modifier means the key is part of a shortcut (e.g. Ctrl+A) — those must
 * go through key_down/key_up so the backend dispatches a real key event
 * instead of literally inserting the character.
 */
export function isPrintableKey(e: {
  key: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
}): boolean {
  return e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey
}
