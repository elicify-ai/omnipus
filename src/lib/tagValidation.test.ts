// tagValidation.test.ts — Dataset: Tag input boundaries (UI-side, SD-C8).
// planning-goals-spec.md Part C, "Dataset: Tag input boundaries" (12 rows).

import { describe, it, expect } from 'vitest'
import { normalizeTag, validateTag, graphemeLength, TAG_MAX_LENGTH, TAG_MAX_COUNT } from './tagValidation'

describe('normalizeTag', () => {
  it('lowercases and trims', () => {
    expect(normalizeTag('Release')).toBe('release')
    expect(normalizeTag('  spaced  ')).toBe('spaced')
    expect(normalizeTag('Q3 Release')).toBe('q3 release')
  })
})

describe('validateTag — boundary dataset', () => {
  it('row 1: empty input is rejected with no chip, no error message', () => {
    const r = validateTag('')
    expect(r.ok).toBe(false)
    expect(r.error).toBe('')
  })

  it('row 1b: whitespace-only input is also a silent no-op', () => {
    const r = validateTag('   ')
    expect(r.ok).toBe(false)
    expect(r.error).toBe('')
  })

  it('row 2: single char is accepted', () => {
    const r = validateTag('a')
    expect(r.ok).toBe(true)
    expect(r.value).toBe('a')
  })

  it('row 3: case is normalised on commit, not rejected', () => {
    const r = validateTag('Release')
    expect(r.ok).toBe(true)
    expect(r.value).toBe('release')
  })

  it('row 4: leading/trailing whitespace is trimmed', () => {
    const r = validateTag('  spaced  ')
    expect(r.ok).toBe(true)
    expect(r.value).toBe('spaced')
  })

  it('row 5: exactly 64 chars is accepted (at the cap)', () => {
    const tag = 'a'.repeat(TAG_MAX_LENGTH)
    const r = validateTag(tag)
    expect(r.ok).toBe(true)
    expect(r.value.length).toBe(64)
  })

  it('row 6: 65 chars is rejected with "Max 64 characters"', () => {
    const tag = 'a'.repeat(65)
    const r = validateTag(tag)
    expect(r.ok).toBe(false)
    expect(r.error).toBe('Max 64 characters')
  })

  it('row 7: the 16th tag is accepted (at the per-task cap)', () => {
    const existing = Array.from({ length: TAG_MAX_COUNT - 1 }, (_, i) => `tag-${i}`)
    const r = validateTag('tag-final', existing)
    expect(r.ok).toBe(true)
  })

  it('row 8: a 17th distinct tag is rejected with "Max 16 tags per task"', () => {
    const existing = Array.from({ length: TAG_MAX_COUNT }, (_, i) => `tag-${i}`)
    const r = validateTag('tag-overflow', existing)
    expect(r.ok).toBe(false)
    expect(r.error).toBe('Max 16 tags per task')
  })

  it('re-adding an already-present tag at the cap is not a count violation', () => {
    const existing = Array.from({ length: TAG_MAX_COUNT }, (_, i) => `tag-${i}`)
    const r = validateTag('tag-3', existing)
    expect(r.ok).toBe(true)
  })

  it('row 9: "Q3 Release" (space) normalises to "q3 release" — NOT rejected (SD-C8)', () => {
    const r = validateTag('Q3 Release')
    expect(r.ok).toBe(true)
    expect(r.value).toBe('q3 release')
  })

  it('row 10: "milestone:q3" prefix convention is accepted verbatim, never re-prefixed', () => {
    const r = validateTag('milestone:q3')
    expect(r.ok).toBe(true)
    expect(r.value).toBe('milestone:q3')
  })

  it('row 11: a script-like string is treated as inert text (no special-casing, no throw)', () => {
    const r = validateTag('<script>alert(1)</script>')
    expect(r.ok).toBe(true)
    expect(r.value).toBe('<script>alert(1)</script>'.toLowerCase())
  })

  it('row 12: a combining-character glyph is measured grapheme-safe (code points, not UTF-16 units)', () => {
    // "cafe" + a combining acute accent (U+0301) — one visible glyph built
    // from TWO code points, as opposed to the precomposed e-acute (U+00E9).
    const combining = 'café'
    expect(combining.length).toBe(5) // UTF-16 code units: c,a,f,e,[combining mark]
    expect(graphemeLength(combining)).toBe(5) // code points: same count here (no surrogate pairs)
    const r = validateTag(combining)
    expect(r.ok).toBe(true)
    expect(r.value).toBe(combining) // already lowercase — unchanged
  })

  it('grapheme length correctly counts a surrogate-pair (astral) character as one code point', () => {
    const withEmoji = 'tag-\u{1F600}' // one grinning-face code point, two UTF-16 units
    expect(withEmoji.length).toBe(6) // UTF-16 units: t,a,g,-,+2 surrogate units
    expect(graphemeLength(withEmoji)).toBe(5) // code points: t,a,g,-,[emoji]
  })
})
