import { describe, expect, it } from 'vitest'
import { mapClientToBrowserCss } from './browserFrameCoords'

// The 800x600 container holds an 800x400 frame (100px vertical padding).
// The frame contains a 600x400 page (100px horizontal encoder padding).
// Container origin (100,50) makes the visible CSS page exactly [200,800)x[150,550).
const box = { left: 100, top: 50, width: 800, height: 600 }
describe('mapClientToBrowserCss', () => {
  it.each([
    [200, 150, { x: 0, y: 0 }], [201, 151, { x: 1, y: 1 }],
    [500, 350, { x: 300, y: 200 }], [799, 549, { x: 599, y: 399 }],
    [199, 350, null], [800, 350, null], [500, 149, null], [500, 550, null],
    [100, 350, null], [900, 350, null], [500, 50, null], [500, 650, null],
  ])('maps client (%s,%s) through both padding layers', (x, y, expected) => {
    expect(mapClientToBrowserCss(x, y, box, 800, 400, 600, 400)).toEqual(expected)
  })

  it('maps the center of the independently measured odd-aspect Chrome capture', () => {
    // CSS756x413 captured as756x408; centered contain preserves the page center.
    expect(mapClientToBrowserCss(378, 204, { left: 0, top: 0, width: 756, height: 408 }, 756, 408, 756, 413)).toEqual({ x: 378, y: 206.5 })
  })

  it('maps a known CSS corner marker through fractional encoder padding', () => {
    // Independent forward projection: contain scale408/413, horizontal inset
    // (756-756*408/413)/2. The source marker is at CSS(10,10).
    const x = (756 - 756 * 408 / 413) / 2 + 10 * 408 / 413
    const y = 10 * 408 / 413
    const result = mapClientToBrowserCss(x, y, { left: 0, top: 0, width: 756, height: 408 }, 756, 408, 756, 413)
    expect(result?.x).toBeCloseTo(10, 10)
    expect(result?.y).toBeCloseTo(10, 10)
  })

  it.each([0, -1, NaN, Infinity])('rejects invalid confirmed CSS dimension %s', dimension => {
    expect(mapClientToBrowserCss(500, 350, box, 800, 400, dimension, 400)).toBeNull()
    expect(mapClientToBrowserCss(500, 350, box, 800, 400, 600, dimension)).toBeNull()
  })

  it.each([0, -1, NaN, Infinity])('rejects invalid decoded dimension %s', dimension => {
    expect(mapClientToBrowserCss(500, 350, box, dimension, 400, 600, 400)).toBeNull()
    expect(mapClientToBrowserCss(500, 350, box, 800, dimension, 600, 400)).toBeNull()
  })

  it.each([NaN, Infinity, -Infinity])('rejects nonfinite pointer coordinates %s', coordinate => {
    expect(mapClientToBrowserCss(coordinate, 350, box, 800, 400, 600, 400)).toBeNull()
    expect(mapClientToBrowserCss(500, coordinate, box, 800, 400, 600, 400)).toBeNull()
  })
  it('preserves actual outside CSS coordinates only for an owned release', () => {
    expect(mapClientToBrowserCss(100, 350, box, 800, 400, 600, 400, true)).toEqual({ x: -100, y: 200 })
    expect(mapClientToBrowserCss(900, 650, box, 800, 400, 600, 400, true)).toEqual({ x: 700, y: 500 })
  })

})
