import { describe, it, expect } from 'vitest'
import { initialOf } from './utils'

describe('initialOf', () => {
  it('falls back to "?" for an empty name', () => {
    expect(initialOf('')).toBe('?')
  })

  it('falls back to "?" for a whitespace-only name', () => {
    expect(initialOf('   ')).toBe('?')
  })

  it('skips leading whitespace instead of returning a blank initial', () => {
    expect(initialOf(' Bob')).toBe('B')
  })

  it('uppercases a plain name to its first letter', () => {
    expect(initialOf('General Assistant')).toBe('G')
  })

  it('does not alter a non-letter first character', () => {
    expect(initialOf('@x')).toBe('@')
  })

  it('reads a full astral code point instead of half a surrogate pair', () => {
    // U+1F469 (WOMAN) is outside the BMP — charAt(0) would return a lone
    // surrogate that renders as U+FFFD. Array.from iterates by code point.
    expect(initialOf('👩x')).toBe('👩')
  })

  it('documents current behavior: a ZWJ-joined emoji cluster still splits at the first code point', () => {
    // '👩‍💻' (woman technologist) is WOMAN + ZWJ + LAPTOP, three code points
    // joined into one grapheme. Array.from splits by code point, not
    // grapheme cluster, so only the leading WOMAN code point is returned —
    // not the whole joined character. Pinning current behavior, not a claim
    // it's correct for every possible name (see utils.ts doc comment).
    expect(initialOf('👩‍💻')).toBe('👩')
  })
})
