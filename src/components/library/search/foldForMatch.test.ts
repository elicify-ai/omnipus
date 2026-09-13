// foldForMatch.test.ts — UAT 2026-09-13 D-129 (web half): one folding rule
// for the search bar's client-side comparisons. Expected values are derived
// from the defect's own examples (`cafe` ↔ `Café`, `resume` ↔ `résumé`,
// `zurich` ↔ `Zürich`, NFC `café.png` ↔ NFD `café (1).png`), not from the
// implementation.

import { describe, it, expect } from 'vitest'
import { foldForMatch, foldWithMap, foldedMatchRanges } from './foldForMatch'

const NFC_CAFE = 'café' // café, precomposed
const NFD_CAFE = 'café' // café, e + combining acute

describe('foldForMatch — UAT D-129', () => {
  it('folds the accents the defect names: é, ü, and résumé', () => {
    expect(foldForMatch('Café')).toBe('cafe')
    expect(foldForMatch('Zürich')).toBe('zurich')
    expect(foldForMatch('résumé')).toBe('resume')
  })

  it('treats the NFC and the NFD spelling of café as the same word', () => {
    expect(NFC_CAFE).not.toBe(NFD_CAFE) // the two really are different strings
    expect(foldForMatch(NFC_CAFE)).toBe(foldForMatch(NFD_CAFE))
    expect(foldForMatch(`${NFC_CAFE}.png`)).toBe('cafe.png')
    expect(foldForMatch(`${NFD_CAFE} (1).png`)).toBe('cafe (1).png')
  })

  it('is a no-op on plain ASCII beyond lower-casing', () => {
    expect(foldForMatch('Invoices 2026')).toBe('invoices 2026')
  })
})

describe('foldWithMap — UAT D-129', () => {
  it('maps every folded character back to the original code point that produced it', () => {
    const { folded, map } = foldWithMap(`x${NFD_CAFE}y`) // x c a f e ́ y  (7 UTF-16 units)
    expect(folded).toBe('xcafey')
    // folded 'y' (index 5) comes from original index 6 — the combining mark
    // at original index 5 produced nothing and has no entry.
    expect(map).toEqual([0, 1, 2, 3, 4, 6, 7])
  })
})

describe('foldedMatchRanges — UAT D-129', () => {
  it('finds `cafe` inside "Café menu" and returns the range of the ORIGINAL text', () => {
    const text = 'Café menu'
    const ranges = foldedMatchRanges(text, ['cafe'])
    expect(ranges).toEqual([[0, 4]])
    expect(text.slice(0, 4)).toBe('Café')
  })

  it('finds an NFC query inside an NFD filename and highlights the whole accented word', () => {
    const text = `${NFD_CAFE} (1).png`
    const ranges = foldedMatchRanges(text, [NFC_CAFE])
    expect(ranges).toEqual([[0, 5]])
    expect(text.slice(0, 5)).toBe(NFD_CAFE)
  })

  it('returns non-overlapping, left-to-right ranges for several terms', () => {
    const text = 'résumé for Zürich'
    expect(foldedMatchRanges(text, ['resume', 'zurich'])).toEqual([
      [0, 6],
      [11, 17],
    ])
  })

  it('returns nothing when no term folds to a substring, and ignores empty terms', () => {
    expect(foldedMatchRanges('Café', ['tea', ''])).toEqual([])
    expect(foldedMatchRanges('Café', [])).toEqual([])
  })
})
