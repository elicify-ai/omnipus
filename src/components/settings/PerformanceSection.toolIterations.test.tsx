/**
 * PerformanceSection.toolIterations.test.tsx — #904 global tool-iteration
 * limit (docs/internal/specs/tool-iteration-limit-spec.md US-1, US-6;
 * founder decisions D3, D8, D11, D13, D16, D17; grill F4).
 *
 * Oracles come from the spec, not the implementation: the bound message
 * ("must be between 1 and 1000", US-1 AS-3), the dialog listing "A: 250 → 200"
 * before any write (US-6 AS-1), Cancel writes nothing (US-6 AS-2), the PUT
 * carries the exact confirmed snapshot (D11/D16), a drift 409 reloads the
 * dialog with the fresh list + "list changed" alert and needs a fresh Confirm
 * (US-6 AS-6), and the F4 result is a 10 s status toast plus an inline
 * role="status" summary.
 *
 * Backend handlers are not built yet: responses are mocks shaped by the
 * generated types (src/lib/api/generated/openapi-types.ts).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
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
import type {
  PerformanceSettings,
  MaxToolIterationsLoweringPreview,
  MaxToolIterationsLoweringConflict,
} from '@/lib/api/generated/openapi-types'
import { PerformanceSection } from './PerformanceSection'

// Platform mode pins useStepUp() to its ConfirmDialog (no password), so the
// step-up confirm is a button click (ADR-0010 WP3).
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

const AGENT_A = { agent_id: 'a', agent_name: 'Alpha', old_value: 250, new_value: 200 }
const AGENT_B = { agent_id: 'b', agent_name: 'Beta', old_value: 220, new_value: 200 }

const WAIT = { timeout: 3000 }
const STEP_UP_CONFIRM = 'Change performance settings'
const LABEL = 'Max tool calls per turn'

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

async function typeLimit(value: string) {
  await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
  fireEvent.change(screen.getByLabelText(LABEL), { target: { value } })
}

beforeEach(() => {
  vi.clearAllMocks()
  // mockReset, not only clear: an unconsumed *Once value from one test must
  // never leak into the next.
  vi.mocked(api.updatePerformanceSettings).mockReset()
  vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockReset()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

// One suite title shared by three describe blocks: full test names stay
// unchanged; the split only keeps each block under the function-size budget.
const SUITE = 'PerformanceSection — global max tool iterations (#904)'

describe(SUITE, () => {
  it('shows the in-force global beside the other Performance budgets (US-1 AS-1, D3)', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
    const input = screen.getByLabelText(LABEL)
    expect(input).toHaveAttribute('min', '1')
    expect(input).toHaveAttribute('max', '1000')
    expect(screen.getByLabelText('Tries per goal')).toBeInTheDocument()
    expect(screen.queryByTestId('performance-max-tool-iterations-saved-warning')).not.toBeInTheDocument()
  })

  it.each(['0', '1001', '-5', '2.5'])(
    'refuses %s inline with a message naming the bound, with no preview and no write (US-1 AS-3)',
    async (value) => {
      renderSection()
      await typeLimit(value)
      const alert = await screen.findByText('Max tool calls per turn must be between 1 and 1000.', {}, WAIT)
      expect(alert).toHaveAttribute('role', 'alert')
      expect(screen.getByLabelText(LABEL)).toHaveAttribute('aria-invalid', 'true')
      expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
      expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    },
  )

  it('accepts the upper bound 1000 — a raise, so it goes straight to the step-up gate (US-1 AS-3 boundary, D20)', async () => {
    renderSection()
    await typeLimit('1000')
    await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT)
    expect(screen.queryByText(/must be between 1 and 1000/)).not.toBeInTheDocument()
    expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
  })

  it('accepts the lower bound 1 — a lowering, so it is previewed first (US-1 AS-3 boundary, D11)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 1, agents: [] })
    renderSection()
    await typeLimit('1')
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(1), WAIT)
    expect(screen.queryByText(/must be between 1 and 1000/)).not.toBeInTheDocument()
  })

  it('D20: a raise never previews, never opens the lowering dialog and PUTs only the new limit', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 400 })
    renderSection()
    await typeLimit('400')
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
    // Exactly the limit: no confirmed_lowering key at all on a raise.
    expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0][0]).toEqual({ max_tool_iterations: 400 })
    expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog', { name: /Lower the limit/ })).not.toBeInTheDocument()
  })

  it('D20: a raise still sits behind the step-up gate — cancelling it writes nothing', async () => {
    renderSection()
    await typeLimit('301')
    const gate = await screen.findByRole('alertdialog', { name: 'Change the performance settings?' }, WAIT)
    fireEvent.click(within(gate).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('D20: a value just below the in-force global is a lowering and is previewed', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 299, agents: [] })
    renderSection()
    await typeLimit('299')
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(299), WAIT)
  })

  it('with no affected agents, shows no lowering dialog and saves after the step-up confirm (US-6 AS-4)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 150, agents: [] })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 150 })
    renderSection()
    await typeLimit('150')

    await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT)
    expect(screen.queryByRole('alertdialog', { name: /Lower the limit/ })).not.toBeInTheDocument()
    expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(150)
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: STEP_UP_CONFIRM }))
    await waitFor(() =>
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_tool_iterations: 150, confirmed_lowering: [] },
        undefined,
      ),
    )
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
  })

  it('lists each affected agent old → new before any password prompt or write (US-6 AS-1)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    renderSection()
    await typeLimit('200')

    const dialog = await screen.findByRole('alertdialog', { name: 'Lower the limit for 1 agent?' }, WAIT)
    const entries = within(dialog).getAllByRole('listitem')
    expect(entries).toHaveLength(1)
    expect(entries[0]).toHaveTextContent('Alpha: 250 →to 200')
    expect(screen.queryByRole('button', { name: STEP_UP_CONFIRM })).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    // Destructive-safe default: focus opens on Cancel.
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
  })

  it('Cancel in the lowering dialog writes nothing and the field returns to the saved value (US-6 AS-2)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    renderSection()
    await typeLimit('200')
    const dialog = await screen.findByRole('alertdialog', { name: /Lower the limit/ }, WAIT)

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(screen.getByLabelText(LABEL)).toHaveValue(300)
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('Confirm sends the exact confirmed snapshot, then reports the lowered agents in a 10 s toast and inline (US-6 AS-3, F4)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_tool_iterations: 200,
      max_tool_iterations_lowered_agents: [AGENT_A],
    })
    renderSection()
    await typeLimit('200')
    const dialog = await screen.findByRole('alertdialog', { name: /Lower the limit/ }, WAIT)

    fireEvent.click(within(dialog).getByRole('button', { name: 'Lower limits' }))
    // The step-up gate comes only after the lowering confirm.
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }))

    await waitFor(() =>
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_tool_iterations: 200, confirmed_lowering: [{ agent_id: 'a', old_value: 250 }] },
        undefined,
      ),
    )
    const expected = 'Lowered 1 agent: Alpha 250 → 200'
    await waitFor(() =>
      expect(addToast).toHaveBeenCalledWith({ variant: 'success', message: expected, duration: 10_000 }),
    )
    const summary = screen.getByTestId('performance-max-tool-iterations-lowered-summary')
    expect(summary).toHaveAttribute('role', 'status')
    expect(summary).toHaveTextContent(expected)
  })

  it('the inline summary stays until the admin next edits the field (F4)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_tool_iterations: 200,
      max_tool_iterations_lowered_agents: [AGENT_A],
    })
    renderSection()
    await typeLimit('200')
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }))
    await screen.findByTestId('performance-max-tool-iterations-lowered-summary', {}, WAIT)

    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '210' } })
    expect(screen.queryByTestId('performance-max-tool-iterations-lowered-summary')).not.toBeInTheDocument()
  })
})

describe(SUITE, () => {
  it('on a 409 drift, reloads the dialog with the fresh list, announces the change, focuses the list and needs a fresh Confirm (US-6 AS-6, D16)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    const fresh: MaxToolIterationsLoweringPreview = { value: 200, agents: [AGENT_A, AGENT_B] }
    const conflict: MaxToolIterationsLoweringConflict = {
      error: 'the agents to lower changed',
      code: 'max_tool_iterations_lowering_drift',
      preview: fresh,
    }
    vi.mocked(api.updatePerformanceSettings)
      .mockRejectedValueOnce(new api.MaxToolIterationsLoweringConflictError(conflict, JSON.stringify(conflict)))
      .mockResolvedValueOnce({ ...SETTINGS, max_tool_iterations: 200, max_tool_iterations_lowered_agents: [AGENT_A, AGENT_B] })
    renderSection()
    await typeLimit('200')
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }))

    const dialog = await screen.findByRole('alertdialog', { name: 'Lower the limit for 2 agents?' }, WAIT)
    const notice = within(dialog).getByRole('alert')
    expect(notice).toHaveTextContent('The list of affected agents changed — review and confirm again.')
    const entries = within(dialog).getAllByRole('listitem')
    expect(entries.map((e) => e.textContent)).toEqual(['Alpha: 250 →to 200', 'Beta: 220 →to 200'])
    await waitFor(() => expect(entries[0]).toHaveFocus())
    // Nothing was retried automatically: one PUT so far, no error toast.
    expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))

    fireEvent.click(within(dialog).getByRole('button', { name: 'Lower limits' }))
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }))
    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(2))
    expect(vi.mocked(api.updatePerformanceSettings).mock.calls[1][0]).toEqual({
      max_tool_iterations: 200,
      confirmed_lowering: [
        { agent_id: 'a', old_value: 250 },
        { agent_id: 'b', old_value: 220 },
      ],
    })
  })

  it('a preview failure saves nothing and says why, inline (spec UI states)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockRejectedValue(new api.ApiError(503, 'Service unavailable'))
    renderSection()
    await typeLimit('200')
    const msg = await screen.findByText(/Could not check which agents this would affect, so nothing was saved/, {}, WAIT)
    expect(msg).toHaveAttribute('role', 'alert')
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('cancelling the step-up prompt writes nothing and restores the field (US-1 AS-4, D8)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 150, agents: [] })
    renderSection()
    await typeLimit('150')
    const gate = await screen.findByRole('alertdialog', { name: 'Change the performance settings?' }, WAIT)
    fireEvent.click(within(gate).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('a lowering failure shows the server message inline (US-6 AS-7)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 150, agents: [] })
    const text = 'limit not changed; could not restore Alpha (now 200, was 250) — set their limits again on each agent’s profile'
    vi.mocked(api.updatePerformanceSettings).mockRejectedValue(
      new api.ApiError(500, text, { code: 'max_tool_iterations_rollback_incomplete' }),
    )
    renderSection()
    await typeLimit('150')
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
    const inline = await screen.findByText(text, { selector: '[role="alert"]' }, WAIT)
    expect(inline).toBeInTheDocument()
  })

  it.each([
    ['above_max', 5000, 1000, 'The limit saved in config.json (5000) is above 1000, so Omnipus is using 1000 instead.'],
    ['below_min', -3, 200, 'The limit saved in config.json (-3) is below 1, so Omnipus is using 200 instead.'],
    ['missing', undefined, 200, 'The limit is missing from config.json, so Omnipus is using 200.'],
  ] as const)('warns when the saved value is %s, naming the saved and in-force values (D13, D17)', async (state, raw, inForce, text) => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_tool_iterations: inForce,
      max_tool_iterations_saved_state: state,
      ...(raw === undefined ? {} : { max_tool_iterations_saved_raw: raw }),
    })
    renderSection()
    const warning = await screen.findByTestId('performance-max-tool-iterations-saved-warning')
    expect(warning).toHaveTextContent(text)
    expect(screen.getByLabelText(LABEL)).toHaveValue(inForce)
  })

  it('with a saved value out of range, re-saving the in-force number still saves (D13 repair)', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_tool_iterations: 1000,
      max_tool_iterations_saved_state: 'above_max',
      max_tool_iterations_saved_raw: 5000,
    })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 1000 })
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(1000))
    // React only fires onChange on a real value change: clear, then retype.
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '1000' } })
    // Same value as in force = not a lowering (D20): no preview, straight to the gate.
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
    await waitFor(() =>
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith({ max_tool_iterations: 1000 }, undefined),
    )
    expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
  })

  it('a stage-reload failure after a committed save says "Saved, but not applied yet", clears the edit, names the lowered agents from the error body and re-reads GET', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    vi.mocked(api.updatePerformanceSettings).mockRejectedValue(
      new api.PerformanceReloadFailedError('agent registry reload failed', '{}', null, { stage: 'reload', loweredAgents: [AGENT_A] }),
    )
    renderSection()
    await typeLimit('200')
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    // The re-read after the failure returns what is now on disk; GET carries
    // no lowered-agents list — the summary comes from the error body.
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 200 })
    fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }))

    await waitFor(() =>
      expect(addToast).toHaveBeenCalledWith(expect.objectContaining({
        variant: 'warning',
        message: 'Saved, but not applied yet: agent registry reload failed',
      })),
    )
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
    const summary = await screen.findByTestId('performance-max-tool-iterations-lowered-summary', {}, WAIT)
    expect(summary).toHaveAttribute('role', 'status')
    expect(summary).toHaveTextContent('Lowered 1 agent: Alpha 250 → 200')
    await waitFor(() => expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(2))
    // Not dirty any more: the field shows the saved value, no Save escape hatch, no inline error.
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(200))
    expect(screen.queryByTestId('performance-max-tool-iterations-save-btn')).not.toBeInTheDocument()
    expect(screen.queryByText(/Failed to save the tool-call limit/)).not.toBeInTheDocument()
  })
})

describe(SUITE, () => {
  describe('a stage-refresh reload failure (config.json written, in-memory config NOT swapped)', () => {
    const REFRESH_TEXT = 'performance settings saved but the reload failed; the new tool-iteration limit applies after the next reload or restart'

    // Lower 300 → 200 (no agents affected); the PUT answers performance_reload_failed
    // stage refresh, and every GET afterwards still reports the OLD in-memory 300
    // plus the server's pending-apply state (#904 round 3, PerformancePendingApply).
    async function refreshFailure(extra: Partial<api.PerformanceReloadFailure> = {}) {
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValueOnce(SETTINGS)
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
        ...SETTINGS,
        pending_apply: { stage: 'refresh', changed_fields: ['max_tool_iterations'] },
      })
      vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [] })
      vi.mocked(api.updatePerformanceSettings).mockRejectedValueOnce(
        new api.PerformanceReloadFailedError(REFRESH_TEXT, '{}', null, { stage: 'refresh', changedFields: ['max_tool_iterations'], ...extra }),
      )
      const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
      render(
        <QueryClientProvider client={client}>
          <PerformanceSection />
        </QueryClientProvider>,
      )
      await typeLimit('200')
      fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
      await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
      await waitFor(() => expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(2))
      return client
    }

    it('keeps the saved value on show, not the stale GET value, with a lasting "saved to the settings file" notice', async () => {
      await refreshFailure()
      const notice = await screen.findByTestId('performance-unapplied-notice', {}, WAIT)
      expect(notice).toHaveAttribute('role', 'status')
      expect(notice).toHaveTextContent(
        `Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: ${REFRESH_TEXT}`,
      )
      expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'warning' }))
      expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
      // GET said 300; the field keeps the saved 200 and is not dirty.
      await new Promise((r) => setTimeout(r, 50))
      expect(screen.getByLabelText(LABEL)).toHaveValue(200)
      expect(screen.queryByTestId('performance-max-tool-iterations-save-btn')).not.toBeInTheDocument()
    })

    // #904 gate round 3 (code-reviewer): since a44d21353 a new global limit is
    // checked against the value SAVED in config.json, while an agent's own
    // limit is still checked against the running global. The notice says so
    // and never claims the new change is checked against what is running.
    it('the notice says a new tool-call limit is checked against the saved value, and agent profiles against the running one', async () => {
      await refreshFailure()
      const notice = await screen.findByTestId('performance-unapplied-notice', {}, WAIT)
      expect(notice).toHaveTextContent(
        'The fields below show the saved values. Until the restart or reload, Omnipus keeps running on the previous ones. A new tool-call limit set here is checked against the saved limit; an agent\u2019s own limit, set on its profile, is still checked against the running one.',
      )
      expect(notice).not.toHaveTextContent(/new change here is checked against what is running/)
    })

    it('the next change is not judged against the stale in-memory global: the stale value is sent via the preview, not skipped as unchanged', async () => {
      await refreshFailure()
      await screen.findByTestId('performance-unapplied-notice', {}, WAIT)
      vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockClear()
      vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 300, agents: [] })
      vi.mocked(api.updatePerformanceSettings).mockResolvedValueOnce({ ...SETTINGS, max_tool_iterations: 300 })
      // The successful PUT applied everything: GET no longer reports pending_apply.
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS)
      // 300 is what GET (stale) reports, but config.json holds 200: a real change.
      fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '300' } })
      await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(300), WAIT)
      fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
      await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(2))
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[1][0]).toMatchObject({ max_tool_iterations: 300 })
      // A successful save put config.json in force: the notice goes.
      await waitFor(() => expect(screen.queryByTestId('performance-unapplied-notice')).not.toBeInTheDocument())
    })

    it('the notice goes once GET reports the saved value (the gateway reloaded)', async () => {
      const client = await refreshFailure()
      await screen.findByTestId('performance-unapplied-notice', {}, WAIT)
      // Still stale on a plain re-read: the notice stays.
      await client.invalidateQueries({ queryKey: ['performance-settings'] })
      await waitFor(() => expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(3))
      expect(screen.getByTestId('performance-unapplied-notice')).toBeInTheDocument()
      // The gateway reloaded: GET now reports the saved 200.
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 200 })
      await client.invalidateQueries({ queryKey: ['performance-settings'] })
      await waitFor(() => expect(screen.queryByTestId('performance-unapplied-notice')).not.toBeInTheDocument())
      expect(screen.getByLabelText(LABEL)).toHaveValue(200)
    })

    it('a lowered list the client could not read is shown to the user, not only logged', async () => {
      await refreshFailure({ loweredUnknown: true })
      const summary = await screen.findByTestId('performance-max-tool-iterations-lowered-summary', {}, WAIT)
      expect(summary).toHaveAttribute('role', 'status')
      expect(summary).toHaveTextContent(/may have been lowered.*reload the page to see which/)
    })
  })

  describe('cancelling the lowering dialog after a D16 drift keeps the other fields honest', () => {
    const conflict: MaxToolIterationsLoweringConflict = {
      error: 'the agents to lower changed',
      code: 'max_tool_iterations_lowering_drift',
      preview: { value: 200, agents: [AGENT_A, AGENT_B] },
    }

    // One PUT carrying both the limit and max_parallel_agents, refused as drift.
    async function driftWithParallelEdit() {
      vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
      vi.mocked(api.updatePerformanceSettings)
        .mockRejectedValueOnce(new api.MaxToolIterationsLoweringConflictError(conflict, JSON.stringify(conflict)))
        .mockResolvedValueOnce({ ...SETTINGS, max_parallel_agents: 8, effective_max_parallel_agents: 8 })
      renderSection()
      await typeLimit('200')
      fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
      await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT)
      // While the gate is open, a parallel-agents edit merges into the same PUT.
      fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
      await new Promise((r) => setTimeout(r, 700))
      fireEvent.click(screen.getByRole('button', { name: STEP_UP_CONFIRM }))
      await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0][0]).toMatchObject({ max_parallel_agents: 8, max_tool_iterations: 200 })
      const dialog = await screen.findByRole('alertdialog', { name: 'Lower the limit for 2 agents?' }, WAIT)
      fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    }

    it('re-sends the other fields without the limit change', async () => {
      await driftWithParallelEdit()
      fireEvent.click(await screen.findByRole('button', { name: STEP_UP_CONFIRM }, WAIT))
      await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(2))
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[1][0]).toEqual({ max_parallel_agents: 8, tools_on_demand: true })
      expect(screen.getByLabelText(LABEL)).toHaveValue(300)
    })

    it('declining that re-send resets the other fields visibly — no input keeps an unsaved edit', async () => {
      await driftWithParallelEdit()
      const gate = await screen.findByRole('alertdialog', { name: 'Change the performance settings?' }, WAIT)
      fireEvent.click(within(gate).getByRole('button', { name: 'Cancel' }))
      await waitFor(() => expect(screen.getByLabelText('Max parallel agents')).toHaveValue(4))
      expect(screen.getByLabelText(LABEL)).toHaveValue(300)
      expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
      expect(screen.queryByTestId('performance-save-btn')).not.toBeInTheDocument()
    })
  })

  it('never prints a literal default when an older backend omits the field (FR-004)', async () => {
    const { max_tool_iterations: _omit, ...older } = SETTINGS
    void _omit
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(older)
    renderSection()
    await waitFor(() => expect(screen.getByLabelText('Tries per goal')).toHaveValue(20))
    expect(screen.getByLabelText(LABEL)).toHaveValue(null)
  })
})
