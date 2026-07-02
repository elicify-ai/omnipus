/**
 * CreateMilestoneSlideOver.test.tsx
 *
 * Unit/integration tests for CreateMilestoneSlideOver, focused on the
 * due-date round trip: the date field is a shadcn DatePicker (Popover +
 * Calendar), not a native `<input type="date">` (ADR-030 §10) — days are
 * picked via react-day-picker's `data-day="YYYY-MM-DD"` day-cell attribute.
 * Mirrors the pattern in MilestoneDatePopover.test.tsx.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateMilestoneSlideOver } from './CreateMilestoneSlideOver'

// Click a react-day-picker day cell (data-day="YYYY-MM-DD") inside the open DatePicker popover.
function clickDay(isoDate: string) {
  const btn = document.querySelector(`[data-day="${isoDate}"] button`)
  if (!btn) throw new Error(`no day button for ${isoDate}`)
  fireEvent.click(btn)
}

// The DatePicker trigger — queried by its DOM id (DatePicker forwards `id` to the
// trigger <Button>, same identifier the old native input used).
function dateTrigger(): HTMLElement {
  const el = document.getElementById('cm-due')
  if (!el) throw new Error('date trigger not found')
  return el
}

// When no value is set, the calendar opens on the real "today" month (react-day-picker's
// own default), not the target month — navigate forward/back via the Nav buttons so the
// target day is on-screen regardless of when the suite happens to run.
function navigateToMonth(isoDate: string) {
  const [y, m] = isoDate.split('-').map(Number)
  const target = new Date(y, m - 1, 1)
  const now = new Date()
  const diff = (target.getFullYear() - now.getFullYear()) * 12 + (target.getMonth() - now.getMonth())
  const label = diff >= 0 ? /go to the next month/i : /go to the previous month/i
  for (let i = 0; i < Math.abs(diff); i++) {
    fireEvent.click(screen.getByRole('button', { name: label }))
  }
}

// ── Mocks ─────────────────────────────────────────────────────────────────────

const mockCreateMilestone = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    createMilestone: (...args: unknown[]) => mockCreateMilestone(...args),
  }
})

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  })
}

function renderSlideOver(props?: Partial<React.ComponentProps<typeof CreateMilestoneSlideOver>>) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateMilestoneSlideOver
        open={true}
        onOpenChange={vi.fn()}
        workspaceId="ws-test-1"
        {...props}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockCreateMilestone.mockReset()
  mockAddToast.mockReset()
})

afterEach(() => {
  vi.clearAllMocks()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CreateMilestoneSlideOver — due-date round trip (mirrors MilestoneDatePopover)', () => {
  it('picking a day and submitting sends due_date as YYYY-MM-DD', async () => {
    mockCreateMilestone.mockResolvedValueOnce({ id: 'ms-new', name: 'v1.0 Launch', due_date: '2026-08-01' })

    renderSlideOver()
    await screen.findByText('New milestone')

    fireEvent.change(screen.getByLabelText(/^name/i), { target: { value: 'v1.0 Launch' } })

    fireEvent.click(dateTrigger())
    navigateToMonth('2026-08-01')
    clickDay('2026-08-01')

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create milestone/i }))
    })

    await waitFor(() => expect(mockCreateMilestone).toHaveBeenCalledOnce())
    const [workspaceId, body] = mockCreateMilestone.mock.calls[0]
    expect(workspaceId).toBe('ws-test-1')
    expect(body.due_date).toBe('2026-08-01')
    expect(body.name).toBe('v1.0 Launch')
  })

  it('differentiation: two different day picks produce two different due_date payloads', async () => {
    mockCreateMilestone
      .mockResolvedValueOnce({ id: 'ms-a', due_date: '2026-06-10' })
      .mockResolvedValueOnce({ id: 'ms-b', due_date: '2026-07-20' })

    const { unmount } = renderSlideOver()
    await screen.findByText('New milestone')
    fireEvent.change(screen.getByLabelText(/^name/i), { target: { value: 'Milestone A' } })
    fireEvent.click(dateTrigger())
    navigateToMonth('2026-06-10')
    clickDay('2026-06-10')
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create milestone/i }))
    })
    await waitFor(() => expect(mockCreateMilestone).toHaveBeenCalledTimes(1))
    expect(mockCreateMilestone.mock.calls[0][1].due_date).toBe('2026-06-10')
    unmount()

    renderSlideOver()
    await screen.findByText('New milestone')
    fireEvent.change(screen.getByLabelText(/^name/i), { target: { value: 'Milestone B' } })
    fireEvent.click(dateTrigger())
    navigateToMonth('2026-07-20')
    clickDay('2026-07-20')
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create milestone/i }))
    })
    await waitFor(() => expect(mockCreateMilestone).toHaveBeenCalledTimes(2))
    expect(mockCreateMilestone.mock.calls[1][1].due_date).toBe('2026-07-20')

    expect(mockCreateMilestone.mock.calls[0][1].due_date).not.toBe(
      mockCreateMilestone.mock.calls[1][1].due_date,
    )
  })

  it('submitting without picking a date omits due_date (undefined, not an empty string)', async () => {
    mockCreateMilestone.mockResolvedValueOnce({ id: 'ms-no-date' })

    renderSlideOver()
    await screen.findByText('New milestone')
    fireEvent.change(screen.getByLabelText(/^name/i), { target: { value: 'No date yet' } })

    // The DatePicker starts empty — no interaction needed.
    expect(dateTrigger()).toHaveTextContent('Pick a date')

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create milestone/i }))
    })

    await waitFor(() => expect(mockCreateMilestone).toHaveBeenCalledOnce())
    const body = mockCreateMilestone.mock.calls[0][1]
    expect(body.due_date).toBeUndefined()
  })

  it('clicking the already-picked day clears it back to the placeholder (clear-via-reclick)', async () => {
    renderSlideOver()
    await screen.findByText('New milestone')

    fireEvent.click(dateTrigger())
    navigateToMonth('2026-08-01')
    clickDay('2026-08-01')
    expect(dateTrigger()).toHaveTextContent('Aug 1, 2026')

    fireEvent.click(dateTrigger())
    clickDay('2026-08-01')
    expect(dateTrigger()).toHaveTextContent('Pick a date')
  })

  it('shows a success toast and closes the slide-over on successful create', async () => {
    mockCreateMilestone.mockResolvedValueOnce({ id: 'ms-ok', due_date: '2026-08-01' })
    const onOpenChange = vi.fn()

    renderSlideOver({ onOpenChange })
    await screen.findByText('New milestone')
    fireEvent.change(screen.getByLabelText(/^name/i), { target: { value: 'Beta' } })
    fireEvent.click(dateTrigger())
    navigateToMonth('2026-08-01')
    clickDay('2026-08-01')

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create milestone/i }))
    })

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())
    expect(mockAddToast.mock.calls[0][0].variant).toBe('success')
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('shows an error toast and keeps the slide-over open when createMilestone rejects', async () => {
    mockCreateMilestone.mockRejectedValueOnce(new Error('500 Internal Server Error'))
    const onOpenChange = vi.fn()

    renderSlideOver({ onOpenChange })
    await screen.findByText('New milestone')
    fireEvent.change(screen.getByLabelText(/^name/i), { target: { value: 'Will fail' } })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /create milestone/i }))
    })

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledOnce())
    expect(mockAddToast.mock.calls[0][0].variant).toBe('error')
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })
})

describe('CreateMilestoneSlideOver — name validation', () => {
  it('blocks submit and shows an inline error when name is empty', async () => {
    renderSlideOver()
    await screen.findByText('New milestone')

    fireEvent.click(screen.getByRole('button', { name: /create milestone/i }))

    expect(await screen.findByText(/name is required/i)).toBeInTheDocument()
    expect(mockCreateMilestone).not.toHaveBeenCalled()
  })
})
