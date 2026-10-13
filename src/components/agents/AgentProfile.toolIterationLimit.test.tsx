// AgentProfile.toolIterationLimit.test.tsx — #904 RED, test plan row 14
// (ToolIterationLimitField) + FR-017 (AgentProfile autosave).
//
// The per-agent limit control is extracted into ToolIterationLimitField
// (spec "Design-System Components"); its props are NOT pinned by the spec,
// so it is exercised where the operator meets it: AgentProfile → Advanced.
// Every string asserted below is quoted from the spec's "UI Screens and
// States" row for the agent profile, User Stories 2, 4 and 5, and the
// Accessibility section ("Max tool calls per turn" label, "Use global
// limit" Button). The SPA renders the SERVER's fields — effective value,
// source, override, override_ignored — and never computes them (FR-003);
// the global it names comes from GET /performance (PerformanceSettings
// .max_tool_iterations). Fixtures deliberately use non-200 values where the
// point is "no literal 200".

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent } from '@/lib/api'

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
    updateWorkspace: vi.fn(),
    deleteAgent: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
    fetchPerformanceSettings: vi.fn(),
  }
})

import { ApiError, fetchAgent, fetchSkills, fetchProviders, updateAgent, fetchPerformanceSettings } from '@/lib/api'
import { useUiStore } from '@/store/ui'

// Prefix match: the label's accessible name must START with the visible
// label text; a caption may be attached via aria-describedby (spec a11y) or,
// as today, nested in the label — this test does not judge that.
const LABEL = /^Max tool calls per turn/
const RESET = 'Use global limit'

const baseAgent = {
  revision: '0'.repeat(64),
  editable_fields: [
    { name: 'context_window_override', editable: true },
    { name: 'max_tool_iterations', editable: true },
  ],
  id: 'triage',
  name: 'Triage Worker',
  type: 'Main',
  locked: false,
  // W1-5: complete server identity; an absent field is not a valid baseline.
  figure: 'Omnipus',
  role: 'general',
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'Cheap triage',
  soul: '',
  timeout_seconds: 60,
  rate_limits: { use_global_defaults: true },
  memory_enabled: true,
}

/** Server-shaped agent: the four #904 fields exactly as the API returns them. */
function agentWith(limit: {
  effective: number
  source: 'global' | 'agent'
  override?: number
  ignored: boolean
}, extra: Record<string, unknown> = {}): Agent {
  return {
    ...baseAgent,
    ...extra,
    max_tool_iterations: limit.effective,
    max_tool_iterations_source: limit.source,
    ...(limit.override !== undefined ? { max_tool_iterations_override: limit.override } : {}),
    max_tool_iterations_override_ignored: limit.ignored,
  } as unknown as Agent
}

function perf(global: number) {
  return {
    max_parallel_agents: 4,
    effective_max_parallel_agents: 4,
    max_parallel_agents_configured: true,
    tools_on_demand: true,
    goal_max_rounds: 20,
    max_tool_iterations: global,
    max_tool_iterations_saved_state: 'ok',
  }
}

function switchTab(testId: string) {
  const trigger = screen.getByTestId(testId)
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
}

async function openAdvanced(agent: Agent, global: number) {
  vi.mocked(fetchAgent).mockResolvedValue(agent)
  vi.mocked(fetchPerformanceSettings).mockResolvedValue(perf(global) as never)
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AgentProfile agentId={agent.id} />
    </QueryClientProvider>,
  )
  await screen.findByText(agent.name)
  if (!screen.queryByLabelText(LABEL)) switchTab('tab-advanced')
  return (await screen.findByLabelText(LABEL)) as HTMLInputElement
}

/** The last updateAgent payload (second argument). */
function lastPayload(): Record<string, unknown> {
  const last = vi.mocked(updateAgent).mock.calls.at(-1)
  if (!last) throw new Error('updateAgent was never called')
  return last[1] as Record<string, unknown>
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset()
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(fetchPerformanceSettings).mockReset()
  vi.mocked(updateAgent).mockReset().mockImplementation(async (_id, body) => ({ ...baseAgent, ...body }) as never)
  useUiStore.setState({ toasts: [] })
})

describe('ToolIterationLimitField on AgentProfile → Advanced (#904 US-2, US-5)', () => {
  it('no own value: empty input, placeholder and source line name the SERVER global (350), no reset button', async () => {
    const input = await openAdvanced(agentWith({ effective: 350, source: 'global', ignored: false }), 350)
    expect(input.value).toBe('')
    expect(input).toHaveAttribute('placeholder', 'Global limit (350)')
    expect(await screen.findByText('Using the global limit (350)')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: RESET })).toBeNull()
    expect(input).toHaveAttribute('type', 'number')
  })

  it('US-2 AS-1: own value 50 under global 200 shows "Lowered for this agent: 50 (global limit 200)" and the reset', async () => {
    const input = await openAdvanced(agentWith({ effective: 50, source: 'agent', override: 50, ignored: false }), 200)
    expect(input.value).toBe('50')
    expect(await screen.findByText('Lowered for this agent: 50 (global limit 200)')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: RESET })).toBeInTheDocument()
  })

  it('US-5 AS-2 (D1): stored 500 above global 200 is flagged "Own value 500 is above the global limit (200) and has no effect" with the reset', async () => {
    await openAdvanced(agentWith({ effective: 200, source: 'global', override: 500, ignored: true }), 200)
    expect(await screen.findByText('Own value 500 is above the global limit (200) and has no effect')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: RESET })).toBeInTheDocument()
  })

  it('US-2 AS-2 (D9, FR-008): "Use global limit" sends max_tool_iterations: null and keeps focus on the input', async () => {
    const input = await openAdvanced(agentWith({ effective: 50, source: 'agent', override: 50, ignored: false }), 200)
    vi.mocked(updateAgent).mockClear()
    fireEvent.click(screen.getByRole('button', { name: RESET }))
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    const payload = lastPayload()
    expect('max_tool_iterations' in payload).toBe(true)
    expect(payload.max_tool_iterations).toBeNull()
    expect(document.activeElement).toBe(input)
  })

  it('typing an own value sends exactly that number', async () => {
    const input = await openAdvanced(agentWith({ effective: 200, source: 'global', ignored: false }), 200)
    vi.mocked(updateAgent).mockClear()
    fireEvent.change(input, { target: { value: '50' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    expect(lastPayload().max_tool_iterations).toBe(50)
  })

  it('US-2 AS-6 / FR-017: an unrelated autosave on a capped-and-flagged agent does NOT resend the limit', async () => {
    await openAdvanced(agentWith({ effective: 200, source: 'global', override: 500, ignored: true }), 200)
    const other = (await screen.findByTestId('agent-context-window-override-input')) as HTMLInputElement
    vi.mocked(updateAgent).mockClear()
    fireEvent.change(other, { target: { value: '128000' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    for (const call of vi.mocked(updateAgent).mock.calls) {
      expect('max_tool_iterations' in (call[1] as Record<string, unknown>)).toBe(false)
    }
  })

  it('US-2 AS-3: a server refusal naming the global is shown inline', async () => {
    const msg = 'max_tool_iterations 300 is above the global limit (200); lower it, or raise the global limit in Settings → Performance'
    const input = await openAdvanced(agentWith({ effective: 200, source: 'global', ignored: false }), 200)
    vi.mocked(updateAgent).mockReset().mockRejectedValue(new ApiError(400, msg))
    fireEvent.change(input, { target: { value: '300' } })
    expect(await screen.findByText(msg, {}, { timeout: 6000 })).toBeInTheDocument()
  })
})

describe('ToolIterationLimitField for external-CLI workers (#904 US-4 AS-3, D14)', () => {
  it('a subagent_3p profile shows the same control, source line and reset', async () => {
    await openAdvanced(
      agentWith(
        { effective: 30, source: 'agent', override: 30, ignored: false },
        { id: 'ext', name: 'External Worker', type: 'subagent_3p', executor: { kind: 'external-cli', cli: 'claude-code' } },
      ),
      200,
    )
    expect(await screen.findByText('Lowered for this agent: 30 (global limit 200)')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: RESET })).toBeInTheDocument()
  })
})
