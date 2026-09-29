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
import { render, queryAllByRole } from '@testing-library/react'
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

// Read the actual icon's text alternative, not the row filename's title or
// the Actions button's label. A label inside aria-hidden is not accessible.
// Include equivalent aria-labelledby and SVG <title> names, not only aria-label.
function accessibleNames(container: HTMLElement): string[] {
  const icon = container.querySelector('button[data-testid^="library-row-"] svg')
  const names: string[] = []
  for (let element: Element | null = icon; element && element.tagName !== 'BUTTON'; element = element.parentElement) {
    if (element.closest('[aria-hidden="true"]')) continue
    const ids = element.getAttribute('aria-labelledby')?.trim().split(/\s+/) ?? []
    const referenced = ids.map((id) => container.ownerDocument.getElementById(id)?.textContent?.trim()).filter(Boolean).join(' ')
    const title = Array.from(element.children).find((child) => child.tagName.toLowerCase() === 'title')?.textContent
    const name = element.getAttribute('aria-label') ?? (referenced || element.getAttribute('title') || title)
    if (name) names.push(name)
  }
  return names
}

// A tooltip or a label on a generic, non-image element is not enough: the
// icon (or its image-role wrapper) must be reachable with its accessible name.
function iconHasAccessibleName(container: HTMLElement, name: RegExp): boolean {
  const svg = container.querySelector('button[data-testid^="library-row-"] svg')
  return svg !== null && queryAllByRole(container, 'img', { name })
    .some((image) => image === svg || image.contains(svg))
}

// Phosphor glyph geometry, not the text alternative: changing only an aria
// label must not make one reused icon look like nine different icons in test 15.
// The design-system table specifies Phosphor for the eight view kinds.
function iconGeometry(container: HTMLElement): string {
  const icon = container.querySelector('button[data-testid^="library-row-"] svg')
  expect(icon, 'each row must render a view-kind glyph').not.toBeNull()
  const geometry = Array.from(icon?.querySelectorAll('path, rect, circle, line, polyline, polygon') ?? [])
    .map((shape) => shape.outerHTML)
    .join('|')
  expect(geometry, 'the icon must contain drawable geometry').not.toBe('')
  return geometry
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
      expect(iconHasAccessibleName(container, new RegExp(`\\b${kind}\\b`, 'i')),
        `the ${kind} glyph must be an accessible image named for its kind, not a hidden icon or a generic tooltip`).toBe(true)
    },
  )

  it('a view with NO kind shows the fallback view icon (EC-3), never the generic unknown-file icon', () => {
    const withKind = renderRow(viewEntry('table', 'item.view'))
    const withoutKind = renderRow(viewEntry(undefined, 'item.view'))
    const unknown = renderRow({ ...viewEntry(undefined, 'item.unknown-extension'), is_view: false })
    const namesWithoutKind = accessibleNames(withoutKind.container)
    expect(namesWithoutKind.some((n) => /\bview\b/i.test(n)),
      `the fallback icon must be named as a view: ${JSON.stringify(namesWithoutKind)}`).toBe(true)
    expect(iconHasAccessibleName(withoutKind.container, /\bview\b/i),
      'the fallback glyph needs a screen-reader-reachable view name').toBe(true)
    expect(iconGeometry(withoutKind.container)).not.toBe(iconGeometry(withKind.container))
    expect(iconGeometry(withoutKind.container)).not.toBe(iconGeometry(unknown.container))
  })

  it('9 distinct icon states: the 8 kinds plus the fallback never collide on the same accessible name', () => {
    const renders = [...ALL_VIEW_KINDS.map((k) => viewEntry(k, 'item.view')), viewEntry(undefined, 'item.view')]
    const rendered = renders.map((e) => renderRow(e).container)
    const glyphs = rendered.map(iconGeometry)
    const names = rendered.map((container) => accessibleNames(container).slice().sort().join('|'))
    expect(new Set(glyphs).size,
      'US-5 requires nine distinct visual icon states; nine different labels on one glyph do not count').toBe(9)
    expect(new Set(names).size,
      'US-5/MIN-008 requires nine distinct accessible kind/fallback names').toBe(9)
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
      // Testing Library applies the accessible-name algorithm here, so a
      // tooltip on a hidden glyph cannot pass as a screen-reader alternative.
      expect(iconHasAccessibleName(container, new RegExp(`\\b${kind}\\b`, 'i')),
        `MIN-008 requires the ${kind} icon (or its image-role wrapper) to expose its kind to a screen reader`).toBe(true)
    },
  )
})
