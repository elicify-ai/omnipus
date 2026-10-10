// AgentProfile.toolIterationLimitRefusal.test.tsx — #904 gate ROUND 2 gaps,
// SPA side of the per-agent limit refusal (D10 above-the-global, FR-006
// bound).
//
// Sources: spec D10/FR-007 (a per-agent save above the global is refused and
// the stored value is unchanged); FR-017 (the limit is sent only when changed
// or reset — a refused value must not be re-sent behind the operator's back);
// the ErrorResponse contract's `field` property
// (contracts/components/schemas/ErrorResponse.yaml: "names the request field
// the error is about"), which gate round 2 makes the server set to
// "max_tool_iterations" on every per-agent limit refusal (backend-lead,
// feature/904-fix2-be). The refusal is therefore recognised by `field`, not
// by the wording of the message.
//
// Harness copied from AgentProfile.toolIterationLimitWiring.test.tsx (API
// module functions mocked at the module boundary; the keepalive flush is a
// raw fetch, stubbed on globalThis).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, PerformanceSettings } from '@/lib/api'
import { isToolIterationLimitRefusal, useToolIterationLimitRefusal } from './useToolIterationLimitRefusal'

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
    fetchExecutorPreview: vi.fn(),
    fetchPerformanceSettings: vi.fn(),
  }
})

import { fetchAgent, fetchSkills, fetchProviders, updateAgent, fetchPerformanceSettings, fetchExecutorPreview, ApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'

const performance200 = { max_tool_iterations: 200, max_tool_iterations_saved_state: 'ok' } as PerformanceSettings

const baseAgent: Agent = {
  revision: '0'.repeat(64),
  editable_fields: [
    { name: 'max_tool_iterations', editable: true },
    { name: 'description', editable: true },
  ],
  id: 'triage',
  name: 'Triage',
  type: 'Main',
  locked: false,
  figure: 'Robot',
  role: 'general',
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'Sorts things',
  soul: '',
  timeout_seconds: 60,
  max_tool_iterations: 200,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  rate_limits: { use_global_defaults: true },
  memory_enabled: true,
}

function renderProfile(agent: Agent) {
  vi.mocked(fetchAgent).mockResolvedValue(agent)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AgentProfile agentId={agent.id} />
    </QueryClientProvider>,
  )
}

async function openAdvanced(agent: Agent) {
  renderProfile(agent)
  await screen.findByText(agent.name)
  if (!screen.queryByTestId('agent-max-tool-calls-input')) {
    const trigger = screen.getByTestId('tab-advanced')
    trigger.focus()
    fireEvent.keyDown(trigger, { key: 'Enter' })
    fireEvent.click(trigger)
  }
  return (await screen.findByTestId('agent-max-tool-calls-input')) as HTMLInputElement
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset()
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(fetchPerformanceSettings).mockReset().mockResolvedValue(performance200)
  vi.mocked(fetchExecutorPreview).mockReset().mockReturnValue(new Promise(() => {}))
  vi.mocked(updateAgent).mockReset().mockImplementation(async () => baseAgent)
  useUiStore.setState({ toasts: [] })
})


// A refusal whose message does NOT name the field: only `field` identifies it.
const NEUTRAL_MESSAGE = 'The value was refused by the server.'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('isToolIterationLimitRefusal — recognised by ErrorResponse.field', () => {
  it('a 400 whose field is max_tool_iterations is a limit refusal even when the message does not name it', async () => {
    const err = await ApiError.fromResponse(jsonResponse(400, { error: NEUTRAL_MESSAGE, field: 'max_tool_iterations' }))
    expect(err.field, 'instrument: the real response parser carries field').toBe('max_tool_iterations')
    expect(isToolIterationLimitRefusal(err)).toBe(true)
  })

  it('a 400 about another field is not a limit refusal', async () => {
    const err = await ApiError.fromResponse(jsonResponse(400, { error: NEUTRAL_MESSAGE, field: 'description' }))
    expect(isToolIterationLimitRefusal(err)).toBe(false)
  })

  it('a 500 naming the field is not a refusal (the save failed; the value was not judged)', async () => {
    const err = await ApiError.fromResponse(jsonResponse(500, { error: NEUTRAL_MESSAGE, field: 'max_tool_iterations' }))
    expect(isToolIterationLimitRefusal(err)).toBe(false)
  })
})

describe('AgentProfile — a refused limit is never re-sent by the tab-close flush', () => {
  let keepalive: { url: string; body: Record<string, unknown> }[]

  beforeEach(() => {
    keepalive = []
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.keepalive) keepalive.push({ url: String(input), body: JSON.parse(String(init.body)) as Record<string, unknown> })
      return jsonResponse(200, baseAgent)
    }))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('FR-017 / D10: after the server refuses 300, a pagehide flush sends the pending edit without the refused limit', async () => {
    vi.mocked(updateAgent).mockImplementation(async (_id: string, data: unknown) => {
      if ((data as Record<string, unknown>).max_tool_iterations === 300) {
        throw new ApiError(400, NEUTRAL_MESSAGE, { field: 'max_tool_iterations' })
      }
      return baseAgent
    })
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '300' } })
    // The refusal (recognised by field) is pinned on the limit field.
    const alert = await screen.findByText(NEUTRAL_MESSAGE, { selector: '[role="alert"]' }, { timeout: 6000 })
    expect(input.getAttribute('aria-describedby')).toContain(alert.id)
    const refusedSends = vi.mocked(updateAgent).mock.calls.filter((c) => (c[1] as Record<string, unknown>).max_tool_iterations === 300)
    expect(refusedSends, 'instrument: the refused value reached the server exactly once').toHaveLength(1)

    // An unrelated edit is pending (inside the 1500 ms debounce) when the tab closes.
    const trigger = screen.getByTestId('tab-basics')
    trigger.focus()
    fireEvent.keyDown(trigger, { key: 'Enter' })
    fireEvent.click(trigger)
    const description = (await screen.findAllByTestId('agent-description-input'))[0] as HTMLTextAreaElement
    fireEvent.change(description, { target: { value: 'Sorts things fast' } })
    await act(async () => {
      window.dispatchEvent(new Event('pagehide'))
    })

    expect(keepalive, 'the pagehide flush fired exactly once').toHaveLength(1)
    expect(keepalive[0].url).toBe('/api/v1/agents/triage')
    expect(keepalive[0].body.description).toBe('Sorts things fast')
    expect('max_tool_iterations' in keepalive[0].body, `flush body re-sent the refused limit: ${JSON.stringify(keepalive[0].body)}`).toBe(false)
  })
})

// CHECK round 2, finding B (surviving mutant S5c: the refusal's agentId
// scoping removed). AgentProfile is NOT remounted per agent (it lives at the
// route level and only its agentId prop changes), so a refusal recorded for
// agent A must never follow the form to agent B: B's same value is sent
// normally and nothing is pinned on B's field (the hook's own contract: "a
// refusal belongs to one agent and never follows the form to another").
describe('AgentProfile — a limit refusal belongs to one agent', () => {
  it('after A is refused 300, switching the same profile to B and entering 300 sends it for B and pins nothing', async () => {
    const agentB: Agent = { ...baseAgent, id: 'beta', name: 'Beta Agent' }
    vi.mocked(fetchAgent).mockImplementation(async (id: string) => (id === 'beta' ? agentB : baseAgent))
    vi.mocked(updateAgent).mockImplementation(async (id: string, data: unknown) => {
      if (id === 'triage' && (data as Record<string, unknown>).max_tool_iterations === 300) {
        throw new ApiError(400, NEUTRAL_MESSAGE, { field: 'max_tool_iterations' })
      }
      return id === 'beta' ? agentB : baseAgent
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="triage" />
      </QueryClientProvider>,
    )
    await screen.findByText(baseAgent.name)
    const openLimit = async () => {
      if (!screen.queryByTestId('agent-max-tool-calls-input')) {
        const trigger = screen.getByTestId('tab-advanced')
        trigger.focus()
        fireEvent.keyDown(trigger, { key: 'Enter' })
        fireEvent.click(trigger)
      }
      return (await screen.findByTestId('agent-max-tool-calls-input')) as HTMLInputElement
    }
    fireEvent.change(await openLimit(), { target: { value: '300' } })
    await screen.findByText(NEUTRAL_MESSAGE, { selector: '[role="alert"]' }, { timeout: 6000 })

    rerender(
      <QueryClientProvider client={client}>
        <AgentProfile agentId="beta" />
      </QueryClientProvider>,
    )
    await screen.findByText(agentB.name)
    const inputB = await openLimit()
    await vi.waitFor(() => expect(inputB.value, 'instrument: B hydrated with no own value').toBe(''))
    expect(screen.queryByText(NEUTRAL_MESSAGE, { selector: '[role="alert"]' }), "A's refusal is not shown on B").toBeNull()

    fireEvent.change(inputB, { target: { value: '300' } })
    await vi.waitFor(
      () => expect(vi.mocked(updateAgent).mock.calls.some((c) => c[0] === 'beta')).toBe(true),
      { timeout: 6000 },
    )
    const bCalls = vi.mocked(updateAgent).mock.calls.filter((c) => c[0] === 'beta')
    expect(bCalls[0][1] as Record<string, unknown>, "B's 300 is sent, not dropped as A's refused value").toMatchObject({ max_tool_iterations: 300 })
    expect(screen.queryByText(NEUTRAL_MESSAGE, { selector: '[role="alert"]' }), 'nothing is pinned on B').toBeNull()
  })
})

// Hook-level twin (kills S5c directly). Through the profile, re-typing the
// limit on B calls clear() and hides a missing scope; the hook's own contract
// — "a refusal belongs to one agent and never follows the form to another" —
// is asserted on every read the profile makes: the pinned message, the dirty
// hold, and the payload filter used by autosave AND the pagehide flush.
describe('useToolIterationLimitRefusal — scoped to the agent it was recorded for', () => {
  it("A's refused 300 is not applied to B: nothing pinned, not held dirty, not dropped from B's payload", async () => {
    const { result, rerender } = renderHook(({ id }) => useToolIterationLimitRefusal(id), { initialProps: { id: 'agent-a' } })
    const refusal = new ApiError(400, NEUTRAL_MESSAGE, { field: 'max_tool_iterations' })
    const send = vi.fn(async (data: { max_tool_iterations?: number | null }) => {
      if (data.max_tool_iterations === 300) throw refusal
      return true
    })
    await act(async () => {
      await result.current.save({ max_tool_iterations: 300, description: 'x' }, send)
    })
    // Instrument: on A the refusal is recorded and applied.
    expect(result.current.messageFor(300), 'instrument: A pins its refusal').toBe(NEUTRAL_MESSAGE)
    expect(result.current.withoutRefused({ max_tool_iterations: 300 }).max_tool_iterations, 'instrument: A drops its refused value').toBeUndefined()

    rerender({ id: 'agent-b' })
    expect(result.current.messageFor(300), 'nothing pinned on B').toBeNull()
    expect(result.current.holdsRefused(300), 'B is not held dirty by A\'s refusal').toBe(false)
    expect(result.current.withoutRefused({ max_tool_iterations: 300 }).max_tool_iterations, "B's 300 is sent").toBe(300)
  })
})
