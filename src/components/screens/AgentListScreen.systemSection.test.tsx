// AgentListScreen.systemSection.test.tsx — ADR-049 D3/FR-095/SD-C16/US-13 AS-1.
//
// Companion to AgentListScreen.test.tsx's base/worker partition suite —
// asserts the fourth, locked "System" section: a `type: 'system'` agent (the
// seeded Judge) renders there (not under "Main agents"), and its card omits
// the ★ "Set as default" control (mirrors the worker-card assertion in the
// existing suite).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentListScreen } from './AgentListScreen'
import type { Agent, CliDetect } from '@/lib/api'

const fetchSpy = vi.fn()
vi.stubGlobal('fetch', fetchSpy)

function makeCliDetect(): CliDetect {
  const entry = () => ({ installed: true, path: '/usr/local/bin/mock-cli', source: 'path' as const })
  return { claude: entry(), codex: entry(), opencode: entry() }
}

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    Link: ({ children, ...rest }: { children: React.ReactNode } & Record<string, unknown>) => (
      <a {...rest}>{children}</a>
    ),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn(), fetchWorkspaces: vi.fn(), updateAgent: vi.fn(), testAgentRunner: vi.fn(), fetchCliDetect: vi.fn() }
})

vi.mock('@/components/agents/CreateAgentModal', () => ({
  CreateAgentModal: () => null,
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: (selector: (s: { token: string | null; role: string | null; username: string | null }) => unknown) =>
    selector({ token: 'test-token', role: 'admin', username: 'admin' }),
}))

import { fetchAgents, fetchWorkspaces, fetchCliDetect } from '@/lib/api'

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'mia',
    name: 'Mia',
    type: 'core',
    locked: false,
    needs_model: false,
    status: 'active',
    model: 'anthropic/claude-3.5-haiku',
    description: 'Assistant',
    soul: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
    ...overrides,
  }
}

function renderScreen() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AgentListScreen />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgents).mockReset()
  vi.mocked(fetchWorkspaces).mockResolvedValue([])
  fetchSpy.mockReset()
  vi.mocked(fetchCliDetect).mockReset()
  vi.mocked(fetchCliDetect).mockResolvedValue(makeCliDetect())
})

describe('AgentListScreen — System section (ADR-049 D3/SD-C16)', () => {
  it('renders a type:system agent in the System section, not under Main agents', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core' }),
      makeAgent({ id: 'judge', name: 'Judge', type: 'system', locked: true, description: 'Adjudicates task/plan attempts' }),
    ])
    renderScreen()

    await screen.findByRole('heading', { name: /^main agents$/i })
    const systemSection = await screen.findByTestId('system-agents-section')
    expect(systemSection).toBeInTheDocument()

    // Expand the (collapsed-by-default) System accordion to reach the card.
    const trigger = within(systemSection).getByTestId('system-agents-trigger')
    trigger.click()

    await waitFor(() => {
      expect(within(systemSection).getByTestId('agent-card-judge')).toBeInTheDocument()
    })

    // The Judge must NOT also render under Main agents.
    const mainSection = screen.getByTestId('base-agents-section')
    expect(within(mainSection).queryByTestId('agent-card-judge')).not.toBeInTheDocument()
  })

  it('omits the ★ "Set as default" control on the System agent card', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core' }),
      makeAgent({ id: 'judge', name: 'Judge', type: 'system', locked: true }),
    ])
    renderScreen()

    const systemSection = await screen.findByTestId('system-agents-section')
    within(systemSection).getByTestId('system-agents-trigger').click()

    await waitFor(() => {
      expect(within(systemSection).getByTestId('agent-card-judge')).toBeInTheDocument()
    })
    const judgeCard = within(systemSection).getByTestId('agent-card-judge').closest('div.relative')!
    expect(within(judgeCard as HTMLElement).queryByRole('button', { name: /set .* as default/i })).not.toBeInTheDocument()
  })

  it('omits the System section entirely when there is no system agent', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    await screen.findByRole('heading', { name: /^main agents$/i })
    expect(screen.queryByTestId('system-agents-section')).not.toBeInTheDocument()
  })
})
