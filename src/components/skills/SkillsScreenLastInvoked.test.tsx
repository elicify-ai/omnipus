/**
 * SkillsScreenLastInvoked.test.tsx — ADR-072 D3.1 coverage (spec test row
 * 54, TestSkillsScreen_LastInvokedSurfaced): a granted skill's last-invoked
 * timestamp is the cheapest observable signal that D2 (description-driven
 * activation) actually works, and a granted-but-never-invoked skill must
 * render VISIBLY as unused rather than looking identical to an actively-used
 * one.
 *
 * Follows the sibling-test-file convention already used for this screen
 * (SkillCard.test.tsx / SkillsScreenToolsTab.test.tsx) — SkillsScreen is
 * tested directly since skill rows are rendered inline, not as a standalone
 * SkillCard component.
 *
 * `skillLastInvoked()` reads `last_invoked` off the real, schema-validated
 * `Skill` (contracts/components/schemas/Skill.yaml), populated by
 * `pkg/gateway/rest.go::listSkills` from `pkg/audit.Logger
 * ::LastInvokedForSkill`. These tests attach it to the mocked `fetchSkills`
 * resolution the same way the real backend response does: as a JSON key on
 * each skill.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (opts: { component: React.ComponentType }) => opts,
  useNavigate: () => vi.fn(),
  useParams: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn(),
    fetchMcpServers: vi.fn().mockResolvedValue([]),
    fetchTools: vi.fn().mockResolvedValue([]),
  }
})

import { fetchSkills, type Skill } from '@/lib/api'
import { SkillsScreen } from '@/components/screens/SkillsScreen'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderScreen() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SkillsScreen />
    </QueryClientProvider>,
  )
}

const grantedNeverInvoked: Skill = {
  id: 'release-notes',
  name: 'Release Notes',
  version: '1.0.0',
  description: 'Generate release notes',
  author: 'omnipus-team',
  source: 'global',
  status: 'active',
  verified: true,
}

const invokedRecently: Skill = {
  id: 'daily-briefing',
  name: 'Daily Briefing',
  version: '2.0.0',
  description: 'Summarize the day',
  author: 'omnipus-team',
  source: 'builtin',
  status: 'active',
  verified: true,
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('SkillsScreen — last-invoked surfacing (ADR-072 D3.1, spec test 54)', () => {
  it('a granted skill that has never been invoked shows no last-used time', async () => {
    // last_invoked deliberately absent from the raw payload — exactly the
    // "granted but never called" state.
    vi.mocked(fetchSkills).mockResolvedValueOnce([grantedNeverInvoked])

    renderScreen()

    await screen.findByText('Release Notes')
    const row = screen.getByTestId('skill-last-invoked-release-notes')
    expect(row).toHaveTextContent('Never used')
    expect(row).not.toHaveTextContent('Last used')
  })

  it('an invoked skill renders its real last-used timestamp, distinguishable from the never-invoked skill', async () => {
    // Attach last_invoked the way the raw (pre-contract) response would —
    // fetchSkills merges any `last_invoked` string it finds on the raw item
    // onto the validated Skill (see extractLastInvoked in lib/api.ts).
    const withActivity = { ...invokedRecently, last_invoked: '2026-08-15T10:00:00Z' }
    vi.mocked(fetchSkills).mockResolvedValueOnce([withActivity as unknown as Skill, grantedNeverInvoked])

    renderScreen()

    await screen.findByText('Daily Briefing')

    const invokedRow = screen.getByTestId('skill-last-invoked-daily-briefing')
    expect(invokedRow).toHaveTextContent('Last used')
    expect(invokedRow).not.toHaveTextContent('Never used')

    // The never-invoked skill in the SAME list must still read distinctly —
    // not "look identical to an actively-used one".
    const neverRow = screen.getByTestId('skill-last-invoked-release-notes')
    expect(neverRow).toHaveTextContent('Never used')
  })
})
