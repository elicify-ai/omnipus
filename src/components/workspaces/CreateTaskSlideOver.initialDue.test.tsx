/**
 * CreateTaskSlideOver.initialDue.test.tsx
 *
 * Tests for the optional `initialDue` prop added in F-07.
 * Mirrors the mock setup in CreateTaskSlideOver.test.tsx exactly.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'

// ── API mock ─────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchMilestones: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
    boardTasksQueryKeys: { list: () => ['tasks'] },
    workspacesQueryKeys: { list: () => ['workspaces'] },
    milestonesQueryKeys: { list: (id: string) => ['milestones', id] },
    isApiError: vi.fn().mockReturnValue(false),
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
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

interface RenderProps {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  workspaceId?: string
  milestoneId?: string | null
  initialDue?: string
}

function renderSlideOver(props: RenderProps = {}) {
  const defaults: Required<Omit<RenderProps, 'initialDue' | 'milestoneId'>> = {
    open: true,
    onOpenChange: vi.fn(),
    workspaceId: 'proj-test',
  }
  const merged = { ...defaults, ...props }
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver
        open={merged.open}
        onOpenChange={merged.onOpenChange}
        workspaceId={merged.workspaceId}
        milestoneId={merged.milestoneId}
        initialDue={merged.initialDue}
      />
    </QueryClientProvider>,
  )
}

// ── Setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  mockAddToast.mockReset()
})

// DateTimePicker's formatted trigger label — matches formatDateTimeDisplay() in date-time-picker.tsx.
function formatDueLabel(datetimeLocal: string): string {
  return new Date(datetimeLocal).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CreateTaskSlideOver — initialDue prop (F-07)', () => {
  it('seeds the due input when initialDue is provided and slide-over opens', () => {
    // BDD: Given CreateTaskSlideOver opens with initialDue="2026-06-22T00:00",
    // When the component renders,
    // Then the due date DateTimePicker trigger shows the formatted seeded value.
    renderSlideOver({ initialDue: '2026-06-22T00:00' })

    const dueTrigger = screen.getByRole('button', { name: /^due date$/i })
    expect(dueTrigger).toHaveTextContent(formatDueLabel('2026-06-22T00:00'))
  })

  it('leaves the due input empty when initialDue is NOT provided (no regression)', () => {
    // BDD: Given CreateTaskSlideOver opens without an initialDue prop,
    // When the component renders,
    // Then the due date DateTimePicker shows its empty placeholder (default behavior preserved).
    renderSlideOver()

    const dueTrigger = screen.getByRole('button', { name: /^due date$/i })
    expect(dueTrigger).toHaveTextContent('Pick a date and time')
  })

  it('updates the due field when the slide-over is reopened with a different initialDue', () => {
    // BDD: Given CreateTaskSlideOver is first opened with initialDue="2026-06-22T00:00",
    // When it is closed then reopened with initialDue="2026-07-04T09:30",
    // Then the due date DateTimePicker trigger shows the "2026-07-04T09:30" formatting.
    const onOpenChange = vi.fn()
    const { rerender } = render(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver
          open={true}
          onOpenChange={onOpenChange}
          workspaceId="proj-test"
          initialDue="2026-06-22T00:00"
        />
      </QueryClientProvider>,
    )

    // Verify initial seed
    let dueTrigger = screen.getByRole('button', { name: /^due date$/i })
    expect(dueTrigger).toHaveTextContent(formatDueLabel('2026-06-22T00:00'))

    // Close (reset)
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    // Reopen with a different initialDue
    rerender(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver
          open={true}
          onOpenChange={onOpenChange}
          workspaceId="proj-test"
          initialDue="2026-07-04T09:30"
        />
      </QueryClientProvider>,
    )

    dueTrigger = screen.getByRole('button', { name: /^due date$/i })
    expect(dueTrigger).toHaveTextContent(formatDueLabel('2026-07-04T09:30'))
  })
})
