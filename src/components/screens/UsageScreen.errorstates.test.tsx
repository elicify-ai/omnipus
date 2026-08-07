// UsageScreen post-review Defect 5: the "By session" tab had no error
// branch for its own `fetchSessions` query — an HTTP failure rendered
// SessionsTable's built-in empty state ("No session data."), which is
// indistinguishable from a genuinely empty install. This file proves the
// fix: a failed per-session fetch now shows an explicit error + retry
// affordance instead, while a successful fetch still renders the real
// table (the positive control — Rule 4 anti-vacuity companion).
//
// New file (not appended to UsageScreen.test.tsx) per this repo's per-unit
// test-file convention; mirrors that file's mock setup.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { UsageScreen } from './UsageScreen'
import type { TokenUsageSummary } from '@/lib/api/generated/openapi-types'
import type { Session } from '@/lib/api'

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
    fetchTokenBudgetStatus: vi.fn(),
  }
})

import { fetchTokenStats, fetchSessions, fetchTokenBudgetStatus } from '@/lib/api'

const mockSummary: TokenUsageSummary = {
  agents: [
    {
      agent_id: 'agent-1',
      agent_name: 'Mia',
      tokens_in: 10000,
      tokens_out: 5000,
      tokens_total: 15000,
    },
  ],
  tokens_cache_read: 0,
  tokens_cache_write: 0,
  period_start: '2026-06-01T00:00:00Z',
  period_end: '2026-06-30T23:59:59Z',
}

const mockSessions: Session[] = [
  {
    id: 'sess-1',
    agent_id: 'agent-1',
    title: 'First chat',
    type: 'chat',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-02T00:00:00Z',
    message_count: 5,
    total_tokens: 12000,
  },
]

function renderUsage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <UsageScreen />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchTokenStats).mockResolvedValue(mockSummary)
  vi.mocked(fetchTokenBudgetStatus).mockResolvedValue({
    budget: 0,
    consumed: 0,
    remaining: 0,
    exhausted: false,
    advisory: 'unbounded — set a budget',
    by_scope: { owner: 0, member: 0, verifier: 0, judge: 0 },
  })
})

describe('UsageScreen — Defect 5: "By session" tab error branch (not indistinguishable from empty)', () => {
  it('shows an explicit error + retry, NOT "No session data.", when the per-session fetch fails', async () => {
    vi.mocked(fetchSessions).mockRejectedValue(new Error('network down'))
    const user = userEvent.setup()
    renderUsage()

    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByTestId('tab-session'))

    await waitFor(() => expect(screen.getByTestId('usage-session-error')).toBeInTheDocument())

    // Must NOT show the generic empty-state copy — that's the actual bug
    // (a real failure reading identically to "this install has no data yet").
    expect(screen.queryByText('No session data.')).not.toBeInTheDocument()
    expect(screen.queryByTestId('sessions-table')).not.toBeInTheDocument()
  })

  it('positive control: shows the real sessions table (not the error UI) when the fetch succeeds', async () => {
    vi.mocked(fetchSessions).mockResolvedValue(mockSessions)
    const user = userEvent.setup()
    renderUsage()

    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByTestId('tab-session'))

    await waitFor(() => expect(screen.getByTestId('sessions-table')).toBeInTheDocument())
    expect(screen.getByText('First chat')).toBeInTheDocument()
    expect(screen.queryByTestId('usage-session-error')).not.toBeInTheDocument()
  })

  it('clicking Retry re-fetches and renders the table once the underlying error clears', async () => {
    vi.mocked(fetchSessions).mockRejectedValueOnce(new Error('network down'))
    const user = userEvent.setup()
    renderUsage()

    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByTestId('tab-session'))
    await waitFor(() => expect(screen.getByTestId('usage-session-error')).toBeInTheDocument())

    vi.mocked(fetchSessions).mockResolvedValue(mockSessions)
    await user.click(screen.getByText('Retry'))

    await waitFor(() => expect(screen.getByTestId('sessions-table')).toBeInTheDocument())
    expect(screen.getByText('First chat')).toBeInTheDocument()
  })
})
