// AgentProfile.limitRefusal.test.tsx — #904 8-reviewer-gate fixes (team-lead
// brief items 4 and 5), frontend-lead's own tests:
//   item 4 — only the limit's own refusal (D10 above-global / 1–1000 bound, a
//   400 naming max_tool_iterations) is pinned on the limit field; a 5xx or a
//   revision 409 shows on the normal autosave indicator; a refused limit never
//   blocks unrelated fields from saving;
//   item 5 — a pre-#904 own value outside 1–1000 on an external-CLI worker is
//   not sent to the executor preview, and the preview is not left in error.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
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


const D10 = 'max_tool_iterations 300 is above the global limit (200); lower it, or raise the global limit in Settings → Performance'

function switchTab(testId: string) {
  const trigger = screen.getByTestId(testId)
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
}
const openBasics = () => switchTab('tab-basics')

function indicator() {
  return screen.getByTestId('last-saved-indicator')
}

// updateAgent refuses any payload carrying the limit with the D10 400 and
// accepts everything else — the server's real behaviour for an above-global value.
function refuseLimitOnly() {
  vi.mocked(updateAgent).mockImplementation(async (_id, payload) => {
    if ((payload as Record<string, unknown>).max_tool_iterations !== undefined) throw new ApiError(400, D10, { field: 'max_tool_iterations' })
    return { ...baseAgent, ...(payload as Partial<Agent>), revision: '1'.repeat(64) } as Agent
  })
}

const payloads = () => vi.mocked(updateAgent).mock.calls.map((c) => c[1] as Record<string, unknown>)

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset()
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(fetchPerformanceSettings).mockReset().mockResolvedValue(performance200)
  vi.mocked(fetchExecutorPreview).mockReset().mockReturnValue(new Promise(() => {}))
  vi.mocked(updateAgent).mockReset().mockImplementation(async () => baseAgent)
  vi.mocked(testAgentRunner).mockReset().mockResolvedValue({ ok: true, reason: '', message: '' } as Awaited<ReturnType<typeof testAgentRunner>>)
  useUiStore.setState({ toasts: [] })
})

describe('AgentProfile — limit autosave errors (#904 review gate, item 4)', () => {
  it('a 5xx while the limit edit is pending shows on the autosave indicator, not on the limit field', async () => {
    const msg = 'The server hit an internal error.'
    vi.mocked(updateAgent).mockRejectedValue(new ApiError(500, msg))
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '150' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    await waitFor(() => expect(within(indicator()).getByText(msg)).toBeInTheDocument(), { timeout: 3000 })
    expect(within(indicator()).getByRole('alert')).toHaveTextContent(msg)
    expect(input).not.toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryAllByText(msg)).toHaveLength(1)
  })

  it('a revision 409 while the limit edit is pending is not pinned on the limit field', async () => {
    vi.mocked(updateAgent).mockRejectedValue(new ApiError(409, 'This conflicts with the current state.'))
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '150' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 6000 })
    await waitFor(() =>
      expect(useUiStore.getState().toasts.some((t) => /changed elsewhere/.test(t.message))).toBe(true),
    )
    expect(input).not.toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByText('This conflicts with the current state.', { selector: '[role="alert"]:not([data-testid])' })).toBeNull()
    expect(within(indicator()).getByRole('alert')).toBeInTheDocument()
  })

  it('the D10 refusal is on the field only; the indicator does not repeat it', async () => {
    refuseLimitOnly()
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '300' } })
    const alert = await screen.findByText(D10, { selector: '[role="alert"]' }, { timeout: 6000 })
    expect(input.getAttribute('aria-describedby')).toContain(alert.id)
    expect(screen.getAllByText(D10)).toHaveLength(1)
    expect(indicator()).toHaveTextContent('Save failed')
  })

  it('an unrelated edit after a refusal still saves (without the refused limit) and the refusal stays explained', async () => {
    refuseLimitOnly()
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '300' } })
    await screen.findByText(D10, { selector: '[role="alert"]' }, { timeout: 6000 })

    openBasics()
    const description = (await screen.findAllByTestId('agent-description-input'))[0] as HTMLTextAreaElement
    fireEvent.change(description, { target: { value: 'Sorts things fast' } })
    await waitFor(() => expect(payloads().some((p) => p.description === 'Sorts things fast')).toBe(true), { timeout: 6000 })
    const saved = payloads().find((p) => p.description === 'Sorts things fast')!
    expect('max_tool_iterations' in saved).toBe(false)
    await waitFor(() => expect(indicator()).not.toHaveTextContent('Save failed'), { timeout: 3000 })
    // Back on the Advanced tab, the limit field still explains its refusal.
    switchTab('tab-advanced')
    expect(await screen.findByText(D10, { selector: '[role="alert"]' })).toBeInTheDocument()
  })

  it('a refusal in a PUT that also carried another field retries once without the limit, so that field is saved', async () => {
    refuseLimitOnly()
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '300' } })
    openBasics()
    const description = (await screen.findAllByTestId('agent-description-input'))[0] as HTMLTextAreaElement
    fireEvent.change(description, { target: { value: 'Sorts things fast' } })
    await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(2), { timeout: 6000 })
    expect(payloads()[0]).toMatchObject({ max_tool_iterations: 300, description: 'Sorts things fast' })
    expect(payloads()[1].description).toBe('Sorts things fast')
    expect('max_tool_iterations' in payloads()[1]).toBe(false)
    switchTab('tab-advanced')
    expect(await screen.findByText(D10, { selector: '[role="alert"]' })).toBeInTheDocument()
  })

  it('editing the limit again clears the refusal and sends the new value', async () => {
    refuseLimitOnly()
    const input = await openAdvanced(baseAgent)
    fireEvent.change(input, { target: { value: '300' } })
    await screen.findByText(D10, { selector: '[role="alert"]' }, { timeout: 6000 })
    vi.mocked(updateAgent).mockImplementation(async () => baseAgent)
    fireEvent.change(input, { target: { value: '150' } })
    expect(screen.queryByText(D10)).toBeNull()
    await waitFor(() => expect(payloads().at(-1)!.max_tool_iterations).toBe(150), { timeout: 6000 })
  })
})

describe('AgentProfile — executor preview with a pre-#904 out-of-range own value (item 5)', () => {
  const worker: Agent = {
    ...baseAgent,
    id: 'ext',
    name: 'External Worker',
    type: 'subagent_3p',
    executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/bin/claude' },
    max_tool_iterations: 200,
    max_tool_iterations_source: 'global',
    max_tool_iterations_override: 5000,
    max_tool_iterations_override_ignored: true,
  }

  it('omits the out-of-range value so the server previews the resolved limit, and the preview is ready, not in error', async () => {
    vi.mocked(fetchExecutorPreview).mockImplementation(async (req) => {
      const n = (req as { max_tool_iterations?: number }).max_tool_iterations
      if (n !== undefined && (n < 1 || n > 1000)) throw new ApiError(400, 'max_tool_iterations must be between 1 and 1000', { field: 'max_tool_iterations' })
      return { binary: 'claude', argv: ['-p', '--max-turns', '200'], command_line: 'claude -p --max-turns 200', prompt_delivery: 'stdin', dropped_args: [] }
    })
    renderProfile(worker)
    await screen.findByText(worker.name)
    const runtime = screen.getByTestId('tab-runtime')
    runtime.focus()
    fireEvent.keyDown(runtime, { key: 'Enter' })
    fireEvent.click(runtime)
    await waitFor(() => expect(fetchExecutorPreview).toHaveBeenCalled(), { timeout: 3000 })
    for (const call of vi.mocked(fetchExecutorPreview).mock.calls) {
      expect('max_tool_iterations' in (call[0] as Record<string, unknown>)).toBe(false)
    }
    await waitFor(() =>
      expect(screen.getByTestId('profile-command-preview')).toHaveAttribute('data-preview-status', 'ready'),
    { timeout: 3000 })
  })
})
