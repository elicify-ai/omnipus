import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { UsageScreen, formatTokens } from './UsageScreen'
import type { TokenUsageSummary } from '@/lib/api/generated/openapi-types'
import type { Session } from '@/lib/api'

// ── Mocks ──────────────────────────────────────────────────────────────────────

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

// ── Helpers ────────────────────────────────────────────────────────────────────

const mockSummary: TokenUsageSummary = {
  agents: [
    {
      agent_id: 'agent-1',
      agent_name: 'Mia',
      tokens_in: 10000,
      tokens_out: 5000,
      tokens_total: 15000,
      by_model: {
        'claude-sonnet-4-6': { in: 10000, out: 5000, total: 15000 },
      },
    },
    {
      agent_id: 'agent-2',
      agent_name: 'Jim',
      tokens_in: 2000,
      tokens_out: 1000,
      tokens_total: 3000,
    },
  ],
  tokens_cache_read: 500,
  tokens_cache_write: 200,
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
  {
    id: 'sess-2',
    agent_id: 'agent-2',
    title: 'Second chat',
    type: 'chat',
    created_at: '2026-06-03T00:00:00Z',
    updated_at: '2026-06-04T00:00:00Z',
    message_count: 3,
    total_tokens: 3000,
  },
  {
    id: 'sess-3',
    agent_id: 'agent-1',
    title: 'No tokens',
    type: 'chat',
    created_at: '2026-06-05T00:00:00Z',
    updated_at: '2026-06-06T00:00:00Z',
    message_count: 1,
    total_tokens: 0,
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

// ── formatTokens unit tests ────────────────────────────────────────────────────

describe('formatTokens', () => {
  it('shows numbers below 1000 as-is', () => {
    expect(formatTokens(0)).toBe('0')
    expect(formatTokens(999)).toBe('999')
  })

  it('formats thousands as k with one decimal', () => {
    expect(formatTokens(1000)).toBe('1.0k')
    expect(formatTokens(44000)).toBe('44.0k')
    expect(formatTokens(1500)).toBe('1.5k')
  })

  it('formats millions as M with one decimal', () => {
    expect(formatTokens(1_200_000)).toBe('1.2M')
    expect(formatTokens(1_000_000)).toBe('1.0M')
  })
})

// ── UsageScreen component tests ────────────────────────────────────────────────

describe('UsageScreen', () => {
  beforeEach(() => {
    vi.mocked(fetchTokenStats).mockResolvedValue(mockSummary)
    vi.mocked(fetchSessions).mockResolvedValue(mockSessions)
    // FE-6: UsageScreen now mounts TokenBudgetSection, which fetches the
    // app-level budget status. Give it a stable resolved mock so the budget
    // card's own loading/error state doesn't bleed into the existing suites.
    vi.mocked(fetchTokenBudgetStatus).mockResolvedValue({
      budget: 0,
      consumed: 0,
      remaining: 0,
      exhausted: false,
      advisory: 'unbounded — set a budget',
      by_scope: { owner: 0, member: 0, verifier: 0, judge: 0 },
    })
  })

  it('shows loading skeleton while fetching', async () => {
    vi.mocked(fetchTokenStats).mockReturnValue(new Promise(() => {}))
    renderUsage()
    expect(screen.getByTestId('usage-skeleton')).toBeInTheDocument()
  })

  it('renders hero stat row with honest token breakdown after data loads', async () => {
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    // Total = 15000 + 3000 = 18000 → "18.0k"
    expect(screen.getByText('18.0k')).toBeInTheDocument()
    // Cached = tokens_cache_read (500) + tokens_cache_write (200) = 700 → "700"
    // Uncached = 18000 - 700 = 17300 → "17.3k"
    // These two reconcile: Cached + Uncached == Total
    expect(screen.getByText('700')).toBeInTheDocument()
    expect(screen.getByText('17.3k')).toBeInTheDocument()
    // No standalone "Input" or "Output" hero cards that would double-count cache
    const heroRow = screen.getByTestId('usage-hero-row')
    expect(heroRow.textContent?.toLowerCase()).not.toContain('input')
    expect(heroRow.textContent?.toLowerCase()).not.toContain('output')
  })

  it('hero Cached + Uncached == Total (no double-counting)', async () => {
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    // Arithmetic reconciliation: cache tokens are a subset of total, not additive.
    // Total = 18000, Cached = 700, Uncached = 17300. 700 + 17300 === 18000.
    const totalTokens = 15000 + 3000 // 18000
    const totalCached = 500 + 200    // 700 (from mockSummary.tokens_cache_read/write)
    const totalUncached = totalTokens - totalCached // 17300
    expect(totalCached + totalUncached).toBe(totalTokens)
  })

  it('renders by-agent bar list in the default agent tab', async () => {
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('tab-content-agent')).toBeInTheDocument())
    // Use getAllByText since agent name also appears in the "Top agent" StatCard
    expect(screen.getAllByText('Mia').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Jim').length).toBeGreaterThanOrEqual(1)
  })

  it('renders by-model bar list when model tab is selected', async () => {
    const user = userEvent.setup()
    renderUsage()
    // Wait for data to load (hero row must appear first)
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    // Click the model tab using userEvent — fires full pointer/mouse sequence that Radix expects
    await user.click(screen.getByTestId('tab-model'))
    // The model tab content mounts after click — wait for the model name to appear
    await waitFor(() =>
      expect(screen.getByText(/claude-sonnet-4-6/)).toBeInTheDocument(),
    )
  })

  it('renders by-session table when session tab is selected', async () => {
    const user = userEvent.setup()
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByTestId('tab-session'))
    // After clicking the session tab, Radix Tabs mounts the session panel content.
    await waitFor(
      () => expect(screen.getByTestId('sessions-table')).toBeInTheDocument(),
      { timeout: 5000 },
    )
    expect(screen.getByText('First chat')).toBeInTheDocument()
    expect(screen.getByText('Second chat')).toBeInTheDocument()
    // Sessions with 0 tokens should NOT appear in the table
    expect(screen.queryByText('No tokens')).not.toBeInTheDocument()
  })

  it('clicking the active sort header toggles direction instead of no-op-ing', async () => {
    const user = userEvent.setup()
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByTestId('tab-session'))
    await waitFor(
      () => expect(screen.getByTestId('sessions-table')).toBeInTheDocument(),
      { timeout: 5000 },
    )

    const rowTitles = () =>
      Array.from(screen.getByTestId('sessions-table').querySelectorAll('tbody tr')).map(
        (tr) => tr.textContent,
      )

    // Default: tokens descending — First chat (12000) before Second chat (3000).
    const tokensHeader = screen.getByRole('columnheader', { name: /tokens/i })
    expect(tokensHeader).toHaveAttribute('aria-sort', 'descending')
    expect(rowTitles()[0]).toContain('First chat')

    // Clicking the already-active "Tokens" header must toggle direction (ascending),
    // not no-op — Second chat (3000) should now come first.
    await user.click(screen.getByRole('button', { name: /tokens/i }))
    await waitFor(() => expect(tokensHeader).toHaveAttribute('aria-sort', 'ascending'))
    expect(rowTitles()[0]).toContain('Second chat')

    // Clicking again flips back to descending.
    await user.click(screen.getByRole('button', { name: /tokens/i }))
    await waitFor(() => expect(tokensHeader).toHaveAttribute('aria-sort', 'descending'))
    expect(rowTitles()[0]).toContain('First chat')
  })

  it('shows empty state when total tokens is 0', async () => {
    vi.mocked(fetchTokenStats).mockResolvedValue({
      agents: [],
      period_start: '2026-06-01T00:00:00Z',
      period_end: '2026-06-30T23:59:59Z',
    })
    vi.mocked(fetchSessions).mockResolvedValue([])
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-empty')).toBeInTheDocument())
    expect(screen.getByText('No usage yet')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Start a chat' })).toBeInTheDocument()
  })

  it('switching period buttons triggers a new query', async () => {
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('period-day')).toBeInTheDocument())

    // Initial load: called with default 'month'
    expect(vi.mocked(fetchTokenStats)).toHaveBeenLastCalledWith('month')

    // Switch to 'week'
    fireEvent.click(screen.getByTestId('period-week'))
    await waitFor(() =>
      expect(vi.mocked(fetchTokenStats)).toHaveBeenCalledWith('week'),
    )
  })

  it('shows error message and hides hero dashboard when stats fetch rejects', async () => {
    vi.mocked(fetchTokenStats).mockRejectedValue(new Error('network failure'))
    renderUsage()
    await waitFor(() =>
      expect(screen.getByText('Could not load usage data. Check your connection and try again.')).toBeInTheDocument(),
    )
    // The hero row must NOT be present — a fetch failure must not render a fake "0" dashboard
    expect(screen.queryByTestId('usage-hero-row')).not.toBeInTheDocument()
  })

  it('shows partial-data warning banner when response has partial: true', async () => {
    vi.mocked(fetchTokenStats).mockResolvedValue({
      ...mockSummary,
      partial: true,
    })
    renderUsage()
    await waitFor(() =>
      expect(screen.getByTestId('usage-partial-warning')).toBeInTheDocument(),
    )
    expect(
      screen.getByText('Some sessions could not be read — these totals may under-count.'),
    ).toBeInTheDocument()
  })

  it('does not show partial-data warning banner when partial is absent', async () => {
    // mockSummary has no partial field — ensure banner is absent after data loads
    vi.mocked(fetchTokenStats).mockResolvedValue({ ...mockSummary })
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    expect(screen.queryByTestId('usage-partial-warning')).toBeNull()
  })

  it('shows no dollar sign or CurrencyDollar anywhere in the rendered output', async () => {
    const { container } = renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    // No dollar amounts rendered
    expect(container.textContent).not.toMatch(/\$\d/)
    // No literal "cost" label (case-insensitive) in the visible stats area
    const heroRow = screen.getByTestId('usage-hero-row')
    expect(heroRow.textContent?.toLowerCase()).not.toContain('cost')
  })

  // ADR-052 FR-036 / SC-014 — UsageScreen must show verifier (Judge) LLM
  // spend, unlike Sidebar/SearchModal which exclude it. Verified against
  // pkg/gateway/rest_stats.go's HandleTokenStats: it aggregates every
  // session type with no filter, so a verifier agent's entry in the
  // fetchTokenStats response already surfaces here today — no
  // include_verifier plumbing needed for THIS view.
  describe('ADR-052 FR-036: verifier (Judge) spend visibility', () => {
    it('shows a verifier-role agent entry in the "By agent" breakdown (aggregate spend is unfiltered by session type)', async () => {
      vi.mocked(fetchTokenStats).mockResolvedValue({
        ...mockSummary,
        agents: [
          ...mockSummary.agents!,
          {
            agent_id: 'judge',
            agent_name: 'Judge',
            tokens_in: 800,
            tokens_out: 200,
            tokens_total: 1000,
          },
        ],
      })
      renderUsage()
      await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
      // "By agent" is the default active tab.
      expect(screen.getByTestId('tab-content-agent')).toHaveTextContent('Judge')
      // Verifier spend is folded into the grand total too.
      expect(screen.getByTestId('usage-hero-row')).toHaveTextContent(formatTokens(15000 + 3000 + 1000))
    })

    it('the "By session" tab calls fetchSessions with includeVerifier:true so verifier session rows are auditable (SC-014)', async () => {
      renderUsage()
      await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
      expect(vi.mocked(fetchSessions)).toHaveBeenCalledWith(undefined, undefined, { includeVerifier: true })
    })

    it('shows a verifier session row tagged "Verifier" in the "By session" tab', async () => {
      const user = userEvent.setup()
      vi.mocked(fetchSessions).mockResolvedValue([
        ...mockSessions,
        {
          id: 'sess-judge',
          agent_id: 'judge',
          title: 'Verdict review',
          type: 'verifier',
          created_at: '2026-06-07T00:00:00Z',
          updated_at: '2026-06-07T00:05:00Z',
          message_count: 4,
          total_tokens: 1000,
        },
      ])
      renderUsage()
      await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
      await user.click(screen.getByTestId('tab-session'))
      await waitFor(
        () => expect(screen.getByTestId('sessions-table')).toBeInTheDocument(),
        { timeout: 5000 },
      )

      expect(screen.getByText('Verdict review')).toBeInTheDocument()
      const tag = screen.getByTestId('session-verifier-tag')
      expect(tag).toBeInTheDocument()
      expect(tag).toHaveTextContent('Verifier')

      // Non-verifier rows must NOT carry the tag.
      const rows = screen.getByTestId('sessions-table').querySelectorAll('tbody tr')
      expect(rows).toHaveLength(3) // sess-1, sess-2, sess-judge (sess-3 has 0 tokens, excluded)
      expect(screen.getAllByTestId('session-verifier-tag')).toHaveLength(1)
    })
  })
})
