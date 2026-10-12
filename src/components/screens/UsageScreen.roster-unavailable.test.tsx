// UsageScreen roster-unavailable tests (combined-review R2-S4-03).
//
// When the shared ['agents'] roster FAILS, the by-session table falls back
// to the agent id — the ruling's accepted stand-in — but until now nothing
// on the screen said the names had failed to load: "jim - Main Session"
// rows read as if the agent were genuinely named "jim". The screen now
// shows the same honest "unavailable" presentation the search modal shows
// for the identical trigger (catalogued ErrorState + Retry), and only when
// the query has FAILED — never while it is merely loading (the skeleton
// covers that window) and never when the roster loaded fine.
//
// Scaffolding (router + api mocks, fixtures, provider) is copied from
// UsageScreen.main-title.test.tsx.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { UsageScreen } from './UsageScreen'
import { fetchTokenStats, fetchSessions, fetchAgents } from '@/lib/api'
import type { TokenUsageSummary } from '@/lib/api/generated/openapi-types'
import type { Session, Agent } from '@/lib/api'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => vi.fn(),
    useLocation: () => ({ pathname: '/usage' }),
    Link: ({ children, to, ...rest }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
    ),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTokenStats: vi.fn(),
    fetchSessions: vi.fn(),
    fetchAgents: vi.fn(),
  }
})

const mockSummary: TokenUsageSummary = {
  agents: [
    {
      agent_id: 'jim',
      agent_name: 'Jim',
      tokens_in: 10000,
      tokens_out: 5000,
      tokens_total: 15000,
    },
  ],
  tokens_cache_read: 500,
  tokens_cache_write: 200,
  period_start: '2026-06-01T00:00:00Z',
  period_end: '2026-06-30T23:59:59Z',
}

function sessionFixture(overrides: Partial<Session>): Session {
  return {
    id: 'sess-main-1',
    type: 'main',
    agent_id: 'jim',
    title: '',
    status: 'active',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-02T00:00:00Z',
    message_count: 5,
    total_tokens: 12000,
    ...overrides,
  } as Session
}

function agentFixture(overrides: Partial<Agent>): Agent {
  return {
    id: 'jim',
    name: 'Jim',
    type: 'core',
    locked: true,
    figure: {} as Agent['figure'],
    role: {} as Agent['role'],
    revision: {} as Agent['revision'],
    ...overrides,
  } as Agent
}

function renderUsage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <UsageScreen />
    </QueryClientProvider>,
  )
}

describe('UsageScreen — a failed roster is signalled, not silent (R2-S4-03)', () => {
  beforeEach(() => {
    vi.mocked(fetchTokenStats).mockResolvedValue(mockSummary)
    vi.mocked(fetchSessions).mockResolvedValue([sessionFixture({})])
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture({})])
  })

  async function openBySessionTab() {
    const user = userEvent.setup()
    const { qc, view } = renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByTestId('tab-session'))
    await waitFor(() => expect(screen.getByTestId('sessions-table')).toBeInTheDocument(), { timeout: 5000 })
    return { qc, view }
  }

  it('when the roster has FAILED the by-session tab says agent names are unavailable — beside the id fallback', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    await openBySessionTab()

    expect(screen.getByText(/Agent names could not be loaded/)).toBeInTheDocument()
    // The ruling's id fallback still renders the row.
    expect(screen.getByRole('link', { name: 'jim - Main Session' })).toBeInTheDocument()
  })

  it('the unavailable note offers Retry, and retrying re-runs the roster fetch', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    await openBySessionTab()

    const callsBefore = vi.mocked(fetchAgents).mock.calls.length
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => {
      expect(vi.mocked(fetchAgents).mock.calls.length).toBeGreaterThan(callsBefore)
    })
  })

  it('while the roster is merely LOADING no unavailable note shows — the skeleton covers the window', async () => {
    vi.mocked(fetchAgents).mockReturnValue(new Promise(() => {})) // never settles
    renderUsage()
    await waitFor(() => {
      expect(screen.getByTestId('usage-skeleton')).toBeInTheDocument()
    })
    expect(screen.queryByText(/Agent names could not be loaded/)).not.toBeInTheDocument()
  })

  it('when the roster loaded fine no unavailable note shows', async () => {
    await openBySessionTab()
    expect(screen.queryByText(/Agent names could not be loaded/)).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Jim - Main Session' })).toBeInTheDocument()
  })
})
