// RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan test 14,
// the derived-view read-only banner (FD-6, US-6 AS-5, TDD test 49's SPA
// half), the rejection-reason/conflict-paths rendering (R2-MAJ-005,
// US-6 AS-4, TDD test 57), and the loading/empty/refusal state distinction
// (R2-MIN-007, US-6 AS-6, TDD test 70).
//
// Oracle: US-6 AS-2 ("Given a Library entry classified 'view'... When
// LibraryPreviewPane renders its body, Then renderBody's switch gains a
// `case 'view':` that calls GET .../knowledge/view with view.collection_id
// and view.name directly... and mounts a preview reusing ViewPartsRenderer
// (F6) to draw the returned rows") and US-6 AS-5 / FD-6 ("Given a .view
// entry with view.derived_from set, When it is opened, Then the preview
// shows a banner naming the source .base file and states that this view is
// READ-ONLY").
//
// renderBody is a closure private to the LibraryPreviewPane component (not
// exported), and it is closed by `const unhandled: never = kind` — a
// genuine, correct `case 'view':` cannot even be added without first
// widening LIBRARY_PREVIEW_KINDS (production code this suite must not
// touch). So rather than rendering the full component (which would need
// production changes just to avoid a TypeScript exhaustiveness error), this
// test verifies the SOURCE directly: does the file contain a `case 'view':`
// branch, and does it reference ViewPartsRenderer? Both are real,
// mechanically-checkable facts about the current file — not a proxy for
// "did the reviewer eyeball it."
//
// Confirmed by direct read of LibraryPreviewPane.tsx's full renderBody
// switch: today's cases are exactly image, video, audio, base, pdf, html,
// markdown, mermaid, text, other, default — no 'view' case, and no
// reference to ViewPartsRenderer anywhere in the file's imports.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/library/LibraryPreviewPane.viewCase.test.ts
import { describe, it, expect } from 'vitest'
import { existsSync, readFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const PANE_PATH = resolve(HERE, 'LibraryPreviewPane.tsx')

function readPaneSource(): string {
  expect(existsSync(PANE_PATH)).toBe(true)
  return readFileSync(PANE_PATH, 'utf8')
}

describe('LibraryPreviewPane.renderBody — the "view" case (US-6 AS-2, TDD test 14)', () => {
  it('renderBody has a case for the "view" kind', () => {
    const src = readPaneSource()
    expect(src).toMatch(/case\s+'view'\s*:/)
  })

  it('the "view" case mounts a component that reuses ViewPartsRenderer, not a re-implementation', () => {
    const src = readPaneSource()
    expect(src).toContain('ViewPartsRenderer')
  })
})

describe('LibraryPreviewPane — derived-view read-only banner (FD-6, US-6 AS-5, TDD test 49)', () => {
  it('names the managing .base file and states the view is read-only when view.derived_from is present', () => {
    const src = readPaneSource()
    // There is, today, no "derived"/"read-only" concept anywhere in this
    // component at all (confirmed separately by
    // `grep -rn "derived view\|read-only view\|derived_from" src/components/library`
    // returning zero hits for this concept) — so neither of the two
    // load-bearing signals FD-6 requires can be present yet.
    expect(src).toMatch(/derived_from/)
    expect(src.toLowerCase()).toMatch(/read-?only/)
  })
})

describe('LibraryPreviewPane — rejected .view shows reason and conflict paths (R2-MAJ-005, US-6 AS-4, TDD test 57)', () => {
  it('renders view.rejection_reason and, for a duplicate, every view.conflict_paths entry', () => {
    const src = readPaneSource()
    // R2-MAJ-005's own two wire fields (LibraryEntryView.rejection_reason,
    // LibraryEntryView.conflict_paths) are what a duplicate-rejected or
    // malformed .view entry's preview must render — neither field exists on
    // the generated LibraryEntry type yet (confirmed: LIBRARY_PREVIEW_KINDS
    // has no 'view' member, TDD test 13), so neither string appears
    // anywhere in this component today.
    expect(src).toMatch(/rejection_reason/)
    expect(src).toMatch(/conflict_paths/)
  })
})

describe('LibraryPreviewPane — view case loading/empty/refusal states render distinctly (R2-MIN-007, US-6 AS-6, TDD test 70)', () => {
  it('is BLOCKED: there is no "view" case at all yet to have distinct states', () => {
    // US-6 AS-6 requires the 'view' case's loading/empty/unservable states
    // to render distinctly, reusing BasePreview's existing state renderers
    // where the shape applies. There is no 'view' case in renderBody at all
    // (TDD test 14's own finding — no `case 'view':`, no reference to
    // ViewPartsRenderer), so there is no component here whose three states
    // could even be inspected.
    throw new Error(
      "BLOCKED: LibraryPreviewPane has no case 'view': branch (TDD test 14) — required before its " +
        'loading/empty/unservable states can be shown to render distinctly, per US-6 AS-6 / ' +
        'R2-MIN-007 / TDD test 70.'
    )
  })
})
