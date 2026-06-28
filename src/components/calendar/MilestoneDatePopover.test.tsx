/**
 * MilestoneDatePopover.test.tsx
 *
 * Unit/integration tests for MilestoneDatePopover — the keyboard/single-pointer
 * reschedule path for milestones (US-5/AS-2, US-3/AS-8, FR-009, FR-010, C-5).
 *
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2) §9 #18.
 *
 * Strategy: mount MilestoneDatePopover wrapped in QueryClientProvider + mock
 * updateMilestone / addToast. Fire submit/cancel interactions with RTL and assert
 * on API calls, toast variant, and dialog open/closed state.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MilestoneDatePopover } from './MilestoneDatePopover'
import type { MilestoneTarget } from './types'

// ── 1. Mock @/lib/api ─────────────────────────────────────────────────────────

const mockUpdateMilestone = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    updateMilestone: (...args: unknown[]) => mockUpdateMilestone(...args),
  }
})

// ── 2. Mock @/store/ui ────────────────────────────────────────────────────────

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── 3. Helpers ────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
}

function makeMilestone(overrides: Partial<MilestoneTarget> = {}): MilestoneTarget {
  return {
    id: 'ms-1',
    name: 'Beta Release',
    due_date: '2026-06-25',
    ...overrides,
  }
}

interface RenderOptions {
  milestone?: MilestoneTarget | null
  onClose?: () => void
  onRescheduled?: () => void
  workspaceId?: string
}

function renderPopover({
  milestone = makeMilestone(),
  onClose = vi.fn<() => void>(),
  onRescheduled = vi.fn<() => void>(),
  workspaceId = 'ws-test-1',
}: RenderOptions = {}) {
  const client = makeClient()
  const result = render(
    <QueryClientProvider client={client}>
      <MilestoneDatePopover
        workspaceId={workspaceId}
        milestone={milestone}
        onClose={onClose}
        onRescheduled={onRescheduled}
      />
    </QueryClientProvider>,
  )
  return { ...result, onClose, onRescheduled }
}

// ── 4. Setup / teardown ───────────────────────────────────────────────────────

beforeEach(() => {
  mockUpdateMilestone.mockReset()
  mockAddToast.mockReset()
})

afterEach(() => {
  vi.clearAllMocks()
})

// ── 5. Tests ──────────────────────────────────────────────────────────────────

describe('MilestoneDatePopover — prefill (spec §9 #18 / FR-009 / C-5)', () => {
  it('prefills the date input from milestone.due_date when opened', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18 / US-5/AS-2 / C-5
    // BDD: Given the dialog opens with milestone.due_date = "2026-06-25",
    // When the dialog renders,
    // Then the date input value is "2026-06-25".

    renderPopover({ milestone: makeMilestone({ due_date: '2026-06-25' }) })

    const input = screen.getByTestId('milestone-date-input') as HTMLInputElement
    expect(input.value).toBe('2026-06-25')
  })

  it('prefills empty string when milestone.due_date is null', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18 / C-5
    // BDD: Given milestone.due_date is null, the input starts empty.

    renderPopover({ milestone: makeMilestone({ due_date: null }) })

    const input = screen.getByTestId('milestone-date-input') as HTMLInputElement
    expect(input.value).toBe('')
  })

  it('differentiation: two different milestone.due_dates prefill two different values', async () => {
    // Anti-hardcode: different milestones prefill different date values.
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18

    // First render: ms-A with due_date "2026-06-01"
    const { unmount: unmount1 } = renderPopover({
      milestone: makeMilestone({ id: 'ms-A', due_date: '2026-06-01' }),
    })
    const input1 = screen.getByTestId('milestone-date-input') as HTMLInputElement
    expect(input1.value).toBe('2026-06-01')
    unmount1()

    // Second render: ms-B with due_date "2026-07-15"
    renderPopover({
      milestone: makeMilestone({ id: 'ms-B', due_date: '2026-07-15' }),
    })
    const input2 = screen.getByTestId('milestone-date-input') as HTMLInputElement
    expect(input2.value).toBe('2026-07-15')
  })
})

describe('MilestoneDatePopover — empty date disables Save (spec §9 #18)', () => {
  it('Save button is disabled when the date input is empty', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18 / FR-009
    // BDD: Given the dialog is open with an empty date, the Save button is disabled.

    renderPopover({ milestone: makeMilestone({ due_date: null }) })

    const saveBtn = screen.getByTestId('milestone-date-save')
    expect(saveBtn).toBeDisabled()
  })

  it('Save button becomes enabled once a date is entered', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18 / FR-009

    renderPopover({ milestone: makeMilestone({ due_date: null }) })

    const input = screen.getByTestId('milestone-date-input')
    await userEvent.type(input, '2026-08-01')

    const saveBtn = screen.getByTestId('milestone-date-save')
    expect(saveBtn).not.toBeDisabled()
  })
})

describe('MilestoneDatePopover — Save success (spec §9 #18 / FR-010 / I-5)', () => {
  it('calls updateMilestone with correct args and fires success toast + callbacks on success', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18 / FR-010 / US-3/AS-4
    // BDD: Given the dialog is open with a valid date,
    // When the user clicks Save and updateMilestone resolves,
    // Then updateMilestone(workspaceId, milestone.id, { due_date }) is called,
    //   AND a toast with variant "success" fires,
    //   AND onRescheduled() is called,
    //   AND onClose() is called.

    mockUpdateMilestone.mockResolvedValueOnce({ id: 'ms-1', due_date: '2026-06-28' })

    const onClose = vi.fn()
    const onRescheduled = vi.fn()
    renderPopover({
      milestone: makeMilestone({ id: 'ms-1', due_date: '2026-06-25' }),
      workspaceId: 'ws-abc',
      onClose,
      onRescheduled,
    })

    // Change the date value to something new
    const input = screen.getByTestId('milestone-date-input') as HTMLInputElement
    await userEvent.clear(input)
    await userEvent.type(input, '2026-06-28')

    await act(async () => {
      screen.getByTestId('milestone-date-save').click()
    })

    await waitFor(() => expect(mockUpdateMilestone).toHaveBeenCalledOnce())

    // updateMilestone must be called with (workspaceId, milestoneId, { due_date })
    const [wsId, msId, body] = mockUpdateMilestone.mock.calls[0]
    expect(wsId).toBe('ws-abc')
    expect(msId).toBe('ms-1')
    expect(body.due_date).toBe('2026-06-28')

    // Success toast
    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())
    const toastArg = mockAddToast.mock.calls[0][0]
    expect(toastArg.variant).toBe('success')

    // Callbacks fired
    expect(onRescheduled).toHaveBeenCalledOnce()
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('differentiation: two different milestone targets send two different due_date payloads', async () => {
    // Anti-hardcode: different milestones produce different PATCH payloads.
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18

    mockUpdateMilestone
      .mockResolvedValueOnce({ id: 'ms-X', due_date: '2026-06-10' })
      .mockResolvedValueOnce({ id: 'ms-Y', due_date: '2026-07-20' })

    // First render: ms-X
    const onClose1 = vi.fn()
    const { unmount: unmount1 } = renderPopover({
      milestone: makeMilestone({ id: 'ms-X', due_date: '2026-06-01' }),
      workspaceId: 'ws-1',
      onClose: onClose1,
    })

    const input1 = screen.getByTestId('milestone-date-input') as HTMLInputElement
    await userEvent.clear(input1)
    await userEvent.type(input1, '2026-06-10')
    await act(async () => {
      screen.getByTestId('milestone-date-save').click()
    })
    await waitFor(() => expect(mockUpdateMilestone).toHaveBeenCalledTimes(1))
    expect(mockUpdateMilestone.mock.calls[0][1]).toBe('ms-X')
    expect(mockUpdateMilestone.mock.calls[0][2].due_date).toBe('2026-06-10')
    unmount1()

    mockAddToast.mockReset()

    // Second render: ms-Y
    const onClose2 = vi.fn()
    renderPopover({
      milestone: makeMilestone({ id: 'ms-Y', due_date: '2026-07-01' }),
      workspaceId: 'ws-1',
      onClose: onClose2,
    })

    const input2 = screen.getByTestId('milestone-date-input') as HTMLInputElement
    await userEvent.clear(input2)
    await userEvent.type(input2, '2026-07-20')
    await act(async () => {
      screen.getByTestId('milestone-date-save').click()
    })
    await waitFor(() => expect(mockUpdateMilestone).toHaveBeenCalledTimes(2))
    expect(mockUpdateMilestone.mock.calls[1][1]).toBe('ms-Y')
    expect(mockUpdateMilestone.mock.calls[1][2].due_date).toBe('2026-07-20')

    // The two due_date values must differ
    expect(mockUpdateMilestone.mock.calls[0][2].due_date).not.toBe(
      mockUpdateMilestone.mock.calls[1][2].due_date,
    )
  })
})

describe('MilestoneDatePopover — Save failure (spec §9 #18 / FR-010)', () => {
  it('shows an error toast when updateMilestone rejects, and dialog stays open (onClose NOT called)', async () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18 / FR-010 / US-3/AS-5
    // BDD: Given updateMilestone rejects (e.g. 500),
    // When the user clicks Save,
    // Then an error toast fires,
    //   AND onClose is NOT called (dialog stays open for retry).

    mockUpdateMilestone.mockRejectedValueOnce(new Error('500 Internal Server Error'))

    const onClose = vi.fn()
    const onRescheduled = vi.fn()
    renderPopover({
      milestone: makeMilestone({ id: 'ms-fail', due_date: '2026-06-25' }),
      onClose,
      onRescheduled,
    })

    // The date input is already prefilled; just click Save
    await act(async () => {
      screen.getByTestId('milestone-date-save').click()
    })

    await waitFor(() => expect(mockUpdateMilestone).toHaveBeenCalledOnce())

    // Error toast must fire with variant 'error'
    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())
    const toastArg = mockAddToast.mock.calls[0][0]
    expect(toastArg.variant).toBe('error')

    // Dialog must stay open — onClose must NOT have been called
    expect(onClose).not.toHaveBeenCalled()
    // onRescheduled must NOT have been called
    expect(onRescheduled).not.toHaveBeenCalled()
  })
})

describe('MilestoneDatePopover — closed when milestone is null', () => {
  it('does not render the dialog content when milestone is null', () => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #18 / C-5
    // BDD: Given milestone is null, the dialog is not open.

    renderPopover({ milestone: null })

    // The dialog should be closed — no date input visible
    expect(screen.queryByTestId('milestone-date-input')).not.toBeInTheDocument()
  })
})
