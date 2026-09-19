/**
 * PerformanceSection.goal.test.tsx — GOAL-FR-045, D-D/D-E.
 *
 * Covers the single global goal-round-budget control under Settings →
 * Performance (`goal_max_rounds` on PerformanceSettings/PerformanceSettingsUpdate,
 * contracts/components/schemas/PerformanceSettings.yaml). Per the operator's
 * 2026-09-11 decision (D-E), GOAL-FR-046's per-goal override control is
 * RETIRED — this is the ONE budget control in the product, and it governs
 * task goals and chat goals identically. No per-goal override surface exists
 * anywhere (not here, not on the task detail panel — see
 * src/components/workspaces/TaskDetailPanel.tsx — GoalBudgetField.tsx is
 * never created by anyone).
 *
 * Adapted from the goal spec's S-34 ("an operator raises a budget in the
 * interface") and S-35 ("a budget below one is refused") scenarios, which
 * were written against a per-goal override that D-E retired — the behaviour
 * they describe (raise the budget in the interface; refuse a sub-1 value, no
 * write occurs) now applies to this global control instead.
 *
 * This suite is a separate file from PerformanceSection.test.tsx (mirroring
 * that file's own note about PerformanceSection.autoDefault.test.tsx) because
 * the shared SETTINGS fixture there does not touch goal_max_rounds at all,
 * and mixing the two is how a "no per-goal override anywhere" assertion
 * quietly stops being exercised.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
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
import { PerformanceSection } from './PerformanceSection'

// Platform mode (identity.mode: 'platform') pins useStepUp() to 'confirm' —
// ConfirmDialog, no consent token (ADR-0010 WP3).
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

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — goal round budget (GOAL-FR-045, D-D/D-E)', () => {
  it('renders the configured global goal_max_rounds value from the API', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(20)
    })
  })

  it('falls back to the documented default (20) when an older backend omits goal_max_rounds', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      max_parallel_agents_configured: true,
      tools_on_demand: true,
      // goal_max_rounds intentionally omitted.
    } as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(20)
    })
  })

  it('the label and help text state plainly, in non-engineer language, that the setting applies to goals in chat and on tasks', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    expect(screen.getByText('Goal completion budget')).toBeInTheDocument()
    const explanation = screen.getByText(/how many times an agent may try to finish a goal/i)
    expect(explanation).toBeInTheDocument()
    expect(explanation).toHaveTextContent(/applies to goals set in chat and to goals on tasks/i)
    expect(
      screen.getByText(/goals already running keep the limit they started with/i),
    ).toBeInTheDocument()
    // No internal jargon leaks into the user-facing copy.
    expect(document.body.textContent).not.toMatch(/adjudication|owner loop|verifiers|sentinel/i)
  })

  it('S-34 adapted: raising the global budget in the interface opens the confirmation, and confirming saves ONLY goal_max_rounds', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, goal_max_rounds: 30 } as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '30' } })

    // Before the debounce fires, no PUT and no dialog yet.
    expect(screen.queryByTestId('confirm-dialog')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('confirm-accept'))
    fireEvent.click(screen.getByTestId('confirm-accept'))

    await waitFor(() => {
      // Load-bearing: the body carries ONLY goal_max_rounds. If the max
      // parallel/tools_on_demand fields were bundled in here it would mean
      // this control secretly writes the other two settings too, which
      // would make an unrelated edit on this screen able to silently revert
      // whatever an operator has open in another tab.
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { goal_max_rounds: 30 },
        undefined,
      )
    })
  })

  it('S-35 adapted: entering 0 is refused and no write occurs', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '0' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith({
        variant: 'error',
        message: 'Tries per goal must be a whole number of at least 1.',
      })
    })
    expect(screen.queryByTestId('confirm-dialog')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('rejects a negative value the same way', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '-5' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith({
        variant: 'error',
        message: 'Tries per goal must be a whole number of at least 1.',
      })
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('rejects a blank value', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith({
        variant: 'error',
        message: 'Tries per goal must be a whole number of at least 1.',
      })
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('accepts exactly 1 (the floor)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, goal_max_rounds: 1 } as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '1' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('confirm-accept'))
    fireEvent.click(screen.getByTestId('confirm-accept'))

    await waitFor(() => {
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { goal_max_rounds: 1 },
        undefined,
      )
    })
  })

  it('editing the goal budget does NOT bundle or revert max_parallel_agents/tools_on_demand — the two controls save independently', async () => {
    // Regression guard for the design this file pins: buildGoalBody sends
    // ONLY goal_max_rounds. If a future edit accidentally routed the goal
    // input through the shared buildBody (max_parallel_agents +
    // tools_on_demand), this assertion catches it because the payload would
    // then also carry those two fields.
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, goal_max_rounds: 45 } as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '45' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('confirm-accept'))
    fireEvent.click(screen.getByTestId('confirm-accept'))

    await waitFor(() => {
      const call = vi.mocked(api.updatePerformanceSettings).mock.calls[0]
      expect(call[0]).toEqual({ goal_max_rounds: 45 })
      expect(call[0]).not.toHaveProperty('max_parallel_agents')
      expect(call[0]).not.toHaveProperty('tools_on_demand')
    })

    // The max-parallel-agents input must still show its own, untouched value.
    expect(screen.getByLabelText('Max parallel agents')).toHaveValue(4)
  })

  it('cancelling re-auth reverts the input to the last-known server value and does not save', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '99' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('confirm-accept'))
    expect(screen.getByLabelText('Tries per goal')).toHaveValue(99)

    fireEvent.click(screen.getByTestId('confirm-cancel'))

    await waitFor(() => {
      expect(screen.queryByTestId('confirm-dialog')).not.toBeInTheDocument()
    })
    await waitFor(() => {
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(20)
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  // ── Review finding 15: two debounces, one pending slot ──────────────────
  //
  // "Max parallel agents" and "Tries per goal" each run their own 600 ms
  // debounce but both write into the SAME pending slot. Before the fix the
  // second timer REPLACED the first control's body, so only the later edit
  // was ever PUT — while onSuccess cleared both dirty flags and the sync
  // effects snapped the discarded field back to the server value, under a
  // "saved" indicator. Silent data loss, reported as a success.
  //
  // These two cases drive the real component through the real debounce
  // timers: nothing here hands the component a pre-merged body.
  it('finding 15: editing BOTH controls inside the debounce window saves BOTH — neither write discards the other', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_parallel_agents: 9,
      effective_max_parallel_agents: 9,
      goal_max_rounds: 30,
    } as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    // Max parallel agents first…
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '9' } })
    await act(async () => { vi.advanceTimersByTime(300) })
    // …then the goal budget, while the first debounce is still pending.
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '30' } })
    // Past both timers (first fires at 600 ms, second at 900 ms).
    await act(async () => { vi.advanceTimersByTime(1000) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('confirm-accept'))
    fireEvent.click(screen.getByTestId('confirm-accept'))

    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalled())

    // Exactly one PUT, carrying BOTH intents. The old behaviour sent
    // `{ goal_max_rounds: 30 }` alone and lost the 9 with no indication.
    const bodies = vi.mocked(api.updatePerformanceSettings).mock.calls.map((c) => c[0])
    const merged = Object.assign({}, ...bodies) as Record<string, unknown>
    expect(merged.max_parallel_agents).toBe(9)
    expect(merged.goal_max_rounds).toBe(30)
  })

  it('finding 15: the discarded control does not silently revert — both inputs still show what was typed after the save', async () => {
    // The server fixture is advanced to what the PUT actually persisted, so a
    // revert here can only come from the component dropping an edit, not from
    // a stale mock.
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

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '9' } })
    await act(async () => { vi.advanceTimersByTime(300) })
    fireEvent.change(screen.getByLabelText('Tries per goal'), { target: { value: '30' } })
    await act(async () => { vi.advanceTimersByTime(1000) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('confirm-accept'))
    fireEvent.click(screen.getByTestId('confirm-accept'))

    await waitFor(() => expect(api.updatePerformanceSettings).toHaveBeenCalled())

    // The refetch that follows a successful save must find BOTH values on the
    // server. Before the fix `max_parallel_agents` was never sent, so this
    // input snapped back to 4 while the screen said "saved".
    await waitFor(() => {
      expect(serverState.max_parallel_agents).toBe(9)
    })
    await waitFor(() => {
      expect(screen.getByLabelText('Max parallel agents')).toHaveValue(9)
      expect(screen.getByLabelText('Tries per goal')).toHaveValue(30)
    })
  })

  it('exactly one goal-round-budget control exists on the whole screen — no per-goal override surface (D-E)', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tries per goal'))

    // Exactly one "Tries per goal" control on the whole screen — not one
    // per goal, not a second field for an "override". The preceding test
    // asserts the card's own copy explains this control applies to goals in
    // chat and on tasks, so the negative here is structural: a single input,
    // not a list or a per-item field.
    expect(screen.getAllByLabelText('Tries per goal')).toHaveLength(1)
    expect(screen.queryAllByRole('spinbutton')).toHaveLength(2) // max-parallel-agents + goal round budget
  })
})
