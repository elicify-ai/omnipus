/**
 * PerformanceSection.goal.test.tsx — the goal round budget under the
 * Settings redesign #1519 (GOAL-FR-045 substance; D-D/D-E history).
 *
 * Covers the single global goal-round-budget control in Limits →
 * "Agents and goals" (`goal_max_rounds` on PerformanceSettings /
 * PerformanceSettingsUpdate). Per the operator's 2026-09-11 decision (D-E),
 * GOAL-FR-046's per-goal override control is RETIRED — this is the ONE
 * budget control in the product, and it governs task goals and chat goals
 * identically. No per-goal override surface exists anywhere.
 *
 * The old file drove the RETIRED autosave model (700 ms debounce + the
 * per-change "Change the performance settings?" confirm + error toasts).
 * FR-010 replaces all of it; every replaced assertion cites the FR that
 * replaces it:
 *   - debounce + confirm → draft on the bar, one Save changes sending a
 *     partial PUT with only the changed fields and the consent token
 *     (FR-010, FR-011, FR-015, FR-026, FR-027);
 *   - the "must be a whole number of at least 1." error TOAST → the D-10
 *     row 5 inline field error that blocks the bar (FR-013);
 *   - cancelling the confirm → Discard (FR-012);
 *   - the finding-15 debounce race → its modern statement under the Save
 *     bar: two drafted changes are saved by ONE Save as one partial PUT
 *     carrying both, and the prompt lists both rows (FR-015, FR-026).
 *
 * Kept, unchanged in substance: the configured value renders, the
 * documented default (20) applies when an older backend omits the field,
 * the plain-language copy rules, the floor value 1 accepted, the
 * no-bundling and no-revert properties, and D-E's "exactly one budget
 * control on the screen" structural oracle (updated for the D-10 row 9
 * input types: max parallel agents stays a text input because empty means
 * no fixed limit; the four ranged values are number inputs).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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
    fetchAppState: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import type { SettingsShellApi } from './SettingsShellContext'
import { SettingsShellContext } from './SettingsShellContext'
import { PerformanceSection } from './PerformanceSection'

// Platform mode (identity.mode: 'platform'); the shell's prompt boundary is
// the recorder below — the section asks it once per Save and rides the
// returned consent token (FR-026/FR-027).
const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

const SETTINGS = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
  goal_max_rounds: 20,
}

function promptRecorder() {
  const prompts: string[][] = []
  const requestPassword = vi.fn(async (labels: string[]) => {
    prompts.push(labels)
    return 'reauth-tok'
  })
  return { prompts, requestPassword }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

function renderSectionWithShell(requestPassword: (labels: string[]) => Promise<string | null>) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SettingsShellContext.Provider
        value={
          {
            announce: () => {},
            setDraftCount: () => {},
            offerRestart: () => {},
            refreshPendingRestart: () => {},
            requestPassword,
            saveBarVisible: false,
            setSaveBarVisible: () => {},
          } as SettingsShellApi
        }
      >
        <PerformanceSection />
      </SettingsShellContext.Provider>
    </QueryClientProvider>,
  )
}

/** The row's error: the alert the field's aria-describedby names (FR-013). */
function fieldAlertText(input: HTMLElement): string {
  const ids = (input.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
  const alert = ids.map((id) => document.getElementById(id)).find((el) => el?.getAttribute('role') === 'alert')
  expect(alert, 'the field must show its error as the alert its aria-describedby names').not.toBeNull()
  return alert?.textContent ?? ''
}

// D-10 row 5: "Tries per goal 0 | Min − 1 | Rejected: 'Enter a whole number
// of at least 1.' | no upper bound."
const TRIES_MESSAGE = 'Enter a whole number of at least 1.'

// The four ranged values are number inputs; max parallel agents is the text
// input because empty means no fixed limit (D-10 row 9).
const KNOWN_NUMBER_FIELDS = [
  'Tries per goal',
  'Idle reminder quiet window',
  'Check-in interval',
  'Max tool calls per turn',
]

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — goal round budget (GOAL-FR-045 substance)', () => {
  it('renders the configured global goal_max_rounds value from the API', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(20)
    })
  })

  it('falls back to the documented default (20) when an older backend omits goal_max_rounds', async () => {
    // SETTINGS with goal_max_rounds intentionally omitted.
    const withoutGoal: Partial<typeof SETTINGS> = { ...SETTINGS }
    delete withoutGoal.goal_max_rounds
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(withoutGoal as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(20)
    })
  })

  it('the budget control carries a plain, non-engineer description of what it bounds, and no jargon leaks (FR-058 row-description layout)', async () => {
    // The redesign replaces the old card (its "Goal completion budget"
    // heading and the "applies to goals in chat and on tasks" copy) with the
    // Limits group's per-field row description — the same pattern the other
    // group rows use (compare Tool loading's row description in
    // PerformanceSection.test.tsx). The D-E substance is kept in that
    // description: the control bounds the rounds an agent gets to finish
    // one goal, stated in plain words. Characterization of the redesign's
    // accepted copy — the spec pins no wording here (the wireframe is the
    // wording authority), so this pins the plain-language property, not a
    // spec sentence.
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    const input = screen.getByLabelText('Tries per goal')
    const ids = (input.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
    const description = ids.map((id) => document.getElementById(id)?.textContent ?? '').join(' ')
    expect(description).toMatch(/how many rounds an agent gets to finish one goal/i)
    // No internal jargon leaks into the user-facing copy.
    expect(document.body.textContent).not.toMatch(/adjudication|owner loop|verifiers|sentinel/i)
  })

  it('S-34 adapted: a drafted raise waits on the bar, and one Save sends ONLY goal_max_rounds with the token (FR-010, FR-015, FR-026, FR-027)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, goal_max_rounds: 30 } as never)
    const { prompts, requestPassword } = promptRecorder()

    renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    // FR-010: typing is a draft — nothing is sent, no dialog opens.
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '30' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    // One Save changes: one prompt naming the row, one partial PUT.
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Tries per goal'])
    await waitFor(() => {
      // Load-bearing: the body carries ONLY goal_max_rounds. If the max
      // parallel/tools_on_demand fields were bundled in here it would mean
      // this control secretly writes the other two settings too, which
      // would make an unrelated edit on this screen able to silently revert
      // whatever an operator has open in another tab (FR-015: partial
      // update, only changed fields).
      expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith({ goal_max_rounds: 30 }, 'reauth-tok')
    })
    // FR-018: the saved signal replaces the retired confirm machinery.
    await screen.findByText(/Saved at/)
  })

  it('S-35 adapted: entering 0 is refused inline with the D-10 message and no write occurs (FR-013, D-10 row 5)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))
    const input = screen.getByLabelText('Tries per goal') as HTMLInputElement

    fireEvent.change(input, { target: { value: '0' } })
    await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(input)).toBe(TRIES_MESSAGE)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    expect(saveBtn).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(saveBtn)
    expect(input).toHaveFocus()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    // The old model toasted this refusal; FR-013 replaces the toast with the
    // inline field error and the blocked bar.
    expect(addToast).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('rejects a negative value the same way (FR-013, D-10 row 5)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))
    const input = screen.getByLabelText('Tries per goal') as HTMLInputElement

    fireEvent.change(input, { target: { value: '-5' } })
    await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(input)).toBe(TRIES_MESSAGE)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(addToast).not.toHaveBeenCalled()
  })

  it('rejects a blank value (FR-013, D-10 row 5)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))
    const input = screen.getByLabelText('Tries per goal') as HTMLInputElement

    fireEvent.change(input, { target: { value: '' } })
    await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(input)).toBe(TRIES_MESSAGE)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(addToast).not.toHaveBeenCalled()
  })

  it('accepts exactly 1 (the floor) through the Save bar (FR-010, FR-015)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, goal_max_rounds: 1 } as never)
    const { requestPassword } = promptRecorder()

    renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '1' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => {
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith({ goal_max_rounds: 1 }, 'reauth-tok')
    })
  })

  it('editing the goal budget does NOT bundle or revert max_parallel_agents/tools_on_demand — the body carries the budget alone (FR-015)', async () => {
    // Regression guard for the design this file pins: the budget Save sends
    // ONLY goal_max_rounds. If a future edit accidentally routed the goal
    // input through a whole-section body, this assertion catches it because
    // the payload would then also carry those two fields.
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, goal_max_rounds: 45 } as never)
    const { requestPassword } = promptRecorder()

    renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '45' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
    const call = vi.mocked(api.updatePerformanceSettings).mock.calls[0]
    expect(call[0]).toEqual({ goal_max_rounds: 45 })
    expect(call[0]).not.toHaveProperty('max_parallel_agents')
    expect(call[0]).not.toHaveProperty('tools_on_demand')

    // The max-parallel-agents input must still show its own, untouched value.
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue('4')
  })

  it('Discard reverts the drafted budget to the saved value and does not save (FR-012; FR-010 retires the cancelled confirmation)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '99' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.getByLabelText('Tries per goal')).toHaveValue(99)

    fireEvent.click(screen.getByRole('button', { name: /discard/i }))

    await waitFor(() => {
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(20)
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  // ── Review finding 15, restated for the Save bar ─────────────────────────
  //
  // The old finding: two 600 ms debounces wrote one pending slot, so the
  // later edit silently REPLACED the earlier one while the screen said
  // "saved". The debounce window is retired (FR-010); its property survives
  // as: two drafted changes are saved by ONE Save changes as one partial PUT
  // carrying BOTH intents (FR-015), and the prompt lists both rows (FR-026)
  // — no drafted value is dropped or replaced.
  it('finding 15: drafting BOTH controls and saving once sends BOTH values — neither write discards the other (FR-015, FR-026)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_parallel_agents: 9,
      effective_max_parallel_agents: 9,
      goal_max_rounds: 30,
    } as never)
    const { prompts, requestPassword } = promptRecorder()

    renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    // Draft both: max parallel agents first, then the goal budget.
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '9' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '30' } })
    expect(await screen.findByText(/2 unsaved changes/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    // FR-026: the prompt lists every password-marked setting of this Save —
    // both rows, in the section's order (order itself is not pinned).
    expect(prompts[0]).toHaveLength(2)
    expect([...prompts[0]].sort()).toEqual(['Max parallel agents', 'Tries per goal'])

    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1))
    // Exactly one PUT, carrying BOTH intents. The old behaviour sent
    // `{ goal_max_rounds: 30 }` alone and lost the 9 with no indication.
    const body = vi.mocked(api.updatePerformanceSettings).mock.calls[0][0] as Record<string, unknown>
    expect(body.max_parallel_agents).toBe(9)
    expect(body.goal_max_rounds).toBe(30)
    expect(Object.keys(body).sort()).toEqual(['goal_max_rounds', 'max_parallel_agents'])
  })

  it('finding 15: neither input silently reverts — both still show what was typed after the save', async () => {
    // The server fixture is advanced to what the PUT actually persisted, so
    // a revert here can only come from the component dropping an edit, not
    // from a stale mock.
    let serverState: Record<string, unknown> = { ...SETTINGS }
    vi.mocked(api.fetchPerformanceSettings).mockImplementation(
      async () => ({ ...serverState }) as never,
    )
    vi.mocked(api.updatePerformanceSettings).mockImplementation(async (body) => {
      serverState = { ...serverState, ...(body as Record<string, unknown>) }
      if (typeof serverState.max_parallel_agents === 'number') {
        serverState.effective_max_parallel_agents = serverState.max_parallel_agents
      }
      return { ...serverState } as never
    })
    const { requestPassword } = promptRecorder()

    renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '9' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '30' } })
    expect(await screen.findByText(/2 unsaved changes/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalled())
    await waitFor(() => {
      expect(serverState.max_parallel_agents).toBe(9)
      expect(serverState.goal_max_rounds).toBe(30)
    })
    // The saved values stay on show — a revert here is a dropped edit.
    await waitFor(() => {
      expect(screen.getByLabelText('Max parallel agents')).toHaveValue('9')
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(30)
    })
  })

  it('exactly one goal-round-budget control exists on the whole screen — no per-goal override surface (D-E)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    // Exactly one "Tries per goal" control on the whole screen — not one
    // per goal, not a second field for an "override". The preceding test
    // asserts the card's own copy explains this control applies to goals in
    // chat and on tasks, so the negative here is structural: a single input,
    // not a list or a per-item field.
    expect(screen.getAllByLabelText('Tries per goal')).toHaveLength(1)
    // Exactly one goal-shaped number field anywhere (no per-goal override).
    expect(screen.getAllByRole('spinbutton', { name: /goal|round/i })).toHaveLength(1)
    // Every numeric field on the screen is one of the known global controls
    // — the four D-10-ranged number inputs plus the max-parallel-agents text
    // input (empty means no fixed limit, D-10 row 9) — so an unnamed extra
    // budget field fails here instead of being absorbed by a bare count.
    const numbers = screen.getAllByRole('spinbutton')
    expect(numbers).toHaveLength(KNOWN_NUMBER_FIELDS.length)
    for (const name of KNOWN_NUMBER_FIELDS) {
      expect(screen.getByRole('spinbutton', { name })).toBeInTheDocument()
    }
    const textNumeric = screen.getByLabelText('Max parallel agents')
    expect(textNumeric.tagName.toLowerCase()).toBe('input')
  })
})
