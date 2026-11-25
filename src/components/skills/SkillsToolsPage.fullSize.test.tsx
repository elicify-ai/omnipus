// SkillsToolsPage.fullSize.test.tsx — test row 66 of skills-tools-redesign-spec: what a Full skill costs,
// shown on the Skills tab (C-14 "Page warning", FR-079, S-130, dataset D18 rows 1-5 and 11-14; the section
// was added by the spec amendment b27c64109).
//
// THE ORACLE is the spec's own text and D18's literals, never a value read off the page:
//
//   Row text   `Skill.full_tokens` rounded to the nearest 100 and shown as "about <n> tokens" (3,240 ->
//              "about 3,200 tokens"); below 50 -> "under 100 tokens"; absent -> no size. Only rows at Full
//              show it.                                                              [D18 rows 1-5]
//   Warning    The Skills tab reads GET /agents and names every agent with `full_skills_tokens` x 2 >
//              `context_window_effective`. Exactly half is not a warning. An agent whose full_skills_tokens or
//              context_window_effective is absent, or whose window is 0, is never named.
//                                                                                    [D18 rows 6, 7, 11, 12]
//   Callout    (warning) "The skills set to Full take more than half of the model's context for <agent names,
//              comma-separated>. Set some of them to Name and short description, or choose a model with a
//              larger context."
//   Freshness  Re-read GET /agents once after each saved skill level or skill permission change, and on window
//              focus. If GET /agents fails: no warning, no extra error callout, the rest of the tab unaffected.
//                                                                                    [D18 rows 13, 14]
//
// NOT PINNED, because the spec is silent: where the callout sits on the tab, the order of several names, the
// wording for one name versus several, whether the other tabs repeat it, and WHICH DOM event "window focus" is
// (the window-focus test fires both a `focus` event on the window and a `visibilitychange` on the document,
// and requires the answer to be read again, however the page listens). Row sizes are wire literals: the
// generated Skill/Agent types do not carry `full_tokens` / `full_skills_tokens` yet (the backend adds them with
// the contract), so the fixtures are plain wire objects; the casts stay valid once they are regenerated.
//
// The page being tested is read through the pack's BLOCKED seam: a missing page is a named, loud failure.
//
// MUTATION PROBES (deferred to CHECK, not run here): `>=` instead of `>` in the half rule (the exactly-half case
// dies); naming an agent whose window is absent or 0 (D18#11/#12 die); rounding down instead of to the nearest
// 100 (the 3,250 and 50 cases die); showing the size at every level (the lower-level case dies); no re-read
// after a saved permission change (the permission re-read dies); reading on focus only (the level re-read dies).
//
// RED, shown by a run: saved run fix4f8-01 (the frontend head f69d7a7b7 plus this file; 23 failed (23);
// ta/fix4-ts-fix4f8-01.log). The dev page has no size text, no GET /agents read and no callout:
//   - the ten row cases fail on the row text ("about 3,200 tokens" and the other rounding literals, and the
//     "no size" cases that sit beside a sized Full row in one list);
//   - the two warning cases fail with "the Full-size warning is shown: expected null not to be null";
//   - the six "no warning" cases (exactly half, window absent, window 0, tokens absent, tokens 0, well under
//     half) fail ONLY on the deliberate soft expectation "the Skills tab reads GET /agents"; their own
//     assertions (no callout) pass trivially on a page that never reads the figures, so what keeps them honest
//     is the CHECK mutants named above, not this run;
//   - D18#13 fails on the row text and on the same soft read expectation; the four re-read cases all fail on
//     the missing callout, and three of them (saved level, saved permission, window focus) also on the read
//     expectation.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import type { Skill } from '@/lib/api'

const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec C-14 "Page warning" and FR-079',
  ).toContain('/src/components/skills/SkillsToolsPage.tsx')
  const mod = (await pageModules['/src/components/skills/SkillsToolsPage.tsx']()) as Record<string, unknown>
  expect(typeof mod.SkillsToolsPage, 'BLOCKED: SkillsToolsPage public export').toBe('function')
  return mod.SkillsToolsPage as ComponentType
}

let SkillsToolsPage: ComponentType
beforeEach(async () => {
  SkillsToolsPage = await requireSkillsToolsPage()
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn(),
    fetchAgents: vi.fn(),
    fetchRegistryTools: vi.fn(),
    fetchBuiltinTools: vi.fn(),
    fetchProviders: vi.fn(),
    fetchSkillMarketplaceStatus: vi.fn(),
    searchSkills: vi.fn(),
    fetchMcpServers: vi.fn(),
  }
})
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

import * as api from '@/lib/api'
import { expectPermissionPatches, useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

const permissions = useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: {},
  visibility: {},
  skill_visibility: {},
  skill_trust: 'ask',
}))

type Level = 'hidden' | 'name_only' | 'name_and_description' | 'full'

/** The stored "What the model sees" level per skill id, as GET /security/permissions serves it (C-6). */
function storeLevels(levels: Record<string, Level>, policies: Record<string, 'allow' | 'ask' | 'deny'> = {}) {
  permissions().stored.skill_visibility = levels
  permissions().stored.skill_policies = policies
}

// A skill as GET /skills serves it. `full_tokens` is the new wire member of C-14 (absent = not served).
function skill(id: string, name: string, extra: Partial<Skill> & { full_tokens?: number } = {}): Skill {
  return {
    has_local_changes: false,
    revision: '0'.repeat(64),
    id,
    name,
    version: '1.0.0',
    description: `Skill ${id}.`,
    author: 'omnipus-team',
    source: 'global',
    status: 'active',
    verified: true,
    category: 'tools',
    trust_approved: false,
    ...extra,
  } as Skill
}

// An agent as GET /agents serves it: the two members the rule reads are left out when absent, as on the wire.
function agent(id: string, name: string, size: { tokens?: number; window?: number }): unknown {
  return {
    id,
    name,
    type: 'custom',
    status: 'active',
    ...(size.tokens === undefined ? {} : { full_skills_tokens: size.tokens }),
    ...(size.window === undefined ? {} : { context_window_effective: size.window }),
  }
}

function serveAgents(answer: () => unknown[]) {
  vi.mocked(api.fetchAgents).mockImplementation(async () => answer() as Awaited<ReturnType<typeof api.fetchAgents>>)
}
const agentReads = () => vi.mocked(api.fetchAgents).mock.calls.length

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.fetchAgents).mockResolvedValue([])
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(api.fetchProviders).mockResolvedValue([])
  vi.mocked(api.fetchSkillMarketplaceStatus).mockResolvedValue({} as never)
  vi.mocked(api.searchSkills).mockResolvedValue([])
  vi.mocked(api.fetchMcpServers).mockResolvedValue([])
})

afterEach(() => {
  cleanup()
})

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  return render(
    <QueryClientProvider client={client}>
      <SkillsToolsPage />
    </QueryClientProvider>,
  )
}

// The Skills tab's groups start collapsed; every fixture skill is in category "tools".
async function openToolsGroup() {
  const trigger = await waitFor(
    () => {
      const found = screen.getAllByRole('button', { name: /^Tools/ }).find((button) => button.hasAttribute('aria-expanded'))
      expect(found, 'the Tools group disclosure is present').toBeDefined()
      return found as HTMLElement
    },
    { timeout: 3000 },
  )
  if (trigger.getAttribute('aria-expanded') === 'false') await userEvent.setup().click(trigger)
}

const rowOf = (id: string) => screen.findByTestId(`skill-row-${id}`)
const SIZE_TEXT = /(about [\d,]+|under 100) tokens/
const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 80)) })

// The page must have read GET /agents before a "no callout" assertion means anything. The read is a soft
// expectation, so the assertions after it still run and a red run shows which of them pass.
async function afterAgentsRead() {
  const read = await waitFor(() => { expect(agentReads()).toBeGreaterThan(0) }, { timeout: 2000 }).then(() => true, () => false)
  expect.soft(read, 'the Skills tab reads GET /agents (spec C-14 "Page warning")').toBe(true)
  await settle()
}

// The warning's sentence, found by its own words. The callout it sits in is returned when there is one.
const WARNING_WORDS = /take more than half of the model['’]s context/
function warningElement(): HTMLElement | null {
  const found = screen.queryAllByText(WARNING_WORDS)
  if (found.length === 0) return null
  // The spec asks for ONE callout. A second copy of the words outside any callout (a screen-reader
  // announcement, say) is not a second warning, so only callouts are counted.
  const callouts = new Set<HTMLElement>()
  for (const element of found) {
    const callout = element.closest('[data-slot="callout"]') as HTMLElement | null
    if (callout) callouts.add(callout)
  }
  expect(callouts.size, 'the warning is a single callout').toBeLessThanOrEqual(1)
  return [...callouts][0] ?? found[0]
}
function findWarning(): Promise<HTMLElement> {
  return waitFor(
    () => {
      const element = warningElement()
      expect(element, 'the Full-size warning is shown (spec C-14 "Page warning")').not.toBeNull()
      return element as HTMLElement
    },
    { timeout: 3000 },
  )
}
// `names` is a regular expression: one name, or an alternation when the order is the spec's silence.
function warningFor(names: string): RegExp {
  return new RegExp(
    `The skills set to Full take more than half of the model['’]s context for ${names}\\. ` +
      'Set some of them to Name and short description, or choose a model with a larger context\\.',
  )
}

// The level control's accessible name. The spec's A11y list (line 964) says "What the model sees for <name>:
// <level>"; this matcher keeps the fixed start and the level at the end and leaves the middle free, so the
// size and warning rules are not tied to the name's exact middle (the name itself is a separate requirement).
function levelButton(row: HTMLElement, word: string): HTMLElement {
  return within(row).getByRole('button', { name: new RegExp(`^What the model sees\\b.*: ${word}$`) })
}

async function chooseLevel(row: HTMLElement, current: string, next: RegExp) {
  const user = userEvent.setup()
  await user.click(levelButton(row, current))
  await user.click(await screen.findByRole('menuitem', { name: next }))
}

// [why, full_tokens, the row text]. 3,250 is D18#5's "half rounds up"; the others follow the same rule.
const SIZE_CASES: ReadonlyArray<readonly [string, number, string]> = [
  ['D18#1', 3240, 'about 3,200 tokens'],
  ['D18#4', 40, 'under 100 tokens'],
  ['D18#5 (half rounds up)', 3250, 'about 3,300 tokens'],
  ['one below half rounds down', 3249, 'about 3,200 tokens'],
  ['49 is below 50', 49, 'under 100 tokens'],
  ['50 is not below 50, and half rounds up', 50, 'about 100 tokens'],
  ['five digits take a thousands separator', 12480, 'about 12,500 tokens'],
]

describe('SkillsToolsPage — a Full skill shows its size (C-14 "Page warning", D18 rows 1-5)', () => {
  it.each(SIZE_CASES)('%s: full_tokens %i reads "%s"', async (_why, tokens, text) => {
    vi.mocked(api.fetchSkills).mockResolvedValue([skill('plan', 'Plan', { full_tokens: tokens })])
    storeLevels({ plan: 'full' })
    renderPage()
    await openToolsGroup()

    const row = await rowOf('plan')
    expect(levelButton(row, 'Full'), 'instrument: the row is at Full').toBeInTheDocument()
    expect(row).toHaveTextContent(text)
  })

  it('D18#2: the same 3,240-token skill at each lower level shows no size, told apart from a Full row in one list', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([
      skill('plan', 'Plan', { full_tokens: 3240 }),
      skill('at-description', 'At description', { full_tokens: 3240 }),
      skill('at-name', 'At name', { full_tokens: 3240 }),
      skill('at-hidden', 'At hidden', { full_tokens: 3240 }),
    ])
    storeLevels({ plan: 'full', 'at-description': 'name_and_description', 'at-name': 'name_only', 'at-hidden': 'hidden' })
    renderPage()
    await openToolsGroup()

    expect(await rowOf('plan')).toHaveTextContent('about 3,200 tokens')
    for (const id of ['at-description', 'at-name', 'at-hidden']) {
      expect(await rowOf(id), `${id}: only a row at Full shows a size`).not.toHaveTextContent(SIZE_TEXT)
    }
  })

  it.each([
    ['D18#3: a skill whose file cannot be read (load_error, no full_tokens)', { status: 'error', load_error: 'SKILL.md could not be read' }],
    ['a Full skill the server sent no full_tokens for', {}],
  ] as const)('%s shows no size beside a sized Full skill', async (_why, extra) => {
    vi.mocked(api.fetchSkills).mockResolvedValue([
      skill('plan', 'Plan', { full_tokens: 3240 }),
      skill('notes-helper', 'Notes Helper', extra as Partial<Skill>),
    ])
    storeLevels({ plan: 'full', 'notes-helper': 'full' })
    renderPage()
    await openToolsGroup()

    expect(await rowOf('plan')).toHaveTextContent('about 3,200 tokens')
    expect(await rowOf('notes-helper')).not.toHaveTextContent(SIZE_TEXT)
  })
})

// Each negative case: one agent that the rule must NOT name. [why, the agent's two members].
const NEVER_NAMED: ReadonlyArray<readonly [string, { tokens?: number; window?: number }]> = [
  ['D18#7: exactly half (4,000 of an 8,000 window)', { tokens: 4000, window: 8000 }],
  ['D18#11: context_window_effective absent', { tokens: 5000 }],
  ['D18#12: a window of 0', { tokens: 5000, window: 0 }],
  ['full_skills_tokens absent', { window: 8000 }],
  ['full_skills_tokens 0', { tokens: 0, window: 8000 }],
  ['well under half (3,000 of 8,000)', { tokens: 3000, window: 8000 }],
]

describe('SkillsToolsPage — the warning names the agents over half their window (C-14, D18 rows 6, 7, 11, 12)', () => {
  it('D18#6: 4,001 of an 8,000 window names Mia in a warning callout carrying the spec sentence', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([skill('plan', 'Plan', { full_tokens: 4001 })])
    storeLevels({ plan: 'full' })
    serveAgents(() => [agent('mia', 'Mia', { tokens: 4001, window: 8000 })])
    renderPage()

    const callout = await findWarning()
    expect(callout).toHaveTextContent(warningFor('Mia'))
    expect(callout, 'spec: "Callout (warning)"').toHaveAttribute('data-variant', 'warning')
  })

  it('names every agent over half and only those: Mia and Ray, not Jim', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([skill('plan', 'Plan', { full_tokens: 5000 })])
    storeLevels({ plan: 'full' })
    serveAgents(() => [
      agent('mia', 'Mia', { tokens: 5000, window: 8000 }),
      agent('jim', 'Jim', { tokens: 3000, window: 8000 }),
      agent('ray', 'Ray', { tokens: 9000, window: 16000 }),
    ])
    renderPage()

    const callout = await findWarning()
    expect(callout).toHaveTextContent(warningFor('(Mia, Ray|Ray, Mia)'))
    expect(callout, 'Jim fits (3,000 of 8,000) and is not named').not.toHaveTextContent('Jim')
  })

  it.each(NEVER_NAMED)('%s: no warning', async (_why, size) => {
    vi.mocked(api.fetchSkills).mockResolvedValue([skill('plan', 'Plan', { full_tokens: 4000 })])
    storeLevels({ plan: 'full' })
    serveAgents(() => [agent('mia', 'Mia', size)])
    renderPage()
    await openToolsGroup()
    await afterAgentsRead()

    expect(warningElement(), 'Mia is never named for this size and window').toBeNull()
  })

  it('D18#13: when GET /agents fails the rows and sizes are normal and the page shows no warning and no error', async () => {
    vi.mocked(api.fetchAgents).mockRejectedValue(new Error('agents read failed: boom'))
    vi.mocked(api.fetchSkills).mockResolvedValue([skill('plan', 'Plan', { full_tokens: 3240 })])
    storeLevels({ plan: 'full' }, { plan: 'ask' })
    renderPage()
    await openToolsGroup()

    const row = await rowOf('plan')
    expect.soft(row, 'the row and its size are normal').toHaveTextContent('about 3,200 tokens')
    await afterAgentsRead()
    expect(warningElement(), 'no warning when the read failed').toBeNull()
    expect(document.body, 'the failure is not shown anywhere').not.toHaveTextContent('boom')
    expect(document.querySelectorAll('[data-slot="callout"][data-variant="error"]'), 'no extra error callout').toHaveLength(0)
    for (const choice of ['Allow', 'Ask', 'Deny']) {
      expect(within(row).getByRole('button', { name: choice }), `${choice}: the rest of the tab still works`).toBeEnabled()
    }
  })
})

describe('SkillsToolsPage — the figures are read again after a saved change and on window focus (C-14 Freshness, D18#14)', () => {
  const TWO_SKILLS = () => [
    skill('plan', 'Plan', { full_tokens: 3000 }),
    skill('deep-research', 'Deep Research', { full_tokens: 2000 }),
  ]

  it('D18#14: a saved level change reads GET /agents once more, and the callout then names Mia', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue(TWO_SKILLS())
    storeLevels({ plan: 'full', 'deep-research': 'name_and_description' })
    // The server's answer follows its own state: 3,000 of 8,000 until the change has reached it, then 5,000.
    serveAgents(() => [agent('mia', 'Mia', { tokens: permissions().patches().length === 0 ? 3000 : 5000, window: 8000 })])
    renderPage()
    await openToolsGroup()
    await afterAgentsRead()
    expect(warningElement(), 'instrument: 3,000 of 8,000 fits').toBeNull()
    const baseline = agentReads()

    await chooseLevel(await rowOf('deep-research'), 'Name and short description', /^Full/)
    await waitFor(() => expectPermissionPatches(permissions(), { skill_visibility: { 'deep-research': 'full' } }))

    expect(await findWarning()).toHaveTextContent(warningFor('Mia'))
    await settle()
    expect(agentReads(), 'once after the saved change: not none, not twice').toBe(baseline + 1)
  })

  it('a saved permission change reads GET /agents once more, and the callout then names Mia', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue(TWO_SKILLS())
    storeLevels({ plan: 'full', 'deep-research': 'full' }, { plan: 'ask' })
    serveAgents(() => [agent('mia', 'Mia', { tokens: permissions().patches().length === 0 ? 0 : 5000, window: 8000 })])
    renderPage()
    await openToolsGroup()
    await afterAgentsRead()
    expect(warningElement(), 'instrument: an Ask skill is not injected, so 0 of 8,000').toBeNull()
    const baseline = agentReads()

    await userEvent.setup().click(within(await rowOf('plan')).getByRole('button', { name: /^allow$/i }))
    await waitFor(() => expectPermissionPatches(permissions(), { skill_policies: { plan: 'allow' } }))

    expect(await findWarning()).toHaveTextContent(warningFor('Mia'))
    await settle()
    expect(agentReads(), 'once after the saved change: not none, not twice').toBe(baseline + 1)
  })

  it('the callout goes away after the re-read says the agent fits again', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue(TWO_SKILLS())
    storeLevels({ plan: 'full', 'deep-research': 'full' })
    serveAgents(() => [agent('mia', 'Mia', { tokens: permissions().patches().length === 0 ? 5000 : 3000, window: 8000 })])
    renderPage()
    await openToolsGroup()
    expect(await findWarning(), 'instrument: 5,000 of 8,000 is over half').toHaveTextContent(warningFor('Mia'))

    await chooseLevel(await rowOf('deep-research'), 'Full', /^Name and short description/)
    await waitFor(() => expectPermissionPatches(permissions(), { skill_visibility: { 'deep-research': 'name_and_description' } }))

    await waitFor(() => { expect(warningElement(), 'the warning clears once Mia fits').toBeNull() }, { timeout: 3000 })
  })

  it('window focus reads GET /agents again', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue(TWO_SKILLS())
    storeLevels({ plan: 'full', 'deep-research': 'full' })
    let answer = 3000
    serveAgents(() => [agent('mia', 'Mia', { tokens: answer, window: 8000 })])
    renderPage()
    await openToolsGroup()
    await afterAgentsRead()
    expect(warningElement(), 'instrument: 3,000 of 8,000 fits').toBeNull()

    answer = 5000 // changed elsewhere while this window was in the background
    act(() => {
      window.dispatchEvent(new Event('focus'))
      document.dispatchEvent(new Event('visibilitychange', { bubbles: true }))
    })

    expect(await findWarning()).toHaveTextContent(warningFor('Mia'))
  })
})
