// baseViewMatch.test.ts — ADR-083 EMB-040/EMB-041/EMB-042.
//
// Every fixture below declares at least two views with genuinely different
// labels/names, so a matcher that always returns views[0] (ignoring the
// fragment) is caught by tests asserting a NON-FIRST view was chosen — the
// deletable-subject trap the spec's own review calls out by name for this
// phase (test 44: "a derived machine name that happens to be right").

import { describe, it, expect } from 'vitest'
import { matchBaseView, matchUnloadableBaseView } from './baseViewMatch'
import type { KnowledgeBaseUnloadableView, KnowledgeBaseView } from '@/lib/api/generated/openapi-types'

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

  it('a near-miss with internal whitespace matches no rung at all, not just the label rungs', () => {
    // Renamed from "stops at the first rung that matches ANYTHING" — that
    // claim was never exercised here: "Needs  Daniel" (double space)
    // matches NEITHER the exact NOR the case-insensitive label, AND does
    // not equal any machine name either, so this fixture is not_found at
    // every rung — it says nothing about whether the ladder stops at the
    // first MATCHING rung, only that an all-round miss stays a miss. This
    // is still EMB-041's own worked example (the missing-view marker,
    // listing the labels that exist), so it is kept — just under an
    // accurate name. See the test below for the actual stop-at-first-match
    // property.
    const m = matchBaseView(VIEWS, 'Needs  Daniel')
    expect(m.kind).toBe('not_found')
  })

  it('stops at the first rung that matches ANYTHING, even ambiguously — an ambiguous match at an earlier rung is never rescued by a cleaner match at a later one', () => {
    // Two views share the exact label "Open" — rung 1 (exact label) matches
    // BOTH of them, ambiguously. A third view's MACHINE NAME is itself
    // "Open", which would be a clean, single match at rung 3 (machine name)
    // if the ladder ever reached it. EMB-041/EMB-042 require stopping at
    // the FIRST rung that produces any match at all, even an ambiguous one
    // — "never falling through past a step that matched at all, even
    // ambiguously" (baseViewMatch.ts's own doc comment). A ladder that
    // instead kept searching for the first rung with EXACTLY ONE match
    // would silently resolve to the third view here rather than refusing;
    // this fixture makes that wrong behavior observably different from the
    // right one.
    const rungs: KnowledgeBaseView[] = [
      view({ name: 'crm--open-a', label: 'Open' }),
      view({ name: 'crm--open-b', label: 'Open' }),
      view({ name: 'Open', label: 'Something Else Entirely' }),
    ]
    const m = matchBaseView(rungs, 'Open')
    expect(m.kind).toBe('ambiguous')
    expect(m.kind === 'ambiguous' && m.matches.map((v) => v.name).sort()).toEqual([
      'crm--open-a',
      'crm--open-b',
    ])
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

// UAT U-32 (S3, retest validation): matchUnloadableBaseView is the second
// half of the not_found answer — it names a view that exists but failed to
// load, distinctly from one that was never declared at all.
function unloadable(over: Partial<KnowledgeBaseUnloadableView> = {}): KnowledgeBaseUnloadableView {
  return {
    name: 'projects--active-projects',
    paths: ['.omnipus-vault/views/projects--active-projects.yaml'],
    code: 'view_unknown_property',
    reason: 'view "projects--active-projects" names property "priority" ...',
    ...over,
  }
}

describe('matchUnloadableBaseView (UAT U-32, S3)', () => {
  it('matches a fragment against an unloadable view\'s machine name, exactly', () => {
    const found = matchUnloadableBaseView([unloadable()], 'projects--active-projects')
    expect(found?.name).toBe('projects--active-projects')
  })

  it('matches case-insensitively, the same tolerance the loaded-view label ladder gives', () => {
    const found = matchUnloadableBaseView([unloadable()], 'PROJECTS--ACTIVE-PROJECTS')
    expect(found?.name).toBe('projects--active-projects')
  })

  it('returns undefined for a fragment matching neither name nor anything close', () => {
    expect(matchUnloadableBaseView([unloadable()], 'Something Else')).toBeUndefined()
  })

  it('returns undefined when the fragment is undefined or empty (EMB-043\'s "no view named" case never applies here)', () => {
    expect(matchUnloadableBaseView([unloadable()], undefined)).toBeUndefined()
    expect(matchUnloadableBaseView([unloadable()], '')).toBeUndefined()
  })

  it('returns undefined when there is nothing unloadable at all', () => {
    expect(matchUnloadableBaseView([], 'projects--active-projects')).toBeUndefined()
    expect(matchUnloadableBaseView(undefined, 'projects--active-projects')).toBeUndefined()
  })

  it('skips an unloadable entry with no readable name (a file so broken even its `name:` key could not be parsed)', () => {
    const nameless = unloadable({ name: undefined })
    expect(matchUnloadableBaseView([nameless], 'projects--active-projects')).toBeUndefined()
  })

  it('a fragment written as the view\'s LABEL (not its machine name) is NOT matched — the server does not carry a label for a rejected view', () => {
    // This is the exact UAT U-32 repro shape: the note wrote
    // "![[Projects.base#Active Projects]]" (the DISPLAY LABEL), but
    // KnowledgeBaseUnloadableView only ever carries the machine `name`.
    // Documented, not silently worked around — see this function's own doc
    // comment and the report for the backend/contract change that would
    // close this gap.
    const found = matchUnloadableBaseView([unloadable()], 'Active Projects')
    expect(found).toBeUndefined()
  })
})
