// AgentProfile.toolIterationLimitWiring.test.tsx — #904 tool-iteration-limit spec,
// profile wiring of the extracted ToolIterationLimitField: FR-017 (send the
// limit only when changed or reset), D9 (reset sends a raw JSON null), D10
// (server refusal shown on the field), D14 (subagent_3p gets the control),
// and the global read from GET /performance. Backend handlers are mocked with
// bodies shaped by the generated Agent / PerformanceSettings types.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent, PerformanceSettings } from '@/lib/api'

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

import { fetchAgent, fetchSkills, fetchProviders, updateAgent, fetchPerformanceSettings, fetchExecutorPreview, testAgentRunner, ApiError } from '@/lib/api'
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
  figure: 'Omnipus',
  role: 'general',
}

const cappedAgent: Agent = {
  ...baseAgent,
  max_tool_iterations: 200,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override: 500,
  max_tool_iterations_override_ignored: true,
}

const loweredAgent: Agent = {
  ...baseAgent,
  max_tool_iterations: 50,
  max_tool_iterations_source: 'agent',
  max_tool_iterations_override: 50,
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

function lastPayload(): Record<string, unknown> {
  return vi.mocked(updateAgent).mock.calls.at(-1)![1] as Record<string, unknown>
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

describe('AgentProfile — Max tool calls per turn (#904)', () => {
  it('US-2 AS-1: lowering sends only the own value and the profile shows the server source line', async () => {
    // The server now holds the own value: the PUT response and every later
    // GET return the lowered agent.
    vi.mocked(updateAgent).mockImplementation(async () => {
      vi.mocked(fetchAgent).mockResolvedValue(loweredAgent)
      return loweredAgent
    })
    const input = await openAdvanced(baseAgent)
    expect(input.value).toBe('')
    expect(await screen.findByText('Using the global limit (200)')).toBeInTheDocument()
    expect(input.placeholder).toBe('Global limit (200)')

    fireEvent.change(input, { target: { value: '50' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    expect(lastPayload().max_tool_iterations).toBe(50)
    expect(await screen.findByText('Lowered for this agent: 50 (global limit 200)')).toBeInTheDocument()
  })

  it('US-2 AS-4: saving exactly the global for an agent riding it is sent (it becomes the own value)', async () => {
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '200' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    expect(lastPayload().max_tool_iterations).toBe(200)
  })

  it('D9 / US-2 AS-2: "Use global limit" sends max_tool_iterations: null', async () => {
    await openAdvanced(loweredAgent)
    expect(screen.getByText('Lowered for this agent: 50 (global limit 200)')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('tool-iteration-limit-reset'))
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    const payload = lastPayload()
    expect('max_tool_iterations' in payload).toBe(true)
    expect(payload.max_tool_iterations).toBeNull()
    expect(JSON.stringify(payload)).toContain('"max_tool_iterations":null')
  })

  it('FR-017 / US-2 AS-6: an unrelated autosave of a capped agent does not re-send the limit', async () => {
    await openAdvanced(cappedAgent)
    expect(screen.getByText('Own value 500 is above the global limit (200) and has no effect')).toBeInTheDocument()
    expect(screen.getByTestId('tool-iteration-limit-reset')).toBeInTheDocument()

    const trigger = screen.getByTestId('tab-basics')
    trigger.focus()
    fireEvent.keyDown(trigger, { key: 'Enter' })
    fireEvent.click(trigger)
    const description = (await screen.findAllByTestId('agent-description-input'))[0] as HTMLTextAreaElement
    fireEvent.change(description, { target: { value: 'Sorts things fast' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    const payload = lastPayload()
    expect(payload.description).toBe('Sorts things fast')
    expect('max_tool_iterations' in payload).toBe(false)
  })

  it('D10: a server refusal of the limit is shown on the field', async () => {
    const refusal = 'max_tool_iterations 300 is above the global limit (200); lower it, or raise the global limit in Settings → Performance'
    vi.mocked(updateAgent).mockRejectedValue(new ApiError(400, refusal, { field: 'max_tool_iterations' }))
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '300' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    const alert = await screen.findByText(refusal, { selector: '[role="alert"]' })
    expect(input.getAttribute('aria-describedby')).toContain(alert.id)
  })

  it('D14: a subagent_3p worker shows the same control and sends its own value', async () => {
    const worker: Agent = {
      ...baseAgent,
      id: 'ext',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/bin/claude' },
    }
    vi.mocked(updateAgent).mockResolvedValue(worker)
    // I12: an external-cli save runs the runner test first.
    vi.mocked(testAgentRunner).mockResolvedValue({ ok: true, reason: '', message: '' } as Awaited<ReturnType<typeof testAgentRunner>>)
    const input = await openAdvanced(worker)
    expect(screen.getByText('Using the global limit (200)')).toBeInTheDocument()
    fireEvent.change(input, { target: { value: '30' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    expect(lastPayload().max_tool_iterations).toBe(30)
  })

  it('D4: the worker command preview omits max_tool_iterations when the agent has no own value (never 0)', async () => {
    const worker: Agent = {
      ...baseAgent,
      id: 'ext',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/bin/claude' },
    }
    renderProfile(worker)
    await screen.findByText(worker.name)
    const runtime = screen.getByTestId('tab-runtime')
    runtime.focus()
    fireEvent.keyDown(runtime, { key: 'Enter' })
    fireEvent.click(runtime)
    await waitFor(() => expect(fetchExecutorPreview).toHaveBeenCalled(), { timeout: 3000 })
    const body = vi.mocked(fetchExecutorPreview).mock.calls.at(-1)![0] as Record<string, unknown>
    expect('max_tool_iterations' in body).toBe(false)
    expect(JSON.stringify(body)).not.toContain('max_tool_iterations')
  })

  it('D4: the worker command preview carries the stored own value', async () => {
    const worker: Agent = {
      ...loweredAgent,
      max_tool_iterations: 30,
      max_tool_iterations_override: 30,
      id: 'ext',
      name: 'External Worker',
      type: 'subagent_3p',
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/bin/claude' },
    }
    renderProfile(worker)
    await screen.findByText(worker.name)
    const runtime = screen.getByTestId('tab-runtime')
    runtime.focus()
    fireEvent.keyDown(runtime, { key: 'Enter' })
    fireEvent.click(runtime)
    await waitFor(() => expect(fetchExecutorPreview).toHaveBeenCalled(), { timeout: 3000 })
    expect((vi.mocked(fetchExecutorPreview).mock.calls.at(-1)![0] as Record<string, unknown>).max_tool_iterations).toBe(30)
  })

  it('FR-004: with GET /performance failing, no global number is invented', async () => {
    vi.mocked(fetchPerformanceSettings).mockRejectedValue(new Error('503'))
    const input = await openAdvanced(loweredAgent)
    expect(screen.getByText('Lowered for this agent: 50')).toBeInTheDocument()
    expect(input.value).toBe('50')
  })
})
