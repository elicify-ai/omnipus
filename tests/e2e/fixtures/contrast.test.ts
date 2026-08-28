/**
 * contrast.test.ts — unit coverage for the WCAG arithmetic behind the ADR-068
 * FR-041 focus-ring row, and for the fixture derivation the e2e rows expect.
 *
 * These run in vitest (vite.config.ts includes `tests/**\/*.test.ts`), so the
 * `spa` gate executes them. That matters: the Playwright rows themselves only
 * run on the Wave D `e2e` gate, and a focus-ring assertion whose maths was
 * never checked would be a rule that passes or fails for the wrong reason.
 *
 * The expected ratios are published WCAG/WebAIM reference values, NOT numbers
 * read back out of this implementation (oracle independence).
 */

import { describe, expect, it } from 'vitest'
import { contrastRatio, flatten, focusRingContrast, parseRgb, relativeLuminance } from './contrast'
import { popularTiles, PROVIDERS_CATALOG } from './onboarding-stubs'

const WHITE = { r: 255, g: 255, b: 255, a: 1 }
const BLACK = { r: 0, g: 0, b: 0, a: 1 }

describe('parseRgb', () => {
  it('parses the rgb() and rgba() forms getComputedStyle returns', () => {
    expect(parseRgb('rgb(212, 175, 55)')).toEqual({ r: 212, g: 175, b: 55, a: 1 })
    expect(parseRgb('rgba(10, 10, 11, 0.5)')).toEqual({ r: 10, g: 10, b: 11, a: 0.5 })
    // The space/slash form Chromium emits for some computed colours.
    expect(parseRgb('rgb(10 10 11 / 0.25)')).toEqual({ r: 10, g: 10, b: 11, a: 0.25 })
  })

  it('returns null for values that carry no colour', () => {
    expect(parseRgb('currentcolor')).toBeNull()
    expect(parseRgb('none')).toBeNull()
    expect(parseRgb('MISSING')).toBeNull()
  })

  it('parses `transparent` as fully transparent black rather than failing', () => {
    // getComputedStyle resolves `transparent` to rgba(0, 0, 0, 0).
    expect(parseRgb('rgba(0, 0, 0, 0)')).toEqual({ r: 0, g: 0, b: 0, a: 0 })
  })
})

describe('relativeLuminance', () => {
  it('is 0 for black and 1 for white (WCAG definition endpoints)', () => {
    expect(relativeLuminance(BLACK)).toBeCloseTo(0, 6)
    expect(relativeLuminance(WHITE)).toBeCloseTo(1, 6)
  })

  it('uses the linear branch below the 0.03928 threshold', () => {
    // 8/255 = 0.0314 → linear branch: 0.0314/12.92 for every channel.
    const grey8 = { r: 8, g: 8, b: 8, a: 1 }
    expect(relativeLuminance(grey8)).toBeCloseTo(8 / 255 / 12.92, 8)
  })
})

describe('contrastRatio', () => {
  it('is 21:1 for black on white — the maximum the formula can produce', () => {
    expect(contrastRatio(WHITE, BLACK)).toBeCloseTo(21, 4)
  })

  it('is 1:1 for a colour against itself', () => {
    expect(contrastRatio(WHITE, WHITE)).toBeCloseTo(1, 6)
  })

  it('is order-independent', () => {
    const gold = { r: 212, g: 175, b: 55, a: 1 }
    expect(contrastRatio(gold, BLACK)).toBeCloseTo(contrastRatio(BLACK, gold), 10)
  })

  it('reproduces the published WebAIM boundary greys on white', () => {
    // #767676 is the darkest grey that still passes 4.5:1 on white (4.54:1).
    expect(contrastRatio({ r: 0x76, g: 0x76, b: 0x76, a: 1 }, WHITE)).toBeCloseTo(4.54, 1)
    // #949494 is the 3:1 boundary grey on white (3.03:1).
    expect(contrastRatio({ r: 0x94, g: 0x94, b: 0x94, a: 1 }, WHITE)).toBeCloseTo(3.03, 1)
  })
})

describe('flatten', () => {
  it('composites a half-transparent white over black to mid grey', () => {
    expect(flatten({ r: 255, g: 255, b: 255, a: 0.5 }, BLACK)).toEqual({
      r: 127.5,
      g: 127.5,
      b: 127.5,
      a: 1,
    })
  })

  it('leaves an opaque colour untouched', () => {
    expect(flatten({ r: 12, g: 34, b: 56, a: 1 }, WHITE)).toEqual({ r: 12, g: 34, b: 56, a: 1 })
  })
})

describe('focusRingContrast', () => {
  const sample = (outlineColor: string, backgroundColor: string) => ({
    selector: '#x',
    outlineColor,
    outlineStyle: 'solid',
    outlineWidth: '1px',
    backgroundColor,
  })

  it("passes 3:1 for the brand's Forge Gold ring on Deep Space Black", () => {
    // The one ring globals.css draws: 1px solid var(--color-accent) #d4af37,
    // on var(--color-primary) #0a0a0b. If either token ever changes so the ring
    // drops under 3:1, this test — and the Playwright row — must fail.
    const ratio = focusRingContrast(sample('rgb(212, 175, 55)', 'rgb(10, 10, 11)'))
    expect(ratio).not.toBeNull()
    expect(ratio as number).toBeGreaterThanOrEqual(3)
  })

  it('fails a ring that is nearly the colour of its own background', () => {
    const ratio = focusRingContrast(sample('rgb(12, 12, 13)', 'rgb(10, 10, 11)'))
    expect(ratio as number).toBeLessThan(3)
  })

  it('accounts for the ring alpha rather than ignoring it', () => {
    const opaque = focusRingContrast(sample('rgb(212, 175, 55)', 'rgb(10, 10, 11)')) as number
    const faded = focusRingContrast(sample('rgba(212, 175, 55, 0.2)', 'rgb(10, 10, 11)')) as number
    expect(faded).toBeLessThan(opaque)
  })

  it('returns null when the browser reported no measurable colour', () => {
    expect(focusRingContrast(sample('MISSING', 'rgb(10, 10, 11)'))).toBeNull()
    expect(focusRingContrast(sample('rgb(212, 175, 55)', 'MISSING'))).toBeNull()
  })
})

describe('popularTiles (the e2e rows’ expectation source)', () => {
  it('yields the 8 Popular companies of FR-022, in catalog order, without repeats', () => {
    const tiles = popularTiles()
    expect(tiles).toHaveLength(8)
    expect(new Set(tiles.map((t) => t.company)).size).toBe(8)
    expect(new Set(tiles.map((t) => t.id)).size).toBe(8)
    // Catalog order: each tile's id appears in the document before the next.
    const order = PROVIDERS_CATALOG.providers.map((p) => p.id)
    const indices = tiles.map((t) => order.indexOf(t.id))
    expect(indices).toEqual([...indices].sort((a, b) => a - b))
  })

  it('names only providers the catalog actually marks popular', () => {
    for (const tile of popularTiles()) {
      const row = PROVIDERS_CATALOG.providers.find((p) => p.id === tile.id)
      expect(row?.tier).toBe('popular')
      expect(row?.company).toBe(tile.company)
    }
  })
})
