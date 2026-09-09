// baseViewMatch.test.ts — ADR-083 EMB-040/EMB-041/EMB-042.
//
// Every fixture below declares at least two views with genuinely different
// labels/names, so a matcher that always returns views[0] (ignoring the
// fragment) is caught by tests asserting a NON-FIRST view was chosen — the
// deletable-subject trap the spec's own review calls out by name for this
// phase (test 44: "a derived machine name that happens to be right").

import { describe, it, expect } from 'vitest'
import { matchBaseView } from './baseViewMatch'
import type { KnowledgeBaseView } from '@/lib/api/generated/openapi-types'

function view(over: Partial<KnowledgeBaseView> = {}): KnowledgeBaseView {
  return { name: 'tasks--needs-daniel', label: 'Needs Daniel', ...over }
}

const VIEWS: KnowledgeBaseView[] = [
  view({ name: 'tasks--needs-daniel', label: 'Needs Daniel' }),
  view({ name: 'tasks--awaiting-founder', label: 'Awaiting founder' }),
]

describe('matchBaseView — the ladder (EMB-041)', () => {
  it('matches an exact label, and picks the NON-FIRST view when that is the one named', () => {
    const m = matchBaseView(VIEWS, 'Awaiting founder')
    expect(m.kind).toBe('matched')
    expect(m.kind === 'matched' && m.view.name).toBe('tasks--awaiting-founder')
    expect(m.kind === 'matched' && m.chosenByDefault).toBe(false)
  })

  it('falls back to a case-insensitive label match when no exact label matched', () => {
    const m = matchBaseView(VIEWS, 'needs daniel')
    expect(m.kind).toBe('matched')
    expect(m.kind === 'matched' && m.view.name).toBe('tasks--needs-daniel')
  })

  it('falls back to the machine name itself, verbatim, as the last rung', () => {
    const m = matchBaseView(VIEWS, 'tasks--awaiting-founder')
    expect(m.kind).toBe('matched')
    expect(m.kind === 'matched' && m.view.label).toBe('Awaiting founder')
  })

  it('stops at the first rung that matches ANYTHING — a near-miss with internal whitespace does not fall through to a machine-name match', () => {
    // "Needs  Daniel" (double space) matches neither the exact nor the
    // case-insensitive label, and does not equal any machine name either —
    // this is EMB-041's own worked example: the missing-view marker,
    // listing the labels that exist.
    const m = matchBaseView(VIEWS, 'Needs  Daniel')
    expect(m.kind).toBe('not_found')
  })
})

describe('matchBaseView — no fragment at all (EMB-043)', () => {
  it('chooses the first declared view and flags it as chosen-by-default', () => {
    const m = matchBaseView(VIEWS, undefined)
    expect(m.kind).toBe('matched')
    expect(m.kind === 'matched' && m.view.name).toBe('tasks--needs-daniel')
    expect(m.kind === 'matched' && m.chosenByDefault).toBe(true)
  })

  it('reports not_found for a file that declares no views at all', () => {
    expect(matchBaseView([], undefined).kind).toBe('not_found')
  })
})

describe('matchBaseView — duplicate labels are refused, not guessed (EMB-042)', () => {
  it('refuses two views sharing one display label and names both', () => {
    const dup: KnowledgeBaseView[] = [
      view({ name: 'crm--open-a', label: 'Open' }),
      view({ name: 'crm--open-b', label: 'Open' }),
    ]
    const m = matchBaseView(dup, 'Open')
    expect(m.kind).toBe('ambiguous')
    expect(m.kind === 'ambiguous' && m.matches.map((v) => v.name).sort()).toEqual([
      'crm--open-a',
      'crm--open-b',
    ])
  })

  it('refuses on a case-insensitive collision too, not just an exact one', () => {
    const dup: KnowledgeBaseView[] = [
      view({ name: 'crm--open-a', label: 'Open' }),
      view({ name: 'crm--open-b', label: 'OPEN' }),
    ]
    const m = matchBaseView(dup, 'open')
    expect(m.kind).toBe('ambiguous')
  })
})

describe('matchBaseView — never constructs a machine name it was not given', () => {
  it('a fragment that would kebab-case into a plausible-looking slug is NOT matched against one that does not exist', () => {
    // "Needs Daniel" would slug to "needs-daniel" under a naive kebab
    // transform, but the server's real slug is "tasks--needs-daniel" (base
    // stem + "--" + kebab). A reader that reconstructs slugs would find a
    // false match here; matchBaseView must not, because it never derives one.
    const m = matchBaseView(VIEWS, 'needs-daniel')
    expect(m.kind).toBe('not_found')
  })
})
