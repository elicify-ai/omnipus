// embedReservedHeights.test.ts — an oracle for EMB-066's per-kind claim.
//
// THE CLAIM, AND WHY IT HAD NO TEST. Three of the Step 6 mount files state in
// their own doc comments that a kind's reserved height is chosen FOR THAT KIND
// — "the smallest reserved height among the Step 6 kinds (EMB-066 / test 96:
// reserved heights differ per kind, not one constant reused everywhere)" on
// audio, "smaller than a saved-view embed's reserved height, larger than a
// single-line notice (EMB-066 / test 96: heights differ per kind)" on the
// query fence. Until this file, each constant appeared ONLY in its own source
// file and in nothing that could disagree with it: setting
// AUDIO_EMBED_RESERVED_HEIGHT_PX and VIDEO_EMBED_RESERVED_HEIGHT_PX to the
// same number broke no test anywhere, so "differ per kind" was an assertion
// the codebase made about itself and never checked.
//
// This file is the cross-kind check. The per-kind half — that each constant is
// the height the wrapper ACTUALLY reserves while its embed is unmounted — is
// asserted in each mount's own suite, where the renderer is already mocked
// (`KbAudioEmbedMount.test.tsx`, `KbVideoEmbedMount.test.tsx`,
// `KbPdfPageEmbedMount.test.tsx`, `KbQueryFenceEmbed.test.tsx`, each binding
// its constant to the wrapper's rendered `style.minHeight`). Neither half is
// sufficient alone: a constant that is distinct but never used would pass
// here and fail there, and four constants all wired correctly to one shared
// value would pass there and fail here. They ship together.
//
// COVERAGE, STATED HONESTLY: this covers the FOUR Step 6 constants, which are
// the exported ones. Three more reserved heights are module-private inside
// `knowledgeMarkdown.tsx` — BASE_EMBED (448), IMAGE_EMBED (240) and
// TRANSCLUSION (72) — and nothing here or anywhere else would notice if two
// of those collapsed onto one number. Widening this file would mean exporting
// them purely for a test, which is a larger change than the gap warrants;
// recorded here so the limit is visible rather than assumed away.
//
// The heavy leaf renderers are mocked because nothing below reads them: this
// file is about four numbers and the modules that own them.

import { describe, it, expect, vi } from 'vitest'

vi.mock('./LibraryAudioPreview', () => ({ LibraryAudioPreview: () => null }))
vi.mock('./LibraryVideoPreview', () => ({ LibraryVideoPreview: () => null }))
vi.mock('./LibraryPdfPreview', () => ({ LibraryPdfPreview: () => null }))

import { AUDIO_EMBED_RESERVED_HEIGHT_PX } from './KbAudioEmbedMount'
import { VIDEO_EMBED_RESERVED_HEIGHT_PX } from './KbVideoEmbedMount'
import { PDF_PAGE_EMBED_RESERVED_HEIGHT_PX } from './KbPdfPageEmbedMount'
import { QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX } from './KbQueryFenceEmbed'

const RESERVED_HEIGHTS = {
  audio: AUDIO_EMBED_RESERVED_HEIGHT_PX,
  video: VIDEO_EMBED_RESERVED_HEIGHT_PX,
  'pdf page': PDF_PAGE_EMBED_RESERVED_HEIGHT_PX,
  'query fence': QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX,
} as const

describe('EMB-066 — reserved heights differ per kind, not one constant reused everywhere', () => {
  it('gives every Step 6 embed kind its OWN height', () => {
    const entries = Object.entries(RESERVED_HEIGHTS)
    const distinct = new Set(entries.map(([, px]) => px))

    // The named-pair form so a failure says WHICH two kinds collapsed onto one
    // number, rather than only that a Set was too small.
    for (let i = 0; i < entries.length; i++) {
      for (let j = i + 1; j < entries.length; j++) {
        const [aKind, aPx] = entries[i]!
        const [bKind, bPx] = entries[j]!
        expect(
          aPx,
          `${aKind} and ${bKind} reserve the same height (${aPx}px) — EMB-066 requires a per-kind reservation`,
        ).not.toBe(bPx)
      }
    }
    expect(distinct.size).toBe(entries.length)
  })

  it('reserves the least height OF THE STEP 6 KINDS for audio, the one single-control-bar kind', () => {
    // KbAudioEmbedMount's own doc comment: "A compact, single-control-bar kind
    // — the smallest reserved height among the Step 6 kinds". Asserted here so
    // that sentence is checkable rather than decorative.
    //
    // SCOPED DELIBERATELY, and the scope is the accurate claim: audio's 96px
    // is NOT the smallest reservation in the file — `knowledgeMarkdown.tsx`'s
    // private TRANSCLUSION_RESERVED_HEIGHT_PX is 72. "Smallest among the Step
    // 6 kinds" is what the source says and what is asserted here; do not
    // widen this to "smallest anywhere", which is false.
    const others = [
      VIDEO_EMBED_RESERVED_HEIGHT_PX,
      PDF_PAGE_EMBED_RESERVED_HEIGHT_PX,
      QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX,
    ]
    for (const px of others) {
      expect(AUDIO_EMBED_RESERVED_HEIGHT_PX).toBeLessThan(px)
    }
  })

  it('reserves less for a compact query-fence result list than for a video or a PDF page', () => {
    // KbQueryFenceEmbed's own doc comment: "A short results-list shape —
    // smaller than a saved-view embed's reserved height, larger than a
    // single-line notice".
    expect(QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX).toBeLessThan(VIDEO_EMBED_RESERVED_HEIGHT_PX)
    expect(QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX).toBeLessThan(PDF_PAGE_EMBED_RESERVED_HEIGHT_PX)
    expect(QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX).toBeGreaterThan(AUDIO_EMBED_RESERVED_HEIGHT_PX)
  })

  it('reserves a positive, plausible height for every kind', () => {
    // A zero or negative reservation would defeat EMB-066 entirely (the page
    // shifts under the reader on mount) while still being "distinct".
    for (const [kind, px] of Object.entries(RESERVED_HEIGHTS)) {
      expect(px, `${kind} reserves a non-positive height`).toBeGreaterThan(0)
      expect(Number.isInteger(px), `${kind} reserves a non-integer pixel height`).toBe(true)
    }
  })
})
