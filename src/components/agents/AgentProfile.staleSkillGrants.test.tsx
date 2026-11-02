// UAT-1 F-9 (founder Q-S3 option A, skills spec r15 §5.10 SKILL_GRANT_STALE):
// the agent's Skills tab marks a granted skill whose grant is stale.
//
// ASSUMED SHAPE (the spec names no field; recorded in the red ledger): the
// agent carries an optional read-only `stale_skill_grants: string[]` (names
// out of `skills`); the badge for each is an element with test id
// `skill-stale-badge-<skill id>` whose text contains "stale". A skill that is
// granted and not stale shows no badge. Real AgentProfile; only API edges are
// replaced.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, Skill } from '@/lib/api'
import { mockCoreAgent } from './agentProfileTestFixtures'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}
if (typeof Element !== 'undefined' && !Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgent: vi.fn(),
    fetchWorkspace: vi.fn(),
    updateAgent: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
  }
})

import { fetchAgent, fetchSkills, fetchProviders } from '@/lib/api'

const skill = (id: string): Skill => ({
  id, name: id, source: 'global', description: `${id} skill`, version: '1.0.0',
  status: 'active', verified: false,
} as Skill)

beforeEach(() => {
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([skill('kept'), skill('recreated')])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
})

function openSkillsTab(agentId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><AgentProfile agentId={agentId} /></QueryClientProvider>)
}

describe('AgentProfile Skills tab: stale grants', () => {
  it('marks only the skill whose grant is stale', async () => {
    const agent = {
      ...mockCoreAgent, skills: ['kept', 'recreated'], stale_skill_grants: ['recreated'],
    } as Agent
    vi.mocked(fetchAgent).mockResolvedValue(agent)
    openSkillsTab('general-assistant')
    await screen.findByText('General Assistant')
    const trigger = screen.getByTestId('tab-skills')
    trigger.focus()
    fireEvent.keyDown(trigger, { key: 'Enter' })
    fireEvent.click(trigger)
    const panel = await screen.findByRole('tabpanel')
    // Positive control: both skills are rendered as granted.
    expect(await within(panel).findByTestId('skill-checkbox-kept')).toBeInTheDocument()
    expect(within(panel).getByTestId('skill-checkbox-recreated')).toBeInTheDocument()

    const badge = within(panel).getByTestId('skill-stale-badge-recreated')
    expect(badge).toHaveTextContent(/stale/i)
    expect(within(panel).queryByTestId('skill-stale-badge-kept')).toBeNull()
  })
})
