import { describe, it, expect } from 'vitest'
import {
  mapClientToDevice,
  computeModifiers,
  mapMouseButton,
  isPrintableKey,
  mapClientToFramePixels,
  framePixelToDeviceCoords,
  computeCropRect,
} from './browserLiveCoords'

describe('mapClientToDevice', () => {
  it('maps a 1:1 rect (rendered box exactly matches frame dimensions)', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    const result = mapClientToDevice(640, 360, rect, 1280, 720)
    expect(result).toEqual({ x: 640, y: 360 })
  })

  it('scales up when the rendered box is smaller than the frame (common case — CSS-shrunk preview)', () => {
    // Rendered at half size (640x360 CSS px) of a 1280x720 device frame.
    const rect = { left: 0, top: 0, width: 640, height: 360 }
    const result = mapClientToDevice(320, 180, rect, 1280, 720)
    expect(result).toEqual({ x: 640, y: 360 })
  })

  it('scales down when the rendered box is larger than the frame', () => {
    const rect = { left: 0, top: 0, width: 2560, height: 1440 }
    const result = mapClientToDevice(1280, 720, rect, 1280, 720)
    expect(result).toEqual({ x: 640, y: 360 })
  })

  it('offsets by rect.left/rect.top (container not flush with viewport origin)', () => {
    const rect = { left: 100, top: 50, width: 1280, height: 720 }
    const result = mapClientToDevice(100, 50, rect, 1280, 720)
    expect(result).toEqual({ x: 0, y: 0 })
  })

  it('clamps negative results to 0 (pointer event fired slightly before the container edge)', () => {
    const rect = { left: 100, top: 50, width: 1280, height: 720 }
    const result = mapClientToDevice(50, 10, rect, 1280, 720)
    expect(result).toEqual({ x: 0, y: 0 })
  })

  it('clamps results beyond frame dimensions to the frame edge', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    const result = mapClientToDevice(2000, 2000, rect, 1280, 720)
    expect(result).toEqual({ x: 1280, y: 720 })
  })

  it('returns null when the container has no measurable size (zero width)', () => {
    const rect = { left: 0, top: 0, width: 0, height: 720 }
    expect(mapClientToDevice(10, 10, rect, 1280, 720)).toBeNull()
  })

  it('returns null when the container has no measurable size (zero height)', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 0 }
    expect(mapClientToDevice(10, 10, rect, 1280, 720)).toBeNull()
  })

  it('returns null when the frame has no reported dimensions yet', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    expect(mapClientToDevice(10, 10, rect, 0, 0)).toBeNull()
  })

  it('divides by page_scale (pageScaleFactor != 1): CDP dispatch coords are CSS px, the frame is device px', () => {
    // 1:1 rect→frame (no CSS shrink), but the page reports pageScaleFactor 2.0
    // (e.g. pinch-zoom) — the frame-space coord must be halved to land back
    // in the CSS-pixel space CDP's Input.dispatchMouseEvent expects.
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    const result = mapClientToDevice(640, 360, rect, 1280, 720, 2.0)
    expect(result).toEqual({ x: 320, y: 180 })
  })

  it('treats an undefined page_scale as 1 (unchanged from the non-scaled formula)', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    const result = mapClientToDevice(640, 360, rect, 1280, 720, undefined)
    expect(result).toEqual({ x: 640, y: 360 })
  })

  it('treats a zero or negative page_scale as 1 (guards against a malformed frame)', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    expect(mapClientToDevice(640, 360, rect, 1280, 720, 0)).toEqual({ x: 640, y: 360 })
    expect(mapClientToDevice(640, 360, rect, 1280, 720, -1)).toEqual({ x: 640, y: 360 })
  })

  it('clamps to the page_scale-adjusted frame edge, not the raw device-pixel edge', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    const result = mapClientToDevice(2000, 2000, rect, 1280, 720, 2.0)
    expect(result).toEqual({ x: 640, y: 360 }) // clamps to 1280/2, 720/2 — not 1280, 720
  })
})

describe('computeModifiers', () => {
  it('returns 0 for no modifiers', () => {
    expect(computeModifiers({ altKey: false, ctrlKey: false, metaKey: false, shiftKey: false })).toBe(0)
  })

  it('sets bit 1 for Alt', () => {
    expect(computeModifiers({ altKey: true, ctrlKey: false, metaKey: false, shiftKey: false })).toBe(1)
  })

  it('sets bit 2 for Ctrl', () => {
    expect(computeModifiers({ altKey: false, ctrlKey: true, metaKey: false, shiftKey: false })).toBe(2)
  })

  it('sets bit 4 for Meta', () => {
    expect(computeModifiers({ altKey: false, ctrlKey: false, metaKey: true, shiftKey: false })).toBe(4)
  })

  it('sets bit 8 for Shift', () => {
    expect(computeModifiers({ altKey: false, ctrlKey: false, metaKey: false, shiftKey: true })).toBe(8)
  })

  it('combines all four into 15 (matches the generated schema max of 15)', () => {
    expect(computeModifiers({ altKey: true, ctrlKey: true, metaKey: true, shiftKey: true })).toBe(15)
  })

  it('combines Ctrl+Shift into 10', () => {
    expect(computeModifiers({ altKey: false, ctrlKey: true, metaKey: false, shiftKey: true })).toBe(10)
  })
})

describe('mapMouseButton', () => {
  it('maps 0 to left', () => {
    expect(mapMouseButton(0)).toBe('left')
  })
  it('maps 1 to middle', () => {
    expect(mapMouseButton(1)).toBe('middle')
  })
  it('maps 2 to right', () => {
    expect(mapMouseButton(2)).toBe('right')
  })
  it('maps 3 to back', () => {
    expect(mapMouseButton(3)).toBe('back')
  })
  it('maps 4 to forward', () => {
    expect(mapMouseButton(4)).toBe('forward')
  })
  it('maps an unknown button value to none', () => {
    expect(mapMouseButton(99)).toBe('none')
  })
})

describe('isPrintableKey', () => {
  it('treats a bare letter as printable', () => {
    expect(isPrintableKey({ key: 'a', ctrlKey: false, metaKey: false, altKey: false })).toBe(true)
  })

  it('treats a bare space as printable', () => {
    expect(isPrintableKey({ key: ' ', ctrlKey: false, metaKey: false, altKey: false })).toBe(true)
  })

  it('treats Enter as non-printable', () => {
    expect(isPrintableKey({ key: 'Enter', ctrlKey: false, metaKey: false, altKey: false })).toBe(false)
  })

  it('treats Backspace as non-printable', () => {
    expect(isPrintableKey({ key: 'Backspace', ctrlKey: false, metaKey: false, altKey: false })).toBe(false)
  })

  it('treats a letter with Ctrl held as non-printable (shortcut, not text)', () => {
    expect(isPrintableKey({ key: 'a', ctrlKey: true, metaKey: false, altKey: false })).toBe(false)
  })

  it('treats a letter with Meta held as non-printable (shortcut, not text)', () => {
    expect(isPrintableKey({ key: 'a', ctrlKey: false, metaKey: true, altKey: false })).toBe(false)
  })

  it('treats a letter with Alt held as non-printable', () => {
    expect(isPrintableKey({ key: 'a', ctrlKey: false, metaKey: false, altKey: true })).toBe(false)
  })

  it('treats a bare Shift+letter as printable (produces an uppercase char, not a shortcut)', () => {
    // Shift alone does not disqualify — e.key already reflects the shifted
    // character (e.g. "A" or "!"), which is exactly what should be inserted.
    expect(isPrintableKey({ key: 'A', ctrlKey: false, metaKey: false, altKey: false })).toBe(true)
  })
})

// ── Annotate-a-region crop mapping (ADR-039 D-B1/B2/B3) ─────────────────────

describe('mapClientToFramePixels', () => {
  it('maps a 1:1 rect (rendered box exactly matches frame dimensions) with NO page_scale division', () => {
    // Unlike mapClientToDevice, this must NOT divide by pageScaleFactor —
    // it targets the raw <img> natural-pixel space for canvas cropping.
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    expect(mapClientToFramePixels(640, 360, rect, 1280, 720)).toEqual({ x: 640, y: 360 })
  })

  it('scales up when the rendered box is smaller than the frame (CSS-shrunk preview)', () => {
    const rect = { left: 0, top: 0, width: 640, height: 360 }
    expect(mapClientToFramePixels(320, 180, rect, 1280, 720)).toEqual({ x: 640, y: 360 })
  })

  it('offsets by rect.left/rect.top', () => {
    const rect = { left: 100, top: 50, width: 1280, height: 720 }
    expect(mapClientToFramePixels(100, 50, rect, 1280, 720)).toEqual({ x: 0, y: 0 })
  })

  it('clamps negative results to 0', () => {
    const rect = { left: 100, top: 50, width: 1280, height: 720 }
    expect(mapClientToFramePixels(50, 10, rect, 1280, 720)).toEqual({ x: 0, y: 0 })
  })

  it('clamps results beyond frame dimensions to the frame edge', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    expect(mapClientToFramePixels(2000, 2000, rect, 1280, 720)).toEqual({ x: 1280, y: 720 })
  })

  it('returns null when the container has no measurable size', () => {
    const rect = { left: 0, top: 0, width: 0, height: 720 }
    expect(mapClientToFramePixels(10, 10, rect, 1280, 720)).toBeNull()
  })

  it('returns null when the frame has no reported dimensions yet', () => {
    const rect = { left: 0, top: 0, width: 1280, height: 720 }
    expect(mapClientToFramePixels(10, 10, rect, 0, 0)).toBeNull()
  })
})

describe('framePixelToDeviceCoords', () => {
  it('is a no-op with page_scale 1 (default)', () => {
    expect(framePixelToDeviceCoords(640, 360)).toEqual({ x: 640, y: 360 })
  })

  it('divides by page_scale, mirroring mapClientToDevice output', () => {
    expect(framePixelToDeviceCoords(640, 360, 2.0)).toEqual({ x: 320, y: 180 })
  })

  it('treats a zero or negative page_scale as 1', () => {
    expect(framePixelToDeviceCoords(640, 360, 0)).toEqual({ x: 640, y: 360 })
    expect(framePixelToDeviceCoords(640, 360, -1)).toEqual({ x: 640, y: 360 })
  })
})

describe('computeCropRect', () => {
  it('computes a rect from a normal top-left-to-bottom-right drag', () => {
    const rect = computeCropRect({ x: 100, y: 100 }, { x: 300, y: 250 }, 1280, 720)
    expect(rect).toEqual({ x: 100, y: 100, width: 200, height: 150 })
  })

  it('normalizes a drag in any direction (bottom-right to top-left)', () => {
    const rect = computeCropRect({ x: 300, y: 250 }, { x: 100, y: 100 }, 1280, 720)
    expect(rect).toEqual({ x: 100, y: 100, width: 200, height: 150 })
  })

  it('treats a drag shorter than minDragSize in both axes as a click, synthesizing a centered box', () => {
    const rect = computeCropRect({ x: 500, y: 400 }, { x: 502, y: 401 }, 1280, 720, { clickBoxSize: 48 })
    expect(rect).toEqual({ x: 476, y: 376, width: 48, height: 48 })
  })

  it('clamps the synthesized click box so it never runs off the frame edge (click near origin)', () => {
    const rect = computeCropRect({ x: 2, y: 3 }, { x: 2, y: 3 }, 1280, 720, { clickBoxSize: 48 })
    expect(rect).toEqual({ x: 0, y: 0, width: 48, height: 48 })
  })

  it('clamps the synthesized click box near the far frame edge', () => {
    const rect = computeCropRect({ x: 1279, y: 719 }, { x: 1279, y: 719 }, 1280, 720, { clickBoxSize: 48 })
    expect(rect).toEqual({ x: 1232, y: 672, width: 48, height: 48 })
  })

  it('shrinks the click box to fit a frame smaller than the default clickBoxSize', () => {
    const rect = computeCropRect({ x: 10, y: 10 }, { x: 10, y: 10 }, 20, 20, { clickBoxSize: 48 })
    expect(rect).toEqual({ x: 0, y: 0, width: 20, height: 20 })
  })

  it('a drag past minDragSize in only ONE axis is still treated as a rectangle, not a click', () => {
    // dx=50 (>= default minDragSize 6), dy=1 — still a real (thin) rectangle.
    const rect = computeCropRect({ x: 100, y: 100 }, { x: 150, y: 101 }, 1280, 720)
    expect(rect).toEqual({ x: 100, y: 100, width: 50, height: 1 })
  })

  it('returns null for a pathological zero-area frame', () => {
    expect(computeCropRect({ x: 0, y: 0 }, { x: 10, y: 10 }, 0, 0)).toBeNull()
  })

  // Reviewer finding: an EXACT axis-aligned drag (dx or dy rounds to 0) used
  // to fall through to the rectangle branch with a zero width/height — a
  // rect canvas.drawImage cannot crop (throws IndexSizeError). It must
  // synthesize the click box instead, exactly like a true click does.
  it('treats an exact vertical drag (dx===0, dy past minDragSize) as a click, not a zero-width rect', () => {
    const rect = computeCropRect({ x: 200, y: 100 }, { x: 200, y: 300 }, 1280, 720, { clickBoxSize: 48 })
    expect(rect).toEqual({ x: 176, y: 76, width: 48, height: 48 })
    expect(rect!.width).toBeGreaterThan(0)
    expect(rect!.height).toBeGreaterThan(0)
  })

  it('treats an exact horizontal drag (dy===0, dx past minDragSize) as a click, not a zero-height rect', () => {
    const rect = computeCropRect({ x: 100, y: 300 }, { x: 300, y: 300 }, 1280, 720, { clickBoxSize: 48 })
    expect(rect).toEqual({ x: 76, y: 276, width: 48, height: 48 })
    expect(rect!.width).toBeGreaterThan(0)
    expect(rect!.height).toBeGreaterThan(0)
  })
})
