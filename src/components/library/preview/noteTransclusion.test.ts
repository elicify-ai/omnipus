// noteTransclusion.test.ts — ADR-083 US-7, EMB-059 (client-side slicing).

import { describe, it, expect } from 'vitest'
import { sliceTranscludedContent } from './noteTransclusion'

const NOTE = `# Title

Intro paragraph.

## Results

First results line.
Second results line.

### Sub-results

Nested detail. ^abc123

## Appendix

Appendix text.
`

describe('sliceTranscludedContent — no fragment', () => {
  it('returns the whole document unchanged', () => {
    const s = sliceTranscludedContent(NOTE)
    expect(s.found).toBe(true)
    expect(s.text).toBe(NOTE)
  })
})

describe('sliceTranscludedContent — a heading section (US-7 AS-2)', () => {
  it('slices the named heading AND its subsections, stopping at the next heading of equal or shallower depth', () => {
    const s = sliceTranscludedContent(NOTE, 'Results')
    expect(s.found).toBe(true)
    expect(s.text).toContain('## Results')
    expect(s.text).toContain('First results line.')
    expect(s.text).toContain('### Sub-results')
    expect(s.text).toContain('Nested detail.')
    // The sibling "## Appendix" section is NOT included — this is the half
    // of the test that a "just return everything after the heading" bug
    // cannot pass.
    expect(s.text).not.toContain('## Appendix')
    expect(s.text).not.toContain('Appendix text.')
    // Nor does it leak the content BEFORE the heading.
    expect(s.text).not.toContain('Intro paragraph.')
  })

  it('matches case-insensitively when no exact-case heading matched', () => {
    const s = sliceTranscludedContent(NOTE, 'results')
    expect(s.found).toBe(true)
    expect(s.text).toContain('## Results')
  })

  it('prefers the EXACT-case heading over a differently-cased one when both exist', () => {
    // NOTE (above) has only ONE heading that could ever match "results" —
    // the case-insensitive fallback pass finds it regardless of whether the
    // exact-match pass ran at all, so that test alone cannot tell "exact
    // first, then case-insensitive" apart from "case-insensitive only". A
    // fixture with BOTH "## Results" and "## results" as real, distinct
    // headings is the only way to observe exact-match precedence as a rule:
    // querying "Results" and querying "results" must land on their OWN
    // same-cased heading, not both collapse onto whichever comes first.
    const DUPLICATE_CASE_NOTE = `# Title

## Results

Capitalized results content.

## results

Lowercase results content.
`
    const exact = sliceTranscludedContent(DUPLICATE_CASE_NOTE, 'Results')
    expect(exact.found).toBe(true)
    expect(exact.text).toContain('Capitalized results content.')
    expect(exact.text).not.toContain('Lowercase results content.')

    const lower = sliceTranscludedContent(DUPLICATE_CASE_NOTE, 'results')
    expect(lower.found).toBe(true)
    expect(lower.text).toContain('Lowercase results content.')
    expect(lower.text).not.toContain('Capitalized results content.')

    // The two queries must resolve to DIFFERENT sections. A mapper whose
    // exact-match pass was deleted (falling straight to case-insensitive
    // matching for every query) would return the SAME first-in-document
    // section ("## Results") for both — this is what that regression looks
    // like from the caller's side.
    expect(exact.text).not.toBe(lower.text)
  })

  it('a heading that does not exist reports not-found, never a silent empty slice', () => {
    const s = sliceTranscludedContent(NOTE, 'Nonexistent Section')
    expect(s.found).toBe(false)
    expect(s.text).toBe('')
  })

  it('the LAST section in a document runs to the end of the document, not to nothing', () => {
    const s = sliceTranscludedContent(NOTE, 'Appendix')
    expect(s.found).toBe(true)
    expect(s.text).toContain('Appendix text.')
  })
})

describe('sliceTranscludedContent — an anchored block (US-7 AS-3)', () => {
  it('slices ONLY the block carrying the anchor, with the anchor token itself stripped', () => {
    const s = sliceTranscludedContent(NOTE, undefined, 'abc123')
    expect(s.found).toBe(true)
    expect(s.text).toContain('Nested detail.')
    expect(s.text).not.toContain('^abc123')
    // Not the heading above it, and not the sibling section below it.
    expect(s.text).not.toContain('### Sub-results')
    expect(s.text).not.toContain('Appendix text.')
  })

  it('a block anchor that does not exist reports not-found', () => {
    const s = sliceTranscludedContent(NOTE, undefined, 'doesnotexist')
    expect(s.found).toBe(false)
  })

  it('a multi-line paragraph anchored at its last line includes every line of that paragraph', () => {
    const multi = `Para line one.\nPara line two. ^blk1\n\nOther paragraph.\n`
    const s = sliceTranscludedContent(multi, undefined, 'blk1')
    expect(s.found).toBe(true)
    expect(s.text).toContain('Para line one.')
    expect(s.text).toContain('Para line two.')
    expect(s.text).not.toContain('Other paragraph.')
  })
})

describe('sliceTranscludedContent — an empty note', () => {
  it('an all-whitespace document slices to empty text, found=true (a real empty note, not a missing fragment)', () => {
    const s = sliceTranscludedContent('   \n\n  ')
    expect(s.found).toBe(true)
    expect(s.text.trim()).toBe('')
  })
})
