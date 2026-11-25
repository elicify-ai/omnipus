// SkillsToolsPage.lastUsed.test.tsx — ADR-072 D3.1 on the new Skills & Tools page.
//
// ADR-072 D3.1 ("The design's biggest risk must be observable", lines 393-406):
// "A per-skill `last_invoked` timestamp is surfaced in the Skills UI. This is the
// cheapest possible answer to 'is D2 working?': a granted skill with a stale or
// empty `last_invoked` is exactly the failure mode, and it is visible without any
// analysis pipeline."
//
// This is the port of SkillsScreenLastInvoked.test.tsx (spec test row 54,
// TestSkillsScreen_LastInvokedSurfaced), which drove the retired SkillsScreen.
// The redesign spec rewrites that screen (spec line 72) and is silent on this
// ADR requirement; team-lead ruled on 2026-11-25 (Q1 A): "An accepted ADR
// requirement only leaves by an explicit decision. Port 'Last used' / 'Never
// used' to a failing new-page test that cites ADR-072 D3.1 ... The frontend
// builds it."
//
// The oracle is the old test's: a skill that was never invoked reads "Never used"
// and does NOT read "Last used", an invoked skill reads "Last used" followed by
// its time, and the two are told apart in the SAME list. `last_invoked` is a real
// `Skill` wire field (contracts/components/schemas/Skill.yaml, `nullable: true`),
// so both an absent field and an explicit null are the never-invoked case. The old
// test's element hook (`skill-last-invoked-<id>`) belonged to the old screen's
// markup; the new page names its rows `skill-row-<id>`, so this test reads the
// text inside the skill's own row. The time's format is not pinned (the ADR says
// "timestamp"; the old screen's `formatDateTime` is not a requirement), only that
// something follows "Last used".
//
// RED on the dev head: the new page renders no last-used text at all.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import type { Skill } from '@/lib/api'

// The pack's BLOCKED seam: a missing page is a named, loud failure, and the file
// still compiles on a tree that has no page yet.
const pageModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/SkillsToolsPage.tsx'])
async function requireSkillsToolsPage(): Promise<ComponentType> {
  expect(
    Object.keys(pageModules),
    'BLOCKED: SkillsToolsPage is not implemented — required by ADR-072 D3.1 and skills-tools-redesign-spec FR-065',
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
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

useSkillPermissionNetwork(() => ({
  policies: {},
  skill_policies: { 'release-notes': 'ask', 'daily-briefing': 'allow' },
  visibility: {},
  skill_visibility: {},
  skill_trust: 'ask',
}))

// The two skills of the retired test, with the same ids, names and sources.
const NEVER_INVOKED_ID = 'release-notes'
const INVOKED_ID = 'daily-briefing'

function skill(id: string, name: string, source: Skill['source'], overrides: Partial<Skill> = {}): Skill {
  return {
    has_local_changes: false,
    revision: '0'.repeat(64),
    id,
    name,
    version: '1.0.0',
    description: `Skill ${id}.`,
    author: 'omnipus-team',
    source,
    status: 'active',
    verified: true,
    category: 'tools',
    trust_approved: false,
    ...overrides,
  }
}

beforeEach(() => {
  vi.resetAllMocks()
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

// The Skills tab's groups start collapsed; both skills are in category "tools".
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

describe('SkillsToolsPage — last-invoked surfacing (ADR-072 D3.1, spec test row 54)', () => {
  it.each([
    ['absent', undefined],
    ['null', null],
  ] as const)('a skill that has never been invoked (last_invoked %s) reads "Never used" and not "Last used"', async (_label, lastInvoked) => {
    vi.mocked(api.fetchSkills).mockResolvedValue([
      skill(NEVER_INVOKED_ID, 'Release Notes', 'global', lastInvoked === undefined ? {} : { last_invoked: lastInvoked }),
    ])
    renderPage()
    await openToolsGroup()

    const row = await screen.findByTestId(`skill-row-${NEVER_INVOKED_ID}`)
    expect(within(row).getByText('Release Notes'), 'the row is the skill\'s own').toBeInTheDocument()
    expect(row).toHaveTextContent('Never used')
    expect(row).not.toHaveTextContent('Last used')
  })

  it('an invoked skill reads "Last used" with its time, told apart from the never-invoked skill in the same list', async () => {
    vi.mocked(api.fetchSkills).mockResolvedValue([
      skill(INVOKED_ID, 'Daily Briefing', 'builtin', { last_invoked: '2026-08-15T10:00:00Z' }),
      skill(NEVER_INVOKED_ID, 'Release Notes', 'global'),
    ])
    renderPage()
    await openToolsGroup()

    const invokedRow = await screen.findByTestId(`skill-row-${INVOKED_ID}`)
    expect(invokedRow).toHaveTextContent(/Last used\s*\S/)
    expect(invokedRow).not.toHaveTextContent('Never used')

    // The never-invoked skill in the SAME list must still read distinctly — not
    // "look identical to an actively-used one" (the retired test's own words).
    const neverRow = screen.getByTestId(`skill-row-${NEVER_INVOKED_ID}`)
    expect(neverRow).toHaveTextContent('Never used')
    expect(neverRow).not.toHaveTextContent('Last used')
  })
})
