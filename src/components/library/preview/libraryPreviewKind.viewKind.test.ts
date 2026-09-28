// RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 13 and 42.
//
// Oracle: US-6 AS-1 ("Given a .view file, When classifyLibraryEntry runs on
// its LibraryEntry, Then it returns the new 'view' kind... extension-matched
// exactly like 'base' is today, and placed after the mime-driven checks per
// the file's own documented ordering rule") and MIN-010 (".VIEW matches,
// matching the loader's existing extension-lowercasing behavior elsewhere").
//
// Today LIBRARY_PREVIEW_KINDS has exactly 10 members and none of them is
// 'view'; classifyLibraryEntry has no `e === 'view'` branch at all — a
// `.view`-extension, non-text-editable entry falls through to 'other'
// (verified by reading libraryPreviewKind.ts in full).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/library/preview/libraryPreviewKind.viewKind.test.ts
import { describe, it, expect } from 'vitest'
import { classifyLibraryEntry, LIBRARY_PREVIEW_KINDS } from './libraryPreviewKind'

describe('classifyLibraryEntry — the new "view" kind (US-6 AS-1, TDD test 13)', () => {
  it('includes "view" in the declared set of preview kinds', () => {
    expect((LIBRARY_PREVIEW_KINDS as readonly string[]).includes('view')).toBe(true)
  })

  it('classifies a .view file as "view", not "other", regardless of is_text_editable', () => {
    expect(classifyLibraryEntry({ name: 'Weekly Status.view', is_text_editable: false })).toBe(
      'view' as (typeof LIBRARY_PREVIEW_KINDS)[number]
    )
    expect(classifyLibraryEntry({ name: 'Weekly Status.view', is_text_editable: true })).toBe(
      'view' as (typeof LIBRARY_PREVIEW_KINDS)[number]
    )
  })

  it('decides "view" on the extension alone, never on the mime hint (mirrors the .base precedent)', () => {
    expect(
      classifyLibraryEntry({ name: 'Weekly Status.view', mime: 'image/png', is_text_editable: false })
    ).toBe('image')
  })
})

describe('classifyLibraryEntry — .VIEW is case-insensitive (MIN-010, TDD test 42)', () => {
  it('classifies an uppercase .VIEW extension identically to .view', () => {
    expect(classifyLibraryEntry({ name: 'NOTES.VIEW', is_text_editable: false })).toBe(
      'view' as (typeof LIBRARY_PREVIEW_KINDS)[number]
    )
  })
})
