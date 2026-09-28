// RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan test
// 16, exercised at the icon-lookup level `LibraryEntryRow.tsx` actually
// delegates to (fileTypeMeta, src/components/chat/AttachmentCard.tsx).
//
// Oracle: US-5 AS-2 ("Given the Library tree, When it lists a directory
// containing both a .base file and a .view file, Then they render with
// visually distinct icons (a view is never mistaken for the base it may
// have been imported from)").
//
// Today fileTypeMeta has no `.base` branch at all (verified by reading its
// full body — no `is('base', ...)` case) and, since a .view extension is not
// registered anywhere either, both fall through to the identical generic
// fallback:
//
//	return { Icon: File, color: '#64748B', label: e ? e.toUpperCase() : 'File' }
//
// — the SAME Icon component reference for both, distinguished only by the
// text label ("BASE" vs "VIEW"), which is exactly the "never mistaken"
// requirement failing today: an icon-only glance (US-5's own framing, "only
// with a different icon") cannot tell the two apart.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/chat/AttachmentCard.viewKindIcon.test.tsx
import { describe, it, expect } from 'vitest'
import { fileTypeMeta } from './AttachmentCard'

describe('fileTypeMeta — .base vs .view icon distinction (US-5 AS-2)', () => {
  it('renders a .base file and a .view file with visually distinct icons', () => {
    const base = fileTypeMeta('Roadmap.base')
    const view = fileTypeMeta('Weekly Status.view')

    expect(view.Icon).not.toBe(base.Icon)
  })

  it('never falls back to the generic unknown-file icon for a .view file', () => {
    const view = fileTypeMeta('Weekly Status.view')
    const generic = fileTypeMeta('Notes.xyz-not-a-real-extension')

    expect(view.Icon).not.toBe(generic.Icon)
  })
})
