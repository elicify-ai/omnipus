import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SchedulesList } from './SchedulesList'
import type { Schedule } from '@/lib/api/generated/openapi-types'
import type { Agent } from '@/lib/api'

// #264 US6 / FR-016 — Schedules feed renders as a card list (NO tables), with
// status badges, owner, trigger summary, next-run; per-agent filter works.

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSchedules: vi.fn(),
    fetchAgents: vi.fn(),
    runSchedule: vi.fn().mockResolvedValue({ schedule_id: 's1', status: 'ok' }),
    pauseSchedule: vi.fn(),
    deleteSchedule: vi.fn().mockResolvedValue(undefined),
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

import { fetchSchedules, fetchAgents, runSchedule } from '@/lib/api'

const mockAgents = [
  { id: 'mia', name: 'Mia', type: 'core' },
  { id: 'max', name: 'Max', type: 'custom' },
] as unknown as Agent[]

const mockSchedules: Schedule[] = [
  {
    id: 's1',
    name: 'Daily PR summary',
    enabled: true,
    owner_agent_id: 'mia',
    trigger: { kind: 'every', every_ms: 86_400_000 },
    message: 'Summarize PRs',
    deliver: false,
    session_mode: 'isolated',
    timeout_seconds: 0,
    state: { last_status: 'ok', next_run_at_ms: 1_900_000_000_000 },
    runs: [
      { ran_at_ms: 1_800_000_000_000, status: 'ok', session_id: 'sess-1', duration_ms: 4200 },
      { ran_at_ms: 1_700_000_000_000, status: 'error', error: 'provider down' },
    ],
    created_at_ms: 1_000,
    updated_at_ms: 2_000,
  },
  {
    id: 's2',
    name: 'Weekly cron job',
    enabled: false,
    owner_agent_id: 'max',
    trigger: { kind: 'cron', cron_expr: '0 9 * * 1' },
    message: 'Weekly report',
    deliver: false,
    session_mode: 'continue',
    timeout_seconds: 120,
    state: { last_status: 'error' },
    runs: [],
    created_at_ms: 1_000,
    updated_at_ms: 2_000,
  },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderList(agentId?: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SchedulesList agentId={agentId} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchSchedules).mockResolvedValue(mockSchedules)
  vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
})

describe('SchedulesList — card feed (#264 US6 AS1)', () => {
  it('renders schedules as cards with status badges and owner', async () => {
    const { container } = renderList()
    await screen.findByText('Daily PR summary')
    expect(screen.getByText('Weekly cron job')).toBeInTheDocument()

    // status badges
    expect(screen.getByText('Enabled')).toBeInTheDocument()
    expect(screen.getByText('Paused')).toBeInTheDocument()
    // session-mode badges
    expect(screen.getByText('isolated')).toBeInTheDocument()
    expect(screen.getByText('continue')).toBeInTheDocument()
    // owner names resolved from agents
    expect(screen.getByText(/Owner: Mia/)).toBeInTheDocument()
    expect(screen.getByText(/Owner: Max/)).toBeInTheDocument()
    // trigger summary
    expect(screen.getByText(/Every 1d/)).toBeInTheDocument()
    expect(screen.getByText(/Cron · 0 9 \* \* 1/)).toBeInTheDocument()

    // NO TABLE rendered (FR-016 / SC-009)
    expect(container.querySelector('table')).toBeNull()
  })

  it('filters to a single owner when agentId is provided', async () => {
    renderList('mia')
    await screen.findByText('Daily PR summary')
    expect(screen.queryByText('Weekly cron job')).not.toBeInTheDocument()
  })

  it('shows empty state when no schedules', async () => {
    vi.mocked(fetchSchedules).mockResolvedValue([])
    renderList()
    await screen.findByText(/no schedules yet/i)
    expect(screen.queryByText('Daily PR summary')).not.toBeInTheDocument()
  })

  it('fires run-now and toasts on a card action', async () => {
    renderList()
    await screen.findByText('Daily PR summary')
    fireEvent.click(screen.getAllByRole('button', { name: /run now/i })[0])
    await waitFor(() => expect(runSchedule).toHaveBeenCalledWith('s1'))
  })

  it('opens the detail sheet with run history when a card is clicked', async () => {
    renderList()
    await screen.findByText('Daily PR summary')
    fireEvent.click(screen.getByTestId('schedule-card-s1'))
    await screen.findByText(/run history/i)
    // two run rows for s1, no table
    expect(screen.getAllByTestId('schedule-run-row')).toHaveLength(2)
  })
})
