import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentListScreen } from './AgentListScreen'
import { useUiStore } from '@/store/ui'
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
    // Both section headers are now always rendered so the "New…" affordance
    // is reachable on a fresh install. The worker section is still in the
    // DOM (header + empty-state), not omitted.
    expect(screen.queryByRole('heading', { name: /^sub-agent workers$/i })).toBeInTheDocument()
  })

  it('shows the workers section subtitle explaining delegation-only role', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'w', name: 'W', type: 'worker', executor: { kind: 'native' } }),
    ])
    renderScreen()
    expect(await screen.findByText(/invoked by other agents, not chat targets/i)).toBeInTheDocument()
  })
})

// Per-section "New…" buttons (v0.3 worker roster split). Each section
// header carries its own affordance, and each opens the modal pre-set to
// the matching tier (createAgentModalType='custom' | 'worker') so the
// CreateAgentModal can render the right form shape.
describe('AgentListScreen — per-section New buttons', () => {
  beforeEach(() => {
    // Reset the store so the test starts with a clean modal state.
    act(() => { useUiStore.setState({ createAgentModalOpen: false, createAgentModalType: 'custom' }) })
  })

  it('shows a "New agent" button in the base section header', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const newBaseButton = within(baseSection).getByTestId('new-base-agent-button')
    expect(newBaseButton).toBeInTheDocument()
    expect(newBaseButton).toHaveTextContent(/new agent/i)
  })

  it('shows a "New worker" button in the worker section header', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', type: 'core' }),
      makeAgent({ id: 'w1', name: 'W1', type: 'worker', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    const newWorkerButton = within(workerSection).getByTestId('new-worker-button')
    expect(newWorkerButton).toBeInTheDocument()
    expect(newWorkerButton).toHaveTextContent(/new worker/i)
  })

  it('clicking the base New agent button sets modal type to custom and opens the modal', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    fireEvent.click(within(baseSection).getByTestId('new-base-agent-button'))
    const state = useUiStore.getState()
    expect(state.createAgentModalOpen).toBe(true)
    expect(state.createAgentModalType).toBe('custom')
  })

  it('clicking the worker New worker button sets modal type to worker and opens the modal', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', type: 'core' }),
      makeAgent({ id: 'w1', name: 'W1', type: 'worker', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    fireEvent.click(within(workerSection).getByTestId('new-worker-button'))
    const state = useUiStore.getState()
    expect(state.createAgentModalOpen).toBe(true)
    expect(state.createAgentModalType).toBe('worker')
  })

  it('does not render a top-of-page generic New Agent button', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    // Wait for the screen to settle — the (now-removed) header button was a
    // top-level Button labeled "New Agent" with a "size=default" sm 14px icon
    // and no section testid. The per-section buttons are the only "New…"
    // affordances and live INSIDE the base/worker sections, identified by
    // their data-testid. Asserting on the section-scoped testid count
    // (exactly one New agent in the base section when only base agents
    // exist) proves the per-section split.
    await screen.findByTestId('base-agents-section')
    const baseSection = screen.getByTestId('base-agents-section')
    // Exactly one New agent button in the base section.
    const newAgentButtons = within(baseSection).getAllByRole('button', { name: /new agent/i })
    expect(newAgentButtons).toHaveLength(1)
    // And no New worker button when there are no workers.
    expect(within(baseSection).queryByRole('button', { name: /new worker/i })).not.toBeInTheDocument()
  })
})

// Per-section empty-state affordances (regression: a fresh install
// historically hid the worker section entirely, so "New worker" was
// unreachable). The fix renders both section headers (with their "+ New …"
// buttons) on every render, even when the section body is empty.
describe('AgentListScreen — empty-state per-section buttons', () => {
  beforeEach(() => {
    act(() => { useUiStore.setState({ createAgentModalOpen: false, createAgentModalType: 'custom' }) })
  })

  it('shows the worker section + New worker button even with no workers (fresh install)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const workerSection = await screen.findByTestId('worker-agents-section')
    // The New worker button lives in the worker section header, always
    // rendered. This is the regression: previously the entire section was
    // hidden when workerAgents.length === 0.
    const newWorkerButton = within(workerSection).getByTestId('new-worker-button')
    expect(newWorkerButton).toBeInTheDocument()
    expect(newWorkerButton).toHaveTextContent(/new worker/i)
  })

  it('shows the base section + New agent button even with no base agents (fresh install)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'w1', name: 'W1', type: 'worker', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const newBaseButton = within(baseSection).getByTestId('new-base-agent-button')
    expect(newBaseButton).toBeInTheDocument()
    expect(newBaseButton).toHaveTextContent(/new agent/i)
  })

  it('renders an empty-state message in each empty section', async () => {
    // The two-tier "fresh install" realistic case: at least one base agent
    // exists (so the page-level "No agents yet" CTA is bypassed), but
    // workers are empty. The base section renders its cards, the worker
    // section renders its empty-state message + New worker button.
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'mia', type: 'core' })])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const workerSection = await screen.findByTestId('worker-agents-section')
    // No worker cards → empty-state affordance.
    expect(within(workerSection).getByTestId('worker-agents-empty')).toBeInTheDocument()
    // Base section is NOT empty here (mia is present), so no empty-state.
    expect(within(baseSection).queryByTestId('base-agents-empty')).not.toBeInTheDocument()
    // Each section still has its New button, even with the worker section empty.
    expect(within(baseSection).getByTestId('new-base-agent-button')).toBeInTheDocument()
    expect(within(workerSection).getByTestId('new-worker-button')).toBeInTheDocument()
  })

  it('renders the base empty-state when only workers exist (inverse fresh install)', async () => {
    // Inverse of the previous case: at least one worker exists, no base
    // agents. The base section renders its empty-state message + New agent
    // button — that button was previously unreachable in this scenario.
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'w1', name: 'W1', type: 'worker', executor: { kind: 'native' } }),
    ])
    renderScreen()
    const baseSection = await screen.findByTestId('base-agents-section')
    const workerSection = await screen.findByTestId('worker-agents-section')
    expect(within(baseSection).getByTestId('base-agents-empty')).toBeInTheDocument()
    expect(within(workerSection).queryByTestId('worker-agents-empty')).not.toBeInTheDocument()
    expect(within(baseSection).getByTestId('new-base-agent-button')).toBeInTheDocument()
    expect(within(workerSection).getByTestId('new-worker-button')).toBeInTheDocument()
  })
})
