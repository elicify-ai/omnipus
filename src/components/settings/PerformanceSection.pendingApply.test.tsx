/**
 * #904 gate round 3, silent-failure #2 — the "saved, but not applied yet"
 * notice is driven by the server's pending-apply state
 * (PerformanceSettings.pending_apply, contracts/components/schemas/
 * PerformancePendingApply.yaml), not by client-only state:
 *
 *   - it survives an unrelated later save while the server still reports the
 *     settings as pending (agents keep running the old limit);
 *   - it is shown from GET alone, e.g. after a page reload, naming the
 *     server's changed_fields;
 *   - it goes only once the server reports everything applied.
 *
 * Oracles come from the contract text: pending_apply is present while saved
 * settings are not in force; stage `refresh` means GET still shows the OLD
 * values, stage `reload` means GET shows the new values.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchPerformanceSettings: vi.fn(),
    updatePerformanceSettings: vi.fn(),
    fetchMaxToolIterationsLoweringPreview: vi.fn(),
    fetchAppState: vi.fn(),
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import type { PerformanceSettings, PerformancePendingApply } from '@/lib/api/generated/openapi-types'
import { PerformanceSection } from './PerformanceSection'

const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

const SETTINGS: PerformanceSettings = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
  goal_max_rounds: 20,
  max_tool_iterations: 300,
  max_tool_iterations_saved_state: 'ok',
}

const WAIT = { timeout: 3000 }
const STEP_UP_CONFIRM = 'Change performance settings'
const LABEL = 'Max tool calls per turn'
const NOTICE = 'performance-unapplied-notice'
const REFRESH_TEXT = 'the new tool-iteration limit applies after the next reload or restart'
const SAVED_SHOWN =
  'The fields below show the saved values. Until the restart or reload, Omnipus keeps running on the previous ones.'
const RUNNING_SHOWN =
  'The fields below still show the values Omnipus is running on; the saved ones take effect after the restart or reload.'
const CHECKS =
  'A new tool-call limit set here is checked against the saved limit; an agent’s own limit, set on its profile, is still checked against the running one.'

function pending(stage: PerformancePendingApply['stage'], fields: PerformancePendingApply['changed_fields']): PerformanceSettings {
  return { ...SETTINGS, pending_apply: { stage, changed_fields: fields } }
}

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
  return client
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.updatePerformanceSettings).mockReset()
  vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockReset()
  vi.mocked(api.fetchPerformanceSettings).mockReset()
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

describe('PerformanceSection — the not-applied notice follows the server (#904 pending_apply)', () => {
  it('after a page reload, GET alone shows the notice for stage reload, naming the pending settings', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(pending('reload', ['goal_max_rounds', 'max_tool_iterations']))
    renderSection()
    const notice = await screen.findByTestId(NOTICE, {}, WAIT)
    expect(notice).toHaveAttribute('role', 'status')
    expect(notice).toHaveTextContent(
      'Saved, but not applied yet: the new goal tries, tool-call limit will be used after a restart or reload',
    )
    expect(notice).toHaveTextContent(SAVED_SHOWN)
    expect(notice).toHaveTextContent(CHECKS)
  })

  it('for stage refresh with no saved values known to this page, it says the fields show the running values', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(pending('refresh', ['max_tool_iterations']))
    renderSection()
    const notice = await screen.findByTestId(NOTICE, {}, WAIT)
    expect(notice).toHaveTextContent(
      'Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: the new tool-call limit will be used after a restart or reload',
    )
    expect(notice).toHaveTextContent(RUNNING_SHOWN)
    expect(notice).not.toHaveTextContent(SAVED_SHOWN)
  })

  it('no pending_apply on GET: no notice', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS)
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
    expect(screen.queryByTestId(NOTICE)).not.toBeInTheDocument()
  })

  it('an unrelated later save keeps the notice while the server still reports pending, and it goes once the server reports applied', async () => {
    // A not-applied tool-limit save (raise 300 → 400, stage refresh).
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValueOnce(SETTINGS)
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(pending('refresh', ['max_tool_iterations']))
    vi.mocked(api.updatePerformanceSettings).mockRejectedValueOnce(
      new api.PerformanceReloadFailedError(REFRESH_TEXT, '{}', null, { stage: 'refresh', changedFields: ['max_tool_iterations'] }),
    )
    const client = renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '400' } })
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
    await screen.findByTestId(NOTICE, {}, WAIT)

    // An unrelated save (max parallel agents) succeeds, but the server still
    // reports the tool-call limit as not applied — in its PUT answer and on GET.
    vi.mocked(api.updatePerformanceSettings).mockResolvedValueOnce({
      ...pending('refresh', ['max_tool_iterations']),
      max_parallel_agents: 8,
      effective_max_parallel_agents: 8,
    })
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...pending('refresh', ['max_tool_iterations']),
      max_parallel_agents: 8,
      effective_max_parallel_agents: 8,
    })
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    await new Promise((r) => setTimeout(r, 700))
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(2))
    // GET re-read after the initial load, the failed save and this save.
    await waitFor(() => expect(vi.mocked(api.fetchPerformanceSettings).mock.calls.length).toBeGreaterThanOrEqual(3))
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.getByTestId(NOTICE)).toHaveTextContent('Saved, but not applied yet')
    // The saved-but-unapplied limit stays on show.
    expect(screen.getByLabelText(LABEL)).toHaveValue(400)

    // The gateway reloaded: GET reports everything applied.
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({ ...SETTINGS, max_parallel_agents: 8, effective_max_parallel_agents: 8, max_tool_iterations: 400 })
    await client.invalidateQueries({ queryKey: ['performance-settings'] })
    await waitFor(() => expect(screen.queryByTestId(NOTICE)).not.toBeInTheDocument())
    expect(screen.getByLabelText(LABEL)).toHaveValue(400)
  })
})
