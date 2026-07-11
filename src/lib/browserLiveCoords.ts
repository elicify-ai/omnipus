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
