/**
 * PerformanceSection.toolIterations.test.tsx — #904 global tool-iteration
 * limit under the Settings redesign #1519 Save bar (settings-redesign-spec
 * FR-010/FR-013/FR-016/FR-029; the #904 flow is kept by the regression
 * table — "must stay green under the Save bar" — with its own spec,
 * docs/internal/specs/tool-iteration-limit-spec.md, governing the dialog,
 * preview and result copy: founder decisions D3, D8, D11, D13, D16, D17,
 * D20; grill F4).
 *
 * The old file drove the RETIRED autosave + step-up model (typing fired
 * the preview and the per-change "Change the performance settings?"
 * confirm). FR-010/FR-016 re-sequence it: the typed value is a draft on
 * the bar; Save changes runs the lowering confirm first (preview → D11
 * dialog), then the one password prompt (FR-026), then the one PUT.
 * Replaced assertions cite the FR that replaces them:
 *   - the #904 inline bound message → Dataset D-10 row 8's exact
 *     "Enter a whole number from 1 to 1,000." (FR-013);
 *   - the "Change the performance settings?" step-up gate and its
 *     cancel-reverts-the-field oracle → the one prompt (FR-026) whose
 *     Cancel keeps every change (FR-029, D-11 row 5);
 *   - Cancel in the lowering dialog reverting the field → FR-016: Cancel
 *     stores nothing and KEEPS the changes (the draft stays on the bar);
 *   - the post-drift "re-send the other fields without the limit" /
 *     "declining resets the other fields" per-control queues → FR-016's
 *     keep-the-changes: after a drift Cancel both drafts stay on the bar
 *     and nothing is sent until the next Save;
 *   - the saved-but-not-applied notice follows the C-6 outcome and the
 *     pending-apply contract; the "checked against the saved/running
 *     value" sentence was retired as not-spec copy by part 9's committed
 *     reconciliation (PerformanceSection.pendingApply.test.tsx header) —
 *     its subject is covered by that file's pinned notice copy, so this
 *     file no longer pins the sentence.
 *
 * Kept, unchanged in substance (regression table): the D11 dialog lists
 * old → new before any prompt or write, destructive-safe focus on Cancel,
 * the PUT carrying the exact confirmed snapshot, the 10 s toast +
 * persistent role=status result summary (F4), the summary cleared on the
 * next edit, the drift 409 reload with the notice + focus + no auto
 * retry (D16), a preview failure saving nothing and saying why inline,
 * the server's rollback message inline (US-6 AS-7), the D13/D17
 * saved-value warnings, the D13 repair re-save, D20 raise properties,
 * and the lowered-agents-unknown result shown to the user, not only
 * logged.
 *
 * Harness: '@/lib/api' module-mocked at the boundary (the spec pins the
 * wire through these functions' responses, not the SPA fetch call sites);
 * the failure paths use the same PerformanceReloadFailedError seam the
 * committed PerformanceSection.pendingApply.test.tsx uses. The password
 * prompt is the shell's, supplied through SettingsShellContext as a
 * recorder (FR-026/FR-027).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within, act } from '@testing-library/react'
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
import type { SettingsShellApi } from './SettingsShellContext'
import { SettingsShellContext } from './SettingsShellContext'
import { PerformanceSection } from './PerformanceSection'

// Platform mode (identity.mode: 'platform'); the shell's prompt boundary is
// the recorder — the section asks it once per Save and rides the returned
// consent token (FR-026/FR-027).
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
const LABEL = 'Max tool calls per turn'
const DRIFT_NOTICE = 'The list of affected agents changed — review and confirm again.'
// D-10 row 8 (replaces the #904 inline message on this page, FR-013).
const BOUND_MESSAGE = 'Enter a whole number from 1 to 1,000.'
const NOTICE = 'performance-unapplied-notice'

/** A prompt recorder whose outcome the test resolves by hand. */
function manualPrompt() {
  const prompts: string[][] = []
  let resolvePrompt: (token: string | null) => void = () => {}
  const requestPassword = vi.fn(
    (labels: string[]) =>
      new Promise<string | null>((resolve) => {
        prompts.push(labels)
        resolvePrompt = resolve
      }),
  )
  return {
    prompts,
    requestPassword,
    resolveWith: (token: string | null) =>
      act(async () => {
        resolvePrompt(token)
      }),
  }
}

let activePrompt: ReturnType<typeof manualPrompt>

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

let lastShellClient: QueryClient | null = null

function renderSectionWithShell() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  lastShellClient = client
  render(
    <QueryClientProvider client={client}>
      <SettingsShellContext.Provider
        value={
          {
            announce: () => {},
            setDraftCount: () => {},
            offerRestart: () => {},
            refreshPendingRestart: () => {},
            requestPassword: activePrompt.requestPassword,
            saveBarVisible: false,
            setSaveBarVisible: () => {},
          } as SettingsShellApi
        }
      >
        <PerformanceSection />
      </SettingsShellContext.Provider>
    </QueryClientProvider>,
  )
  return client
}

/** A typed value is a draft on the bar — no preview, no prompt, no PUT (FR-010, FR-016). */
async function typeDraft(value: string) {
  await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
  fireEvent.change(screen.getByLabelText(LABEL), { target: { value } })
  expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
  expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
  expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
}

/** The row's error: the alert the field's aria-describedby names (FR-013). */
function fieldAlertText(input: HTMLElement): string {
  const ids = (input.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
  const alert = ids.map((id) => document.getElementById(id)).find((el) => el?.getAttribute('role') === 'alert')
  expect(alert, 'the field must show its error as the alert its aria-describedby names').not.toBeNull()
  return alert?.textContent ?? ''
}

beforeEach(() => {
  vi.clearAllMocks()
  // mockReset, not only clear: an unconsumed *Once value from one test must
  // never leak into the next.
  vi.mocked(api.updatePerformanceSettings).mockReset()
  vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockReset()
  vi.mocked(api.fetchPerformanceSettings).mockReset()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
  activePrompt = manualPrompt()
})

// One suite title shared by three describe blocks: full test names stay
// stable; the split only keeps each block under the function-size budget.
const SUITE = 'PerformanceSection — global max tool iterations (#904)'

describe(SUITE, () => {
  it('shows the in-force global beside the other Performance budgets (US-1 AS-1, D3)', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(300))
    // The D-10 row 8 range (1–1,000) is enforced by the screen's own
    // validation, proven by the refusal cases below — the old
    // min/max-attribute pins are not the spec's oracle (FR-013, D-10 row 8).
    expect(screen.getByLabelText('Tries per goal')).toBeInTheDocument()
    expect(screen.queryByTestId('performance-max-tool-iterations-saved-warning')).not.toBeInTheDocument()
  })

  it.each(['0', '1001', '-5', '2.5'])(
    'refuses %s inline with the D-10 bound message, with no preview and no write (US-1 AS-3, FR-013, D-10 row 8)',
    async (value) => {
      renderSection()
      await typeDraft(value)
      const input = screen.getByLabelText(LABEL)
      await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
      expect(fieldAlertText(input)).toBe(BOUND_MESSAGE)
      expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
      const saveBtn = screen.getByRole('button', { name: /save changes/i })
      expect(saveBtn).toHaveAttribute('aria-disabled', 'true')
      fireEvent.click(saveBtn)
      expect(input).toHaveFocus()
      expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
      expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
      expect(activePrompt.prompts).toHaveLength(0)
    },
  )

  it('accepts the upper bound 1000 — a raise: drafted, saved without any preview (US-1 AS-3 boundary, D20)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 1000 })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '1000' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByText(BOUND_MESSAGE)).not.toBeInTheDocument()
    expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')
    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0][0]).toEqual({ max_tool_iterations: 1000 })
  })

  it('accepts the lower bound 1 — a lowering, so Save previews it first (US-1 AS-3 boundary, D11, FR-016)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 1, agents: [] })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '1' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByText(BOUND_MESSAGE)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(1), WAIT)
  })

  it('D20: a raise never previews, never opens the lowering dialog and PUTs only the new limit (FR-010, FR-015)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 400 })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '400' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')
    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
    // Exactly the limit: no confirmed_lowering key at all on a raise.
    expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0][0]).toEqual({ max_tool_iterations: 400 })
    expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog', { name: /Lower the limit/ })).not.toBeInTheDocument()
  })

  it('D20: a raise still asks at Save — cancelling the prompt writes nothing and keeps the change (FR-026, FR-029/D-11 row 5)', async () => {
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '301' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith(null)

    // FR-029/D-11 row 5: Cancel stores nothing and keeps every change —
    // the retired step-up gate reverted the field instead.
    await waitFor(() => {
      expect(screen.getByLabelText(LABEL)).toHaveValue(301)
      expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('D20: a value just below the in-force global is a lowering and is previewed at Save (FR-016)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 299, agents: [] })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '299' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(299), WAIT)
  })

  it('with no affected agents, shows no lowering dialog and saves after the prompt (US-6 AS-4)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 150, agents: [] })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 150 })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '150' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(screen.queryByRole('alertdialog', { name: /Lower the limit/ })).not.toBeInTheDocument()
    expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(150)
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')
    await waitFor(() =>
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_tool_iterations: 150, confirmed_lowering: [] },
        'reauth_tok',
      ),
    )
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
  })

  it('lists each affected agent old → new before any password prompt or write (US-6 AS-1)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    const dialog = await screen.findByRole('alertdialog', { name: /Lower the limit/ }, WAIT)
    const entries = within(dialog).getAllByRole('listitem')
    expect(entries).toHaveLength(1)
    // The dialog names the agent and both values (old → new); the arrow
    // spacing is product copy, not the oracle.
    expect(entries[0].textContent).toMatch(/Alpha/)
    expect(entries[0].textContent).toMatch(/250/)
    expect(entries[0].textContent).toMatch(/200/)
    // FR-016: the confirm precedes the prompt — none is open yet, nothing sent.
    expect(activePrompt.prompts).toHaveLength(0)
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    // Destructive-safe default: focus opens on Cancel.
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
  })

  it('Cancel in the lowering dialog writes nothing and keeps the change (US-6 AS-2, FR-016)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    const dialog = await screen.findByRole('alertdialog', { name: /Lower the limit/ }, WAIT)

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    // FR-016: Cancel keeps the changes — the retired flow reverted the field.
    expect(screen.getByLabelText(LABEL)).toHaveValue(200)
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(activePrompt.prompts).toHaveLength(0)
  })

  it('Confirm sends the exact confirmed snapshot, then reports the lowered agents in a 10 s toast and inline (US-6 AS-3, F4)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_tool_iterations: 200,
      max_tool_iterations_lowered_agents: [AGENT_A],
    })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    const dialog = await screen.findByRole('alertdialog', { name: /Lower the limit/ }, WAIT)

    fireEvent.click(within(dialog).getByRole('button', { name: 'Lower limits' }))
    // The one prompt comes only after the lowering confirm (FR-016, FR-026).
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')

    await waitFor(() =>
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_tool_iterations: 200, confirmed_lowering: [{ agent_id: 'a', old_value: 250 }] },
        'reauth_tok',
      ),
    )
    const expected = 'Lowered 1 agent: Alpha 250 → 200'
    await waitFor(() =>
      expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ message: expected, duration: 10_000 })),
    )
    const summary = screen.getByTestId('performance-lowered-summary')
    expect(summary).toHaveAttribute('role', 'status')
    expect(summary).toHaveTextContent(expected)
  })

  it('the inline summary is cleared by the next edit of the field, and that edit drafts without writing (tool-iteration-limit-spec:272, settings spec regression table #904, FR-010)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_tool_iterations: 200,
      max_tool_iterations_lowered_agents: [AGENT_A],
    })
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')
    await screen.findByTestId('performance-lowered-summary', {}, WAIT)

    // tool-iteration-limit-spec.md:272 (F4): the inline summary is a
    // PERSISTENT element — contrast: the 10-second toast — that "stays until
    // the admin NEXT EDITS THE FIELD or leaves the page". So the next edit
    // of the field clears it; while the summary persists, a leftover line
    // would misdescribe the new draft as the earlier completed lowering.
    // The settings spec's regression table (#904 "must stay green under the
    // Save bar") keeps this result behaviour; only the summary's old
    // AUTOSAVE trigger is retired (FR-010).
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '210' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByTestId('performance-lowered-summary')).not.toBeInTheDocument()
    // Retired with the old flow is only the trigger: the edit is a draft on
    // the bar (FR-010) — no fresh preview, no second prompt, no second write
    // until the next Save changes. The ONE preview call so far is the first
    // Save's own required lowering preview (#904 US-6 AS-1, before its
    // confirm dialog); the next edit must add none.
    const previewsBefore = vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mock.calls.length
    expect(previewsBefore).toBe(1)
    expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledTimes(previewsBefore)
    expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
    expect(activePrompt.prompts).toHaveLength(1)
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
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')

    // The reloaded drift dialog, found by its list-changed marker (probe2:
    // the dialog carries no accessible name to match on).
    await screen.findByTestId('max-tool-iterations-list-changed', {}, WAIT)
    const dialog = screen.getByRole('alertdialog')
    const notice = within(dialog).getByRole('alert')
    expect(notice).toHaveTextContent(DRIFT_NOTICE)
    const entries = within(dialog).getAllByRole('listitem')
    expect(entries).toHaveLength(2)
    expect(entries[0].textContent).toMatch(/Alpha/)
    expect(entries[1].textContent).toMatch(/Beta/)
    await waitFor(() => expect(entries[0]).toHaveFocus())
    // Nothing was retried automatically: one PUT so far, no error toast.
    expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))

    fireEvent.click(within(dialog).getByRole('button', { name: 'Lower limits' }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(2))
    await activePrompt.resolveWith('reauth_tok')
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
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // PRODUCTION-SIDE (kept RED on the FE checkout, reported to team-lead):
    // the spec keeps an inline error here — tool-iteration-limit-spec.md
    // line 273 "Preview fetch failure → inline error" — but the FE shows
    // only an error toast naming the server message and no inline alert.
    // The oracle stays the spec, not the FE.
    const msg = await screen.findByText(/Could not check which agents this would affect, so nothing was saved/, {}, WAIT)
    expect(msg).toHaveAttribute('role', 'alert')
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(activePrompt.prompts).toHaveLength(0)
  })

  it('a lowering failure shows the server message inline (US-6 AS-7)', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 150, agents: [] })
    const text = 'limit not changed; could not restore Alpha (now 200, was 250) — set their limits again on each agent’s profile'
    vi.mocked(api.updatePerformanceSettings).mockRejectedValue(
      new api.ApiError(500, text, { code: 'max_tool_iterations_rollback_incomplete' }),
    )
    // FR-020 outcome 4 (spec :1924): a 500 without a recognized signal is
    // UNKNOWN, so the screen runs the C-6 saved-state check (spec :246).
    // Its first read is GET /config/pending-restart on the raw fetch edge
    // (saveOutcome.ts::readPendingRestartFresh). An empty list leaves the
    // key unlisted, so the saved value is the running read — the pre-Save
    // 300 served by the mocked section read — and the check concludes the
    // change was NOT saved. #904 (:105) keeps the server's own error text
    // inline for this code.
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/config/pending-restart')) {
        return { ok: true, status: 200, json: async () => [] }
      }
      throw new Error(`toolIterations: unexpected fetch ${url}`)
    }))
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '150' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')
    // FR-020 outcome 4 → the C-6 check concludes NOT SAVED (the running read
    // still holds the pre-Save 300); the check's not-saved line is the
    // hook's wording for the outcome (exact text not drawn in the wireframe,
    // spec :651) and the bar counts the refused change (spec :484).
    expect(await screen.findByText('These changes were not saved. Save them again.', {}, WAIT)).toBeInTheDocument()
    expect(screen.getByText(/1 change could not be saved/)).toBeInTheDocument()
    // #904 line 368: the 500 codes are shown as their `error` text — the
    // server's own rollback message inline (FR-020 outcome 3's
    // message-under-the-field presentation for a not-saved outcome).
    const inline = await screen.findByText(text, { selector: '[role="alert"]' }, WAIT)
    expect(inline).toBeInTheDocument()
    // FR-020 outcome 3's presentation: the refused draft is kept.
    expect(screen.getByLabelText(LABEL)).toHaveValue(150)
    vi.unstubAllGlobals()
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
    // No saved-warning testid exists on the FE checkout (probe2/d13): the
    // warning is a role=status element carrying the sentence (followed by
    // the FE's added "The file has not been changed; saving a value here
    // replaces it."), so the oracle matches the sentence itself and pins
    // the status carrier around it.
    const warning = await screen.findByText(text, { exact: false }, WAIT)
    expect(warning.closest('[role="status"]') ?? warning).toHaveAttribute('role', 'status')
    expect(screen.getByLabelText(LABEL)).toHaveValue(inForce)
  })

  it('with a saved value out of range, the in-force number is repairable through the bar: typing it back drafts nothing (FR-011), a different valid value saves through the lowering confirm (D13)', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_tool_iterations: 1000,
      max_tool_iterations_saved_state: 'above_max',
      max_tool_iterations_saved_raw: 5000,
    })
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 900 })
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 900, agents: [AGENT_A] })
    renderSectionWithShell()
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(1000))
    // React only fires onChange on a real value change: clear, then retype.
    // FR-011: a value typed back to the saved one is NOT a change — no
    // draft, no bar, so the retired "re-save the identical number" path no
    // longer exists.
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '1000' } })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    // The repair a user can actually make: a different valid value. The
    // in-force 1000 IS the D-10 cap, so every valid change here is a
    // lowering — 900 goes through the lowering flow (preview → confirm →
    // prompt → PUT), not a shortcut.
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '900' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(api.fetchMaxToolIterationsLoweringPreview).toHaveBeenCalledWith(900), WAIT)
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')
    await waitFor(() =>
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_tool_iterations: 900, confirmed_lowering: [{ agent_id: 'a', old_value: 250 }] },
        'reauth_tok',
      ),
    )
  })

  it('a stage-reload failure (config.json written, in-memory swapped) still reports the lowered agents — F4 toast + inline summary, no error, field shows the saved value', async () => {
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    vi.mocked(api.updatePerformanceSettings).mockRejectedValue(
      new api.PerformanceReloadFailedError('agent registry reload failed', '{}', null, { stage: 'reload', loweredAgents: [AGENT_A] }),
    )
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    // The re-read after the failure returns what is now on disk (stage
    // reload: the running config WAS swapped); GET carries no lowered-agents
    // list — the summary comes from the failure outcome.
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 200 })
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')

    // The spec replaces the retired warning-toast oracle: stage reload means
    // the running config WAS swapped, so no pending-apply notice is due
    // (FR-020 outcome 1 is driven by GET's pending_apply — the refresh
    // stage, pinned by PerformanceSection.pendingApply.test); the lowering
    // outcome keeps tool-iteration-limit-spec.md F4's presentation
    // (probe2 evidence: success toast naming the agents + persistent
    // summary, no warning, no error).
    await waitFor(() =>
      expect(addToast).toHaveBeenCalledWith(expect.objectContaining({
        variant: 'success',
        message: 'Lowered 1 agent: Alpha 250 → 200',
        duration: 10_000,
      })),
    )
    expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
    const summary = await screen.findByTestId('performance-lowered-summary', {}, WAIT)
    expect(summary).toHaveAttribute('role', 'status')
    expect(summary).toHaveTextContent('Lowered 1 agent: Alpha 250 → 200')
    // Not dirty any more: the field shows the saved value, the bar is gone,
    // no inline save error (the per-control save button of the retired flow
    // no longer exists to check for — FR-010 put saving on the bar alone).
    await waitFor(() => expect(screen.getByLabelText(LABEL)).toHaveValue(200))
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Failed to save the tool-call limit/)).not.toBeInTheDocument()
  })
})

describe(SUITE, () => {
  describe('a stage-refresh reload failure (config.json written, in-memory config NOT swapped)', () => {
    // Lower 300 → 200 (no agents affected); the save is committed but the
    // running config still answers the OLD 300, and GET reports the server's
    // pending-apply state (#904 round 3, PerformancePendingApply). The
    // failure seam is the same one the committed
    // PerformanceSection.pendingApply.test.tsx drives.
    async function refreshFailure(extra: Partial<api.PerformanceReloadFailure> = {}) {
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValueOnce(SETTINGS)
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
        ...SETTINGS,
        pending_apply: { stage: 'refresh', changed_fields: ['max_tool_iterations'] },
      })
      vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [] })
      vi.mocked(api.updatePerformanceSettings).mockRejectedValueOnce(
        new api.PerformanceReloadFailedError(
          'performance settings saved but the reload failed',
          '{}',
          null,
          { stage: 'refresh', changedFields: ['max_tool_iterations'], ...extra },
        ),
      )
      renderSectionWithShell()
      await waitFor(() => screen.getByLabelText(LABEL))
      fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
      expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
      await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
      await activePrompt.resolveWith('reauth_tok')
      await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
      await waitFor(() => expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(2))
      return lastShellClient
    }

    it('keeps the saved value on show, not the stale GET value, with a lasting saved-not-applied notice (FR-020 outcome 1)', async () => {
      await refreshFailure()
      const notice = await screen.findByTestId(NOTICE, {}, WAIT)
      expect(notice).toHaveAttribute('role', 'status')
      // The refresh-stage notice copy is pinned by the committed
      // PerformanceSection.pendingApply.test.tsx; this file pins the state,
      // the role and the field, not a second copy of the sentence.
      expect(notice).toHaveTextContent('Saved, but not applied yet')
      // The retired model's warning toast is gone (FE evidence: the refresh
      // stage shows the notice only); keep the no-error-toast negative.
      expect(addToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
      // GET said 300; the field keeps the saved 200 and is not dirty.
      await new Promise((r) => setTimeout(r, 50))
      expect(screen.getByLabelText(LABEL)).toHaveValue(200)
      expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    })

    it('the next change is not skipped as unchanged against the stale in-memory global — the drafted value is PUT even when it equals the stale GET value (FR-020, C-6 saved-state rule)', async () => {
      await refreshFailure()
      await screen.findByTestId(NOTICE, {}, WAIT)
      vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockClear()
      vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 300, agents: [] })
      vi.mocked(api.updatePerformanceSettings).mockResolvedValueOnce({ ...SETTINGS, max_tool_iterations: 300 })
      // The successful PUT applied everything: GET no longer reports pending_apply.
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS)
      // 300 is what GET (stale) reports, but config.json holds 200: a real
      // change. Against the in-force 300 it is not a lowering (D20), so the
      // preview must not run — the draft goes straight to the prompt; the
      // PUT (not a skip-as-unchanged) is the property under test.
      fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '300' } })
      expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
      expect(api.fetchMaxToolIterationsLoweringPreview).not.toHaveBeenCalled()
      fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
      await waitFor(() => expect(activePrompt.prompts.length).toBe(2))
      await activePrompt.resolveWith('reauth_tok')
      await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(2))
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[1][0]).toMatchObject({ max_tool_iterations: 300 })
      // A successful save put config.json in force: the notice goes.
      await waitFor(() => expect(screen.queryByTestId(NOTICE)).not.toBeInTheDocument())
    })

    it('the notice goes once GET reports the saved value (the gateway reloaded)', async () => {
      const client = (await refreshFailure()) as QueryClient
      await screen.findByTestId(NOTICE, {}, WAIT)
      // Still stale on a plain re-read: the notice stays.
      await act(async () => {
        await client.invalidateQueries({ queryKey: ['performance-settings'] })
      })
      await waitFor(() => expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(3))
      expect(screen.getByTestId(NOTICE)).toBeInTheDocument()
      // The gateway reloaded: GET now reports the saved 200.
      vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({ ...SETTINGS, max_tool_iterations: 200 })
      await act(async () => {
        await client.invalidateQueries({ queryKey: ['performance-settings'] })
      })
      await waitFor(() => expect(screen.queryByTestId(NOTICE)).not.toBeInTheDocument())
      expect(screen.getByLabelText(LABEL)).toHaveValue(200)
    })

    it('a lowered list the client could not read is shown to the user, not only logged', async () => {
      await refreshFailure({ loweredUnknown: true })
      // PRODUCTION-SIDE (kept RED on the FE checkout, reported to
      // team-lead): the #904 result copy requires an unreadable lowered
      // list to be surfaced — "may have been lowered … reload the page to
      // see which" — but in this refresh-stage case the FE shows only the
      // generic pending-apply notice and no lowered-agents information
      // anywhere (r2 DOM evidence). The oracle stays the spec.
      const summary = await screen.findByTestId('performance-lowered-summary', {}, WAIT)
      expect(summary).toHaveAttribute('role', 'status')
      expect(summary).toHaveTextContent(/may have been lowered.*reload the page to see which/)
    })

  })

  it('after a D16 drift, Cancel keeps both drafts on the bar and nothing is sent (FR-016; the retired re-send/reset queues are gone)', async () => {
    const conflict: MaxToolIterationsLoweringConflict = {
      error: 'the agents to lower changed',
      code: 'max_tool_iterations_lowering_drift',
      preview: { value: 200, agents: [AGENT_A, AGENT_B] },
    }
    vi.mocked(api.fetchMaxToolIterationsLoweringPreview).mockResolvedValue({ value: 200, agents: [AGENT_A] })
    vi.mocked(api.updatePerformanceSettings)
      .mockRejectedValueOnce(new api.MaxToolIterationsLoweringConflictError(conflict, JSON.stringify(conflict)))
    renderSectionWithShell()
    await waitFor(() => screen.getByLabelText(LABEL))
    // Draft both the lowering and an unrelated max-parallel-agents change.
    fireEvent.change(screen.getByLabelText(LABEL), { target: { value: '200' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    expect(await screen.findByText(/2 unsaved changes/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    fireEvent.click(within(await screen.findByRole('alertdialog', { name: /Lower/ }, WAIT)).getByRole('button', { name: 'Lower limits' }))
    await waitFor(() => expect(activePrompt.prompts.length).toBe(1))
    await activePrompt.resolveWith('reauth_tok')
    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))

    // The drift reloads the dialog; Cancel answers it (found by its
    // list-changed marker — the dialog carries no accessible name).
    await screen.findByTestId('max-tool-iterations-list-changed', {}, WAIT)
    const dialog = screen.getByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    // FR-016/FR-020 outcome 3: Cancel stores nothing and keeps the changes —
    // BOTH drafts stay on the bar, which now reads "2 changes could not be
    // saved" with the drift reason shown under the setting (probe2
    // presentation; FR-029/D-11 row 5: Cancel keeps every change).
    expect(screen.getByText(/2 changes could not be saved/)).toBeInTheDocument()
    expect(screen.getByText('The agents this lowers changed since you confirmed. Check the new list, then save again.')).toBeInTheDocument()
    expect(screen.getByLabelText(LABEL)).toHaveValue(200)
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue('8')
    expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
    expect(activePrompt.prompts).toHaveLength(1)
  })

  it('never prints a literal default when an older backend omits the field (forward compatibility)', async () => {
    const { max_tool_iterations: _omit, ...older } = SETTINGS
    void _omit
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(older)
    renderSection()
    await waitFor(() => expect(screen.getByLabelText('Tries per goal')).toHaveValue(20))
    expect((screen.getByLabelText(LABEL) as HTMLInputElement).value).toBe('')
  })
})
