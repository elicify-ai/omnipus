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
 * of whatever live-view frame the caller is mapping against.
 *
 * ADR-047 (JPEG-fallback removal) — the only live caller today is
 * `mapClientToDeviceVideo` below, always with `pageScale` fixed at 1 (the
 * WebRTC video path has no analogous factor to divide out). This function
 * keeps its general `pageScale`-aware form because the underlying math is a
 * real, independently-tested general-purpose coordinate mapping — not
 * because anything still calls it with a variable `pageScale`. The
 * `browser_screencast`-specific framing below (CDP's Page.startScreencast /
 * pageScaleFactor) is historical: that wire frame no longer exists, but the
 * derivation is kept for context.
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
 * Video-mode variant of `mapClientToDevice` (WebRTC build, wave-plan "Key
 * implementation decisions" item 8) — maps a client-space pointer coordinate
 * against a `<video>` sink's `videoWidth`/`videoHeight` instead of a JPEG
 * screencast frame's `width`/`height`.
 *
 * Unlike the JPEG path (`Page.startScreencast`, capped at
 * screencastMaxWidth/Height and reporting a separate CDP `page_scale`
 * factor), tabCapture delivers the tab's viewport at device-pixel resolution
 * directly — there is no analogous downscale cap and no separate
 * pageScaleFactor to divide out. `page_scale` is therefore fixed at 1.0 for
 * every video-mode call (per the wave-plan decision — re-verify against real
 * capture in e2e). This is a thin, pure wrapper around `mapClientToDevice`
 * rather than a parallel implementation, so the clamp/offset/null-guard
 * behavior can never drift between the two sinks — only the scale differs.
 *
 * The caller (BrowserLiveView) picks this variant over `mapClientToDevice`
 * based on which sink (`<video>` vs `<img>`) is currently rendering.
 */
export function mapClientToDeviceVideo(
  clientX: number,
  clientY: number,
  rect: RectLike,
  videoWidth: number,
  videoHeight: number,
): DeviceCoords | null {
  return mapClientToDevice(clientX, clientY, rect, videoWidth, videoHeight, 1)
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
 * currently-rendered `<video>` sink (i.e. frameWidth × frameHeight, the
 * video's own `videoWidth`/`videoHeight`). This is the coordinate space
 * `ctx.drawImage(video, sx, sy, sw, sh, …)` expects when cropping it
 * (annotate-and-discuss, ADR-039 D-B1/B2).
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
 * Given the bounding box of a replaced element rendered with `object-fit:
 * contain` at `width:100%; height:100%` of its box, returns the sub-rectangle
 * actually occupied by the visible content — compensating for the letterbox
 * (top/bottom bars) or pillarbox (left/right bars) `contain` introduces
 * whenever the box's aspect ratio differs from the content's own.
 *
 * BUG 1 fix (pop-out "tiny letterboxed video", live UAT re-run): the
 * fullscreen pop-out route lets its media element grow to fill the whole
 * window (`fillContainer` — see BrowserLiveView's own prop doc) instead of
 * being capped at intrinsic size, so its container's aspect ratio no longer
 * always matches the frame/video content's aspect ratio the way the docked
 * panel's intrinsic-sized layout does. Every coordinate-mapping call
 * (`mapClientToDevice`/`mapClientToDeviceVideo`) assumes its `rect` argument
 * tightly wraps the visible content with NO letterboxing (see
 * `mapClientToDevice`'s own doc comment) — `BrowserLiveView.mapPointerToDeviceCoords`
 * routes every raw `containerRef.getBoundingClientRect()` through this first
 * so that invariant keeps holding even when the element itself is now bigger
 * than its content.
 *
 * A box whose aspect ratio already matches the content (the docked panel's
 * historical "size-to-intrinsic-content, capped by max-*" layout, which
 * never letterboxes) round-trips through this UNCHANGED but for
 * floating-point noise — safe to call unconditionally, on every layout, not
 * just the fill one.
 *
 * A non-positive box or content dimension returns `box` unchanged (mirrors
 * every other guard in this file — a 0×0 box/content has no meaningful
 * "visible sub-rectangle" to compute, and the caller's own frameWidth<=0
 * check downstream already treats that as "no measurable size").
 */
export function computeObjectContainRect(box: RectLike, contentWidth: number, contentHeight: number): RectLike {
  if (box.width <= 0 || box.height <= 0 || contentWidth <= 0 || contentHeight <= 0) {
    return box
  }
  const boxAspect = box.width / box.height
  const contentAspect = contentWidth / contentHeight
  if (contentAspect > boxAspect) {
    // Content relatively WIDER than the box: full box width used, letterboxed top/bottom.
    const height = box.width / contentAspect
    return { left: box.left, top: box.top + (box.height - height) / 2, width: box.width, height }
  }
  if (contentAspect < boxAspect) {
    // Content relatively TALLER/narrower than the box: full box height used, pillarboxed left/right.
    const width = box.height * contentAspect
    return { left: box.left + (box.width - width) / 2, top: box.top, width, height: box.height }
  }
  // Aspect ratios already match (or are equal within this exact comparison) — no-op.
  return box
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
