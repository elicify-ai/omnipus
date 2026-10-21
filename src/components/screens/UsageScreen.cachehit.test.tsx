import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { UsageScreen } from './UsageScreen'
import type { TokenUsageSummary } from '@/lib/api/generated/openapi-types'

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

import { fetchAgents, fetchSessions, fetchTokenStats } from '@/lib/api'

// 100 uncached + 800 read + 100 write. The spec's hit ratio is 0.8, shown as 80%.
const hitSummary: TokenUsageSummary = {
  agents: [
    {
      agent_id: 'mia',
      agent_name: 'Mia',
      tokens_in: 100,
      tokens_out: 10,
      tokens_total: 1010,
      tokens_cache_read: 800,
      tokens_cache_write: 100,
      cache_hit_ratio: 0.8,
    },
  ],
  tokens_cache_read: 800,
  tokens_cache_write: 100,
  by_provider: {
    anthropic: { in: 100, out: 10, cache_read: 800, cache_write: 100, total: 1010, cache_hit_ratio: 0.8 },
  },
  period_start: '2026-06-01T00:00:00Z',
  period_end: '2026-07-01T00:00:00Z',
}

const nullSummary: TokenUsageSummary = {
  agents: [
    {
      agent_id: 'mia',
      agent_name: 'Mia',
      tokens_in: 0,
      tokens_out: 10,
      tokens_total: 10,
      cache_hit_ratio: null,
    },
  ],
  tokens_cache_read: 0,
  tokens_cache_write: 0,
  period_start: '2026-06-01T00:00:00Z',
  period_end: '2026-07-01T00:00:00Z',
}

function renderUsage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <UsageScreen />
    </QueryClientProvider>,
  )
}

describe('TestUsageScreen_HitPercentAndProviderTab', () => {
  beforeEach(() => {
    vi.mocked(fetchTokenStats).mockResolvedValue(hitSummary)
    vi.mocked(fetchSessions).mockResolvedValue([])
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('shows 80% for 0.8 and no percent for a null ratio', async () => {
    const first = renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    expect(screen.getByText('80%')).toBeInTheDocument()
    expect(screen.queryByText('0.8%')).not.toBeInTheDocument()
    first.unmount()

    vi.mocked(fetchTokenStats).mockResolvedValue(nullSummary)
    renderUsage()
    await waitFor(() => expect(screen.getAllByText('Mia').length).toBeGreaterThan(0))
    expect(screen.queryByText('0%')).not.toBeInTheDocument()
    expect(screen.queryByText('Cache hit')).not.toBeInTheDocument()
  })

  it('adds the provider breakdown on the existing tabs control', async () => {
    const user = userEvent.setup()
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    const providerTab = screen.getByRole('tab', { name: /provider/i })
    await user.click(providerTab)
    expect(providerTab).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('anthropic')).toBeInTheDocument()
    expect(screen.getByText('80%')).toBeInTheDocument()
  })

  it('keeps the selected tab focused when the period changes', async () => {
    const user = userEvent.setup()
    let resolveDay!: (summary: TokenUsageSummary) => void
    const dayResponse = new Promise<TokenUsageSummary>((resolve) => { resolveDay = resolve })
    vi.mocked(fetchTokenStats).mockImplementation((period) => (
      period === 'day' ? dayResponse : Promise.resolve(hitSummary)
    ))
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-hero-row')).toBeInTheDocument())
    await user.click(screen.getByRole('tab', { name: /provider/i }))
    expect(screen.getByRole('tab', { name: /provider/i })).toHaveAttribute('aria-selected', 'true')

    await user.click(screen.getByTestId('period-day'))
    await waitFor(() => expect(fetchTokenStats).toHaveBeenLastCalledWith('day'))
    expect(screen.getByTestId('usage-skeleton')).toBeInTheDocument()
    resolveDay(hitSummary)
    await waitFor(() => expect(screen.queryByTestId('usage-skeleton')).not.toBeInTheDocument())

    // The period fetch remounts the tabs. Inspect the live selection, not a detached tab.
    expect(screen.getByTestId('period-day')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('tab', { name: /provider/i })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: /agent/i })).toHaveAttribute('aria-selected', 'false')
    const providerPanel = screen.getByRole('tabpanel', { name: /provider/i })
    expect(providerPanel).toBeVisible()
    expect(within(providerPanel).getByText('anthropic')).toBeInTheDocument()
    expect(within(providerPanel).getByText('80%')).toBeInTheDocument()
  })

  it('keeps the loading state', () => {
    vi.mocked(fetchTokenStats).mockReturnValue(new Promise(() => {}))
    renderUsage()
    expect(screen.getByTestId('usage-skeleton')).toBeInTheDocument()
  })

  it('keeps the empty state', async () => {
    vi.mocked(fetchTokenStats).mockResolvedValue({
      agents: [],
      period_start: '2026-06-01T00:00:00Z',
      period_end: '2026-07-01T00:00:00Z',
    })
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-empty')).toBeInTheDocument())
  })

  it('keeps an error distinct from an empty list', async () => {
    vi.mocked(fetchTokenStats).mockRejectedValue(new Error('offline'))
    renderUsage()
    await waitFor(() => expect(screen.getByText(/could not load usage data/i)).toBeInTheDocument())
    expect(screen.queryByTestId('usage-empty')).not.toBeInTheDocument()
  })

  it('keeps the partial state', async () => {
    vi.mocked(fetchTokenStats).mockResolvedValue({ ...hitSummary, partial: true })
    renderUsage()
    await waitFor(() => expect(screen.getByTestId('usage-partial-warning')).toBeInTheDocument())
  })
})
