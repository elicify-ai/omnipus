// UsageScreen main-title tests (founder UI feedback 2026-10-11, point 4).
//
// The "By session" table must read "<Agent> - Main Session" for an untitled
// main — derived from the agent's CURRENT display name in the shared agents
// query, so an agent rename updates the row without a reload — and never
// "Untitled". A server-set title always wins; non-main sessions keep the
// existing "Untitled" fallback.
//
// Scaffolding (router + api mocks, fixtures, provider) is copied from
// UsageScreen.test.tsx; fetchAgents is mocked alongside the other endpoints.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { UsageScreen } from './UsageScreen'
import { fetchTokenStats, fetchSessions, fetchAgents } from '@/lib/api'
import type { TokenUsageSummary } from '@/lib/api/generated/openapi-types'
import type { Session, Agent } from '@/lib/api'

// ── Mocks (copied from UsageScreen.test.tsx) ──────────────────────────────────

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

// ── Fixtures ──────────────────────────────────────────────────────────────────

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
  const view = render(
    <QueryClientProvider client={qc}>
      <UsageScreen />
    </QueryClientProvider>,
  )
  return { qc, view }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('UsageScreen — main session display title', () => {
  beforeEach(() => {
    vi.mocked(fetchTokenStats).mockResolvedValue(mockSummary)
    vi.mocked(fetchSessions).mockResolvedValue([sessionFixture({})])
    vi.mocked(fetchAgents).mockResolvedValue([agentFixture({})])
  })

  // The by-session table lives behind the "By session" breakdown tab
  // (default tab is "By agent") — same path the UsageScreen.test.tsx
  // sibling tests drive.
  async function openBySessionTab() {
    const user = userEvent.setup()
    const { qc, view } = renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByTestId('tab-session'))
    await waitFor(() => expect(screen.getByTestId('sessions-table')).toBeInTheDocument(), { timeout: 5000 })
    return { qc, view }
  }

  it('reads "<Agent> - Main Session" for an untitled main in the by-session table', async () => {
    await openBySessionTab()
    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Jim - Main Session' })).toBeInTheDocument()
    })
    expect(screen.queryByText('Untitled')).not.toBeInTheDocument()
  })

  it('shows the server title when one is present', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([sessionFixture({ title: 'Weekly planning' })])
    await openBySessionTab()
    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Weekly planning' })).toBeInTheDocument()
    })
  })

  it('keeps "Untitled" for an untitled NON-main session (unchanged titling)', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([sessionFixture({ id: 'sess-chat', type: 'chat' })])
    await openBySessionTab()
    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Untitled' })).toBeInTheDocument()
    })
  })

  it('follows an agent rename in the agents query data without a reload', async () => {
    const { qc } = await openBySessionTab()
    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Jim - Main Session' })).toBeInTheDocument()
    })

    // The rename lands in the shared agents query cache (['agents']) — the
    // same cache the Sessions and Search modal reads — exactly as an
    // agent-rename flow writes it (setQueryData / refetch). The row must
    // re-derive from the new cache data on the next render, never hold a
    // cached copy of the old title.
    qc.setQueryData(['agents'], [agentFixture({ name: 'Jim Prime' })])
    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Jim Prime - Main Session' })).toBeInTheDocument()
    })
    expect(screen.queryByRole('link', { name: 'Jim - Main Session' })).not.toBeInTheDocument()
  })
})
