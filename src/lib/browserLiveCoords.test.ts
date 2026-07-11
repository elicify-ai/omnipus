import { describe, it, expect } from 'vitest'
import {
  mapClientToDevice,
  computeModifiers,
  mapMouseButton,
  isPrintableKey,
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
