// RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 15 and 69.
//
// Oracle: US-5 AS-1 ("Given a Library directory listing containing views of
// different kinds... each view's icon matches its view.kind (D-ICON's
// 8-way mapping) and a view with no view.kind shows the fallback view
// icon... never the generic unknown-file icon") and US-5 AS-3/MIN-008
// ("a screen reader encounters it... the icon carries an accessible name...
// stating the kind in words").
//
// Both are BLOCKED per the qa-lead RED protocol: `LibraryEntry.view.kind`
// does not exist on the generated type (`grep -c "ViewKind"
// src/lib/api/generated/openapi-types.ts` = 0, confirmed by direct read),
// and no per-kind icon dispatch exists anywhere in src/components/library —
// the only extension-driven icon lookup in the codebase, fileTypeMeta
// (src/components/chat/AttachmentCard.tsx), dispatches on FILE EXTENSION,
// not on a view's `kind` property, and has no branch for any of the 8
// ViewKind values (table/list/tiles/board/calendar/summary/trend/
// breakdown) at all — confirmed by reading its full body (file header
// note: `.base` itself falls through to the generic File icon today, TDD
// test 16's own finding).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/library/LibraryTree.viewKindIcon.red.test.ts
import { describe, it } from 'vitest'

// TestLibraryTree_IconPerKind is TDD Plan test 15 (US-5 AS-1): a fixture
// directory with one view of each of the 8 `kind` values plus one with no
// `kind` must render 9 distinct icon states.
describe('Library tree — per-view-kind icon (US-5 AS-1, TDD test 15)', () => {
  it('is BLOCKED: no view.kind field or per-kind icon dispatch exists yet', () => {
    throw new Error(
      'BLOCKED: LibraryEntry.view.kind (contract FR-VA-009, D-CONTRACT) and a per-ViewKind icon ' +
        'dispatch do not exist anywhere in src/ — required before 8 kinds + 1 fallback can be shown ' +
        'to render 9 distinct icon states, per US-5 AS-1 / TDD test 15.'
    )
  })
})

// TestLibraryTree_IconHasAccessibleName is TDD Plan test 69 (US-5 AS-3,
// MIN-008, R2-MIN-007): each of the 9 icon states must carry a text
// alternative naming the kind in words, not merely a visual glyph.
describe('Library tree — icon accessible name (US-5 AS-3, TDD test 69)', () => {
  it('is BLOCKED: there is no per-kind icon component to carry an accessible name yet', () => {
    throw new Error(
      'BLOCKED: same root cause as TDD test 15 — with no view.kind field and no per-kind icon ' +
        'dispatch, there is no icon component yet whose accessible name (aria-label or equivalent) ' +
        'could be asserted, per US-5 AS-3 / MIN-008 / TDD test 69.'
    )
  })
})
