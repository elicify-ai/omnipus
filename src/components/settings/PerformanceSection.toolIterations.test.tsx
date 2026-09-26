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

describe('PerformanceSection — global max tool iterations (#904)', () => {
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

  it('accepts both bounds 1 and 1000 (US-1 AS-3 boundary)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 1000, agents: [] })
    renderSection()
    await typeLimit('1000')
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(1000), WAIT)
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '1' } })
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(1), WAIT)
    expect(screen.queryByText(/must be between 1 and 1000/)).not.toBeInTheDocument()
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
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 1000, agents: [] })
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(1000))
    // React only fires onChange on a real value change: clear, then retype.
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '1000' } })
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(1000), WAIT)
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
