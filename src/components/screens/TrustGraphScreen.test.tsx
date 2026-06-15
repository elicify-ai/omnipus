import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Agent } from '@/lib/api'

// ── TrustGraphScreen SAVE-WIRING test (Spec-3 FR-6.2 / US-3 / NFR-7) ─────────
//
// The React Flow canvas (DelegationGraph) is mocked to a lightweight stub that
// surfaces the edit handlers as buttons — so we exercise the screen's real
// per-agent save flow (drawing an edge → PUT payload for that source → toast →
// query invalidation) WITHOUT canvas/ResizeObserver in jsdom.
//
// Asserts: drawing A→B issues updateAgent(A, {delegation_policy:{to:[…B…]}}) and
// NEVER sends accept_from / budget; deleting removes the ref; a clean graph
// sends no PUT.

const updateAgentMock = vi.fn()
const addToastMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    updateAgent: (...args: unknown[]) => updateAgentMock(...args),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: (selector: (s: { addToast: typeof addToastMock }) => unknown) =>
    selector({ addToast: addToastMock }),
}))

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}))

// Stub the React Flow canvas: render buttons that invoke the edit handlers so
// the screen's save flow is driven without a real canvas.
vi.mock('@/components/agents/delegation/DelegationGraph', () => ({
  DelegationGraph: (props: {
    onConnect: (s: string, t: string) => void
    onDeleteEdge: (s: string, t: string) => void
    onToggleMode: (s: string, m: string) => void
    onSetDepth: (s: string, d: number | undefined) => void
  }) => (
    <div data-testid="delegation-graph-stub">
      <button onClick={() => props.onConnect('jim', 'ray')}>connect-jim-ray</button>
      <button onClick={() => props.onDeleteEdge('jim', 'existing')}>delete-jim-existing</button>
      <button onClick={() => props.onToggleMode('jim', 'background')}>toggle-jim-bg</button>
      <button onClick={() => props.onSetDepth('jim', 5)}>depth-jim-5</button>
    </div>
  ),
}))

import { TrustGraphScreen } from './TrustGraphScreen'
import { fetchAgents } from '@/lib/api'

const fetchAgentsMock = fetchAgents as unknown as ReturnType<typeof vi.fn>

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'agent',
    name: 'Agent',
    type: 'core',
    locked: false,
    status: 'active',
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

function roster(): Agent[] {
  return [
    makeAgent({
      id: 'jim',
      name: 'Jim',
      default: true,
      delegation_policy: {
        to: [{ kind: 'local', id: 'existing' }],
        accept_from: [{ kind: 'local', id: 'leak' }],
        modes: ['await'],
        depth: 2,
        budget: { max_cost_usd: 99, max_tokens: 88 },
      },
    }),
    makeAgent({ id: 'ray', name: 'Ray' }),
    makeAgent({ id: 'existing', name: 'Existing' }),
  ]
}

function renderScreen() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <TrustGraphScreen />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  updateAgentMock.mockReset()
  updateAgentMock.mockResolvedValue(makeAgent({ id: 'jim', name: 'Jim' }))
  addToastMock.mockReset()
  fetchAgentsMock.mockReset()
  fetchAgentsMock.mockResolvedValue(roster())
})

describe('TrustGraphScreen — save wiring', () => {
  it('drawing jim→ray then Save issues a PUT for jim with the new `to`', async () => {
    renderScreen()
    await screen.findByTestId('delegation-graph-stub')

    fireEvent.click(screen.getByText('connect-jim-ray'))
    // Save button reflects the single dirty source.
    const save = await screen.findByTestId('delegation-save')
    expect(save).toHaveTextContent('Save (1)')
    fireEvent.click(save)

    await waitFor(() => expect(updateAgentMock).toHaveBeenCalledTimes(1))
    const [id, body] = updateAgentMock.mock.calls[0]
    expect(id).toBe('jim')
    expect(body.delegation_policy.to).toEqual([
      { kind: 'local', id: 'existing' },
      { kind: 'local', id: 'ray' },
    ])
    expect(body.delegation_policy.modes).toEqual(['await'])
    expect(body.delegation_policy.depth).toBe(2)
  })

  it('NEVER sends accept_from or budget in the save payload (NFR-7)', async () => {
    renderScreen()
    await screen.findByTestId('delegation-graph-stub')
    fireEvent.click(screen.getByText('connect-jim-ray'))
    fireEvent.click(await screen.findByTestId('delegation-save'))

    await waitFor(() => expect(updateAgentMock).toHaveBeenCalledTimes(1))
    const body = updateAgentMock.mock.calls[0][1]
    const json = JSON.stringify(body)
    expect(json).not.toContain('accept_from')
    expect(json).not.toContain('budget')
    expect(json).not.toContain('leak')
    expect(Object.keys(body.delegation_policy).sort()).toEqual(['depth', 'modes', 'to'])
  })

  it('deleting an edge removes the ref from the save payload', async () => {
    renderScreen()
    await screen.findByTestId('delegation-graph-stub')
    fireEvent.click(screen.getByText('delete-jim-existing'))
    fireEvent.click(await screen.findByTestId('delegation-save'))

    await waitFor(() => expect(updateAgentMock).toHaveBeenCalledTimes(1))
    expect(updateAgentMock.mock.calls[0][1].delegation_policy.to).toEqual([])
  })

  it('a successful save shows a success toast', async () => {
    renderScreen()
    await screen.findByTestId('delegation-graph-stub')
    fireEvent.click(screen.getByText('connect-jim-ray'))
    fireEvent.click(await screen.findByTestId('delegation-save'))
    await waitFor(() =>
      expect(addToastMock).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success' }),
      ),
    )
  })

  it('a failed save keeps the edit and shows an error toast (no silent failure)', async () => {
    updateAgentMock.mockRejectedValueOnce(new Error('boom'))
    renderScreen()
    await screen.findByTestId('delegation-graph-stub')
    fireEvent.click(screen.getByText('connect-jim-ray'))
    fireEvent.click(await screen.findByTestId('delegation-save'))

    await waitFor(() =>
      expect(addToastMock).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error' }),
      ),
    )
    // The edit survives — Save is still offered (still dirty).
    expect(await screen.findByTestId('delegation-save')).toHaveTextContent('Save (1)')
  })

  it('Save is disabled when nothing changed', async () => {
    renderScreen()
    const save = await screen.findByTestId('delegation-save')
    expect(save).toBeDisabled()
    expect(save).toHaveTextContent('Saved')
  })

  it('renders the empty state when there are no agents', async () => {
    fetchAgentsMock.mockResolvedValue([])
    renderScreen()
    expect(await screen.findByTestId('delegation-empty')).toBeInTheDocument()
  })
})
