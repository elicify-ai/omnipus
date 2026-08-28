// AgentProfile.contextWindow.test.tsx — ADR-066 D9 (T068-30): per-agent
// `context_window_override` with the read-only "Effective window · source"
// line and the clamp indicator (FR-037, FR-002; B-45 client half).
//
// The three read-only fields (`context_window_effective`,
// `context_window_source`, `context_window_clamped`) are OPTIONAL on the
// wire until the resolver lands (T066-RESOLVE-WINDOW) — when the wire omits
// them, nothing is rendered for them. The override input is always shown
// for native agents (it is the operator's write surface) and round-trips
// through `PUT /agents/{id}` as `AgentUpdateRequest.context_window_override`.
// The backend counterpart is TDD row 42 `TestGateway_AgentWindowFieldsAndOverrideReload`.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent } from '@/lib/api'
import { CONTEXT_WINDOW_SOURCE_LABEL } from '@/components/settings/ContextSection'

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

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => ({}),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgent: vi.fn(),
    fetchWorkspace: vi.fn(),
    updateAgent: vi.fn(),
    updateWorkspace: vi.fn(),
    deleteAgent: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
  }
})

import { fetchAgent, fetchSkills, fetchProviders, updateAgent } from '@/lib/api'
import { useUiStore } from '@/store/ui'

const baseAgent: Agent = {
  id: 'general-assistant',
  name: 'General Assistant',
  type: 'Main',
  locked: false,
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'General purpose assistant',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  rate_limits: { use_global_defaults: true },
  memory_enabled: true,
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderProfile(agentId: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentProfile agentId={agentId} />
    </QueryClientProvider>,
  )
}

function switchTab(testId: string) {
  const trigger = screen.getByTestId(testId)
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
}

async function openAdvanced(agent: Agent) {
  vi.mocked(fetchAgent).mockResolvedValue(agent)
  renderProfile(agent.id)
  await screen.findByText(agent.name)
  if (!screen.queryByTestId('agent-context-window-override-input')) {
    switchTab('tab-advanced')
  }
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgent).mockReset()
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(updateAgent).mockReset().mockResolvedValue(baseAgent)
  useUiStore.setState({ toasts: [] })
})

describe('AgentProfile — context window override (ADR-066 D9, FR-037)', () => {
  it('renders the override input hydrated from Agent.context_window_override', async () => {
    await openAdvanced({ ...baseAgent, context_window_override: 200_000 })
    const input = (await screen.findByTestId('agent-context-window-override-input')) as HTMLInputElement
    expect(input.value).toBe('200000')
    expect(screen.getByText(/Context window override/i)).toBeInTheDocument()
  })

  it('renders an empty override input when the wire omits context_window_override', async () => {
    await openAdvanced(baseAgent)
    const input = (await screen.findByTestId('agent-context-window-override-input')) as HTMLInputElement
    expect(input.value).toBe('')
  })

  it('a typed override round-trips as context_window_override on PUT /agents/{id}', async () => {
    await openAdvanced(baseAgent)
    const input = (await screen.findByTestId('agent-context-window-override-input')) as HTMLInputElement
    vi.mocked(updateAgent).mockClear()
    fireEvent.change(input, { target: { value: '128000' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    const last = vi.mocked(updateAgent).mock.calls.at(-1)!
    expect(last[0]).toBe(baseAgent.id)
    expect(last[1].context_window_override).toBe(128_000)
  })

  it('clearing a set override sends null (clear), never 0 or a partial value', async () => {
    await openAdvanced({ ...baseAgent, context_window_override: 200_000 })
    const input = (await screen.findByTestId('agent-context-window-override-input')) as HTMLInputElement
    vi.mocked(updateAgent).mockClear()
    fireEvent.change(input, { target: { value: '' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    for (const call of vi.mocked(updateAgent).mock.calls) {
      expect(call[1].context_window_override).not.toBe(0)
    }
    const last = vi.mocked(updateAgent).mock.calls.at(-1)!
    expect(last[1].context_window_override).toBeNull()
  })

  it('an untouched form never sends context_window_override when the wire omitted it', async () => {
    await openAdvanced(baseAgent)
    await screen.findByTestId('agent-context-window-override-input')
    // Nudge an unrelated field so an autosave fires.
    const timeout = (await screen.findByTestId('agent-timeout-input')) as HTMLInputElement
    vi.mocked(updateAgent).mockClear()
    fireEvent.change(timeout, { target: { value: '90' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    const last = vi.mocked(updateAgent).mock.calls.at(-1)!
    expect('context_window_override' in last[1]).toBe(false)
  })

  it('hides the override field for subagent_3p (exempt subprocess-CLI rows)', async () => {
    await openAdvanced({
      ...baseAgent,
      id: 'ext',
      name: 'External',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code' },
    })
    expect(screen.queryByTestId('agent-context-window-override-input')).toBeNull()
    expect(screen.queryByTestId('agent-context-window-effective')).toBeNull()
  })
})

describe('AgentProfile — effective window, source and clamp (FR-037, FR-002, B-45)', () => {
  it('shows "Effective window · source" from context_window_effective + context_window_source', async () => {
    await openAdvanced({
      ...baseAgent,
      context_window_effective: 1_048_576,
      context_window_source: 'catalog',
      context_window_clamped: false,
    })
    const line = await screen.findByTestId('agent-context-window-effective')
    expect(line.textContent).toContain('1,048,576')
    expect(line.textContent).toContain(CONTEXT_WINDOW_SOURCE_LABEL.catalog)
    expect(screen.queryByTestId('agent-context-window-clamped')).toBeNull()
  })

  it('hides the effective line and clamp note when the wire omits the read-only fields', async () => {
    await openAdvanced(baseAgent)
    await screen.findByTestId('agent-context-window-override-input')
    expect(screen.queryByTestId('agent-context-window-effective')).toBeNull()
    expect(screen.queryByTestId('agent-context-window-clamped')).toBeNull()
  })

  it('shows the clamp note when context_window_clamped is true (override above capability)', async () => {
    await openAdvanced({
      ...baseAgent,
      context_window_override: 2_000_000,
      context_window_effective: 1_048_576,
      context_window_source: 'operator',
      context_window_clamped: true,
    })
    const note = await screen.findByTestId('agent-context-window-clamped')
    expect(note.textContent).toMatch(/clamped/i)
    expect(note.textContent).toContain('1,048,576')
    const line = screen.getByTestId('agent-context-window-effective')
    expect(line.textContent).toContain(CONTEXT_WINDOW_SOURCE_LABEL.operator)
  })

  it('renders the source label from the shared ContextSection copy for every ladder rung', async () => {
    await openAdvanced({
      ...baseAgent,
      context_window_effective: 64_000,
      context_window_source: 'floor',
    })
    const line = await screen.findByTestId('agent-context-window-effective')
    expect(line.textContent).toContain(CONTEXT_WINDOW_SOURCE_LABEL.floor)
  })
})
