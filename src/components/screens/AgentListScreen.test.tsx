import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentListScreen } from './AgentListScreen'
import type { Agent } from '@/lib/api'

// Wave 2 — the Agents roster splits into two sections: "Base agents"
// (type !== 'worker') and "Sub-agent workers" (type === 'worker').

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
  return { ...actual, fetchAgents: vi.fn(), updateAgent: vi.fn(), testAgentRunner: vi.fn() }
})

// CreateAgentModal pulls in heavy deps; stub it to keep the screen test focused.
vi.mock('@/components/agents/CreateAgentModal', () => ({
  CreateAgentModal: () => null,
}))

import { fetchAgents } from '@/lib/api'

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'mia',
    name: 'Mia',
    type: 'core',
    locked: false,
    status: 'active',
    model: 'anthropic/claude-3.5-haiku',
    description: 'Assistant',
    soul: '',
    heartbeat: '',
    instructions: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    steering_mode: 'loop',
    tool_feedback: true,
    heartbeat_enabled: false,
    heartbeat_interval: 300,
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
})

describe('AgentListScreen — base/worker partition', () => {
  it('renders a base agent under "Base agents" and a worker under "Sub-agent workers"', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core' }),
      makeAgent({
        id: 'worker-1',
        name: 'General Worker',
        type: 'worker',
        executor: { kind: 'native' },
      }),
    ])
    renderScreen()

    const baseHeading = await screen.findByRole('heading', { name: /^base agents$/i })
    const workerHeading = await screen.findByRole('heading', { name: /^sub-agent workers$/i })
    expect(baseHeading).toBeInTheDocument()
    expect(workerHeading).toBeInTheDocument()

    // The worker renders under the workers section as a worker-card, the base
    // agent as a normal agent-card.
    await waitFor(() => {
      expect(screen.getByTestId('agent-card-mia')).toBeInTheDocument()
      expect(screen.getByTestId('worker-card-worker-1')).toBeInTheDocument()
    })

    // The worker card omits the default-★ "Set as default" control.
    const workerCard = screen.getByTestId('worker-card-worker-1').closest('div.relative')!
    expect(within(workerCard as HTMLElement).queryByRole('button', { name: /set .* as default/i })).not.toBeInTheDocument()
  })

  it('omits the workers section entirely when there are no workers', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    await screen.findByRole('heading', { name: /^base agents$/i })
    expect(screen.queryByRole('heading', { name: /^sub-agent workers$/i })).not.toBeInTheDocument()
  })

  it('shows the workers section subtitle explaining delegation-only role', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'w', name: 'W', type: 'worker', executor: { kind: 'native' } }),
    ])
    renderScreen()
    expect(await screen.findByText(/invoked by other agents, not chat targets/i)).toBeInTheDocument()
  })
})
