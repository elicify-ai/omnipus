// RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), US-5 AS-1 (TDD
// test 15) and US-5 AS-3 / MIN-008 (TDD test 69) — va-qa3 dispatch, P5.
//
// Oracle (spec lines 1274-1290, 1961-1968, 2151-2152):
//   US-5 "Independent test": render the Library tree over a fixture with
//   one view of each of the 8 `kind` values plus one with no `kind`; assert
//   9 distinct rendered icon states (8 + fallback).
//   AS-1: "each view's icon matches its view.kind (D-ICON's 8-way mapping)
//   and a view with no view.kind shows the fallback view icon (EC-3) —
//   never the generic unknown-file icon".
//   Dataset C (rows C1-C8): table->table icon, list->list icon,
//   tiles->tiles icon, board->board icon, calendar->calendar icon,
//   summary->summary icon, trend->trend icon, breakdown->breakdown icon.
//   AS-3/MIN-008: "the icon carries an accessible name ... stating the kind
//   in words ('Calendar view')" — this spec fixes the REQUIREMENT (a text
//   alternative naming the kind), not the exact wording; the 9 glyphs and
//   their exact accessible-name strings are the implementing lead's choice
//   at RED/GREEN time (design-system table, spec line 1305).
//
// The real component under test is LibraryEntryRow.tsx (there is no
// LibraryTree.tsx file — this test file's name is TDD Plan's own naming for
// test 15/69; LibraryEntryRow is the Library tree's actual per-entry
// renderer, confirmed by reading it in full and by
// LibraryEntryRow.test.tsx's own icon-selection convention: an icon's
// accessible name is asserted via `[aria-label="..."]` on the rendered
// container, e.g. "Mounted folder" for LibraryMounts.test.tsx's badge —
// this file follows the SAME convention for kind icons/badges).
//
// Confirmed by direct read (2026-09-29): LibraryEntryRow's icon-selection
// logic (containerIcon -> is_dir/isVault -> fileTypeMeta(entry.name,
// entry.mime)) never reads `entry.view` or `entry.view.kind` anywhere — a
// `.view` file with `view.kind` set renders with whatever generic icon
// fileTypeMeta picks for its extension, carrying no kind-naming
// aria-label/title at all. LibraryEntryView.kind (contract, generated
// openapi-types.ts) already exists and is typed — only the SPA-side
// dispatch on it is missing (D-CONTRACT landed via commit 562acdc32; the
// gap is consumption, not the wire type).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/library/LibraryTree.viewKindIcon.red.test.ts
import { describe, it, expect } from 'vitest'
import { createElement } from 'react'
import { render } from '@testing-library/react'
import { LibraryEntryRow } from './LibraryEntryRow'
import type { LibraryEntry } from '@/lib/api'
import type { components } from '@/lib/api/generated/openapi-types'

// This file is named `.red.test.ts` (not `.tsx`, per the va-qa3 dispatch's
// exact target path) — esbuild's default `ts` loader does not transform
// JSX, so rendering uses `createElement` rather than a `<LibraryEntryRow
// .../>` literal, to keep a real component-parse/build failure from
// masquerading as this test's "red" (per elicify-test-writing's "fails for
// the right reason" rule).

type ViewKind = components['schemas']['ViewKind']

// The 8 kinds Dataset C (rows C1-C8) and US-5's "Independent test" fix as
// the complete set — order matches the spec's own C1..C8 table.
const ALL_VIEW_KINDS: ViewKind[] = [
  'table',
  'list',
  'tiles',
  'board',
  'calendar',
  'summary',
  'trend',
  'breakdown',
]

function viewEntry(kind: ViewKind | undefined, name: string): LibraryEntry {
  return {
    name,
    path: name,
    is_dir: false,
    is_hidden: false,
    size: 42,
    modified_at: '2026-09-29T00:00:00Z',
    is_text_editable: true,
    is_view: true,
    ...(kind === undefined ? {} : { view: { kind, name, label: name } }),
  } as LibraryEntry
}

function renderRow(e: LibraryEntry) {
  return render(
    createElement(LibraryEntryRow, {
      workspaceId: 'ws-1',
      entry: e,
      selected: false,
      onOpenDirectory: () => {},
      onSelectFile: () => {},
      onDownload: () => {},
      onRename: () => {},
      onTransfer: () => {},
      onDelete: () => {},
      onUnmount: () => {},
    }),
  )
}

// accessibleNames collects every aria-label/title in the rendered row that
// names the KIND ICON specifically — the "accessible name ... stating the
// kind in words" MIN-008 requires, however the implementing lead attaches
// it (aria-label directly, or a tooltip component that ends up rendering
// one). The row's own "Actions for <entry.name>" menu button (confirmed:
// LibraryEntryRow.tsx's only pre-existing aria-label today) is excluded —
// it names the FILE, not the kind, and would otherwise leak the fixture's
// own filename into a false match.
function accessibleNames(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('[aria-label], [title]'))
    .filter((el) => !(el.getAttribute('aria-label') ?? '').startsWith('Actions for '))
    .map((el) => el.getAttribute('aria-label') ?? el.getAttribute('title') ?? '')
}

describe('Library tree row — per-kind view icon (US-5 AS-1, TDD test 15)', () => {
  it.each(ALL_VIEW_KINDS)(
    'a view with kind "%s" carries an accessible name naming that kind',
    (kind) => {
      const { container } = renderRow(viewEntry(kind, 'item.view'))
      const names = accessibleNames(container)
      expect(
        names.some((n) => n.toLowerCase().includes(kind)),
        `expected some element's aria-label/title to name kind "${kind}" (Dataset C row for ` +
          `"${kind}" -> "${kind} icon"); found accessible names: ${JSON.stringify(names)}. ` +
          "LibraryEntryRow never reads entry.view.kind today — it only dispatches on " +
          'is_dir/isVault/fileTypeMeta(name, mime), so no kind-naming accessible name exists yet.',
      ).toBe(true)
    },
  )

  it('a view with NO kind shows the fallback view icon (EC-3), never the generic unknown-file icon', () => {
    const withKind = renderRow(viewEntry('table', 'item.view'))
    const withoutKind = renderRow(viewEntry(undefined, 'item.view'))
    const namesWithKind = accessibleNames(withKind.container)
    const namesWithoutKind = accessibleNames(withoutKind.container)
    // EC-3's fallback must still be a VIEW icon, not the same "unknown file"
    // treatment a non-view file with an unrecognized extension gets — so its
    // accessible-name set must differ from the "table" kind's, and must not
    // itself claim to be "table" (or any other real kind).
    expect(
      namesWithoutKind.length > 0,
      "a view with no view.kind must still carry SOME accessible name (the fallback view icon), " +
        `not silently render with none. Found: ${JSON.stringify(namesWithoutKind)}`,
    ).toBe(true)
    expect(namesWithoutKind).not.toEqual(namesWithKind)
  })

  it('9 distinct icon states: the 8 kinds plus the fallback never collide on the same accessible name', () => {
    const renders = [...ALL_VIEW_KINDS.map((k) => viewEntry(k, 'item.view')), viewEntry(undefined, 'item.view')]
    const signatures = renders.map((e) => {
      const { container } = renderRow(e)
      return accessibleNames(container).slice().sort().join('|')
    })
    const distinct = new Set(signatures)
    expect(
      distinct.size,
      `expected 9 distinct icon-state signatures (8 kinds + fallback per US-5's Independent Test), ` +
        `got ${distinct.size} distinct value(s) across ${signatures.length} renders: ` +
        `${JSON.stringify(signatures)}. Today every one of these renders identically (fileTypeMeta ` +
        "keyed only on name/mime), since entry.view.kind is never read.",
    ).toBe(9)
  })
})

describe('Library tree row icon — accessible name states the kind in words (US-5 AS-3, MIN-008, TDD test 69)', () => {
  it.each(ALL_VIEW_KINDS)(
    'kind "%s" accessible name is a real word, not a bare icon-only glyph',
    (kind) => {
      const { container } = renderRow(viewEntry(kind, 'item.view'))
      const names = accessibleNames(container)
      const match = names.find((n) => n.toLowerCase().includes(kind))
      expect(
        match,
        `MIN-008 requires the kind icon to carry an accessible name stating the kind in words ` +
          `(e.g. "Calendar view") — found no aria-label/title naming "${kind}" among: ` +
          `${JSON.stringify(names)}.`,
      ).toBeTruthy()
      // "in words", not a bare kind token with no context (MIN-008's own
      // example is "Calendar view", not "calendar") — require at least one
      // extra word/character beyond the bare kind token.
      expect(
        (match ?? '').length,
        `accessible name "${match}" for kind "${kind}" is no longer than the bare kind token — ` +
          'MIN-008 asks for the kind stated "in words" (e.g. "Calendar view"), not a bare token.',
      ).toBeGreaterThan(kind.length)
    },
  )
})
