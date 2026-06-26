import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChartBar, ChatCircle } from '@phosphor-icons/react'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Progress } from '@/components/ui/progress'
import { fetchTokenStats, fetchSessions, tokenStatsQueryKeys, type TokenStatsPeriod } from '@/lib/api'

// ── Formatting helpers ─────────────────────────────────────────────────────────

/** Human-readable token count: 44.0k / 1.2M. Values under 1000 show as-is. */
export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return n.toString()
}

// ── StatCard ──────────────────────────────────────────────────────────────────

interface StatCardProps {
  label: string
  value: string
  hero?: boolean
  unit?: string
}

function StatCard({ label, value, hero = false, unit }: StatCardProps) {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 flex flex-col gap-1">
      <div
        className={`font-mono tabular-nums font-bold${hero ? ' text-2xl text-[var(--color-accent)]' : ' text-base text-[var(--color-secondary)]'}`}
      >
        {value}
        {unit && (
          <span className={`font-sans font-normal${hero ? ' text-sm ml-1 text-[var(--color-muted)]' : ' text-xs ml-1 text-[var(--color-muted)]'}`}>
            {unit}
          </span>
        )}
      </div>
      <div className="text-xs text-[var(--color-muted)]">{label}</div>
    </div>
  )
}

// ── Loading skeleton ──────────────────────────────────────────────────────────

function UsageSkeleton() {
  return (
    <div className="space-y-6" data-testid="usage-skeleton" aria-busy="true" aria-label="Loading usage data">
      {/* Hero row skeleton */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className="h-20 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse"
          />
        ))}
      </div>
      {/* Bar list skeleton */}
      <div className="space-y-3">
        {[1, 2, 3].map((i) => (
          <div key={i} className="flex items-center gap-3">
            <div className="w-24 h-3 rounded bg-[var(--color-surface-2)] animate-pulse" />
            <div className="flex-1 h-2 rounded-full bg-[var(--color-surface-2)] animate-pulse" />
            <div className="w-12 h-3 rounded bg-[var(--color-surface-2)] animate-pulse" />
          </div>
        ))}
      </div>
    </div>
  )
}

// ── Bar list ──────────────────────────────────────────────────────────────────

interface BarItem {
  name: string
  tokens: number
  href?: string
}

function BarList({ items, maxTokens }: { items: BarItem[]; maxTokens: number }) {
  if (items.length === 0) {
    return (
      <p className="text-sm text-[var(--color-muted)] py-4 text-center">No data for this period.</p>
    )
  }
  return (
    <div className="space-y-2.5" role="list">
      {items.map((item) => {
        const pct = maxTokens > 0 ? Math.round((item.tokens / maxTokens) * 100) : 0
        const formattedTokens = formatTokens(item.tokens)
        return (
          <div key={item.name} className="flex items-center gap-3" role="listitem">
            {/* Name */}
            <div className="w-28 sm:w-36 shrink-0 truncate text-xs text-[var(--color-secondary)]" title={item.name}>
              {item.href ? (
                <Link to={item.href} className="hover:text-[var(--color-accent)] transition-colors">
                  {item.name}
                </Link>
              ) : (
                item.name
              )}
            </div>
            {/* Bar */}
            <div className="flex-1 min-w-0">
              <Progress
                value={pct}
                aria-label={`${item.name}: ${item.tokens} tokens`}
                className="h-1.5"
              />
            </div>
            {/* Count */}
            <div
              className="w-14 text-right text-xs font-mono tabular-nums text-[var(--color-muted)] shrink-0"
              aria-label={`${item.tokens} tokens`}
            >
              {formattedTokens}
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ── Sessions table ────────────────────────────────────────────────────────────

interface SessionRow {
  id: string
  title: string
  tokens: number
}

type SortKey = 'tokens' | 'title'

function SessionsTable({ rows }: { rows: SessionRow[] }) {
  const [sortKey, setSortKey] = useState<SortKey>('tokens')
  const sorted = [...rows].sort((a, b) =>
    sortKey === 'tokens' ? b.tokens - a.tokens : a.title.localeCompare(b.title),
  )

  if (sorted.length === 0) {
    return <p className="text-sm text-[var(--color-muted)] py-4 text-center">No session data.</p>
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs" data-testid="sessions-table">
        <thead>
          <tr className="border-b border-[var(--color-border)] text-[var(--color-muted)]">
            <th className="text-left py-2 pr-4 font-medium">
              <button
                type="button"
                onClick={() => setSortKey('title')}
                className={`hover:text-[var(--color-secondary)] transition-colors${sortKey === 'title' ? ' text-[var(--color-accent)]' : ''}`}
              >
                Session
              </button>
            </th>
            <th className="text-right py-2 pl-4 font-medium">
              <button
                type="button"
                onClick={() => setSortKey('tokens')}
                className={`hover:text-[var(--color-secondary)] transition-colors${sortKey === 'tokens' ? ' text-[var(--color-accent)]' : ''}`}
              >
                Tokens
              </button>
            </th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr key={row.id} className="border-b border-[var(--color-border)]/50 hover:bg-[var(--color-surface-1)] transition-colors">
              <td className="py-2 pr-4 max-w-[200px] truncate text-[var(--color-secondary)]" title={row.title}>
                <Link
                  to="/sessions/$sessionId"
                  params={{ sessionId: row.id }}
                  className="hover:text-[var(--color-accent)] transition-colors"
                >
                  {row.title || 'Untitled'}
                </Link>
              </td>
              <td className="py-2 pl-4 text-right font-mono tabular-nums text-[var(--color-muted)]">
                {formatTokens(row.tokens)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ── Period selector ───────────────────────────────────────────────────────────

const PERIODS: { value: TokenStatsPeriod; label: string }[] = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
  { value: 'all', label: 'All' },
]

// ── Main screen ───────────────────────────────────────────────────────────────

export function UsageScreen() {
  const [period, setPeriod] = useState<TokenStatsPeriod>('month')
  const [breakdownTab, setBreakdownTab] = useState<'agent' | 'model' | 'session'>('agent')

  const {
    data: summary,
    isLoading: statsLoading,
    isError: statsError,
  } = useQuery({
    queryKey: tokenStatsQueryKeys.byPeriod(period),
    queryFn: () => fetchTokenStats(period),
  })

  const {
    data: sessions = [],
    isLoading: sessionsLoading,
  } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 30_000,
  })

  const isLoading = statsLoading || sessionsLoading

  // Derive totals
  const totalTokens = summary?.agents?.reduce((acc, a) => acc + (a.tokens_total ?? 0), 0) ?? 0
  const totalIn = summary?.agents?.reduce((acc, a) => acc + (a.tokens_in ?? 0), 0) ?? 0
  const totalOut = summary?.agents?.reduce((acc, a) => acc + (a.tokens_out ?? 0), 0) ?? 0
  const totalCacheRead = summary?.tokens_cache_read ?? 0
  const totalCacheWrite = summary?.tokens_cache_write ?? 0
  const totalCached = totalCacheRead + totalCacheWrite

  const agentCount = summary?.agents?.length ?? 0
  const topAgent = summary?.agents
    ? [...summary.agents].sort((a, b) => (b.tokens_total ?? 0) - (a.tokens_total ?? 0))[0]
    : null

  // By-agent bar items
  const agentItems: BarItem[] = (summary?.agents ?? [])
    .map((a) => ({ name: a.agent_name ?? a.agent_id, tokens: a.tokens_total ?? 0 }))
    .filter((a) => a.tokens > 0)
    .sort((a, b) => b.tokens - a.tokens)
  const agentMax = agentItems[0]?.tokens ?? 0

  // By-model bar items
  const modelMap = new Map<string, number>()
  for (const a of summary?.agents ?? []) {
    if (!a.by_model) continue
    for (const [model, entry] of Object.entries(a.by_model)) {
      modelMap.set(model, (modelMap.get(model) ?? 0) + (entry.total ?? 0))
    }
  }
  const modelItems: BarItem[] = [...modelMap.entries()]
    .map(([name, tokens]) => ({ name, tokens }))
    .filter((m) => m.tokens > 0)
    .sort((a, b) => b.tokens - a.tokens)
  const modelMax = modelItems[0]?.tokens ?? 0

  // By-session items — use sessions list with total_tokens
  const sessionRows: SessionRow[] = sessions
    .filter((s) => s.total_tokens != null && s.total_tokens > 0)
    .map((s) => ({
      id: s.id,
      title: s.title || 'Untitled',
      tokens: s.total_tokens ?? 0,
    }))
    .sort((a, b) => b.tokens - a.tokens)
    .slice(0, 50)

  const isEmpty = !isLoading && totalTokens === 0

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8 space-y-8">
        {/* Header + Period selector */}
        <div className="flex items-start justify-between gap-4 flex-wrap">
          <div className="flex items-center gap-3">
            <ChartBar size={22} weight="fill" className="text-[var(--color-accent)] shrink-0" />
            <div>
              <h1 className="font-headline text-xl font-bold text-[var(--color-secondary)]">Usage</h1>
              <p className="text-xs text-[var(--color-muted)] mt-0.5">Token usage by agent, model, and session</p>
            </div>
          </div>

          {/* Period segmented control */}
          <div
            className="flex items-center gap-0.5 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-0.5"
            role="group"
            aria-label="Select time period"
          >
            {PERIODS.map(({ value, label }) => (
              <button
                key={value}
                type="button"
                onClick={() => setPeriod(value)}
                data-testid={`period-${value}`}
                aria-pressed={period === value}
                className={`px-3 py-1 rounded-md text-xs font-medium transition-colors${
                  period === value
                    ? ' bg-[var(--color-surface-2)] text-[var(--color-accent)]'
                    : ' text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
        </div>

        {/* Loading state */}
        {isLoading && <UsageSkeleton />}

        {/* Error state */}
        {!isLoading && statsError && (
          <div className="rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm text-[var(--color-error)]">
            Could not load usage data. Check your connection and try again.
          </div>
        )}

        {/* Empty state */}
        {!isLoading && !statsError && isEmpty && (
          <div
            className="flex flex-col items-center justify-center py-20 gap-4 text-center"
            data-testid="usage-empty"
          >
            <ChatCircle size={48} className="text-[var(--color-border)]" aria-hidden="true" />
            <div>
              <p className="text-sm font-medium text-[var(--color-secondary)]">No usage yet</p>
              <p className="text-xs text-[var(--color-muted)] mt-1">Start a chat to see token usage here.</p>
            </div>
            <Link
              to="/"
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-[var(--color-primary)] text-sm font-medium hover:opacity-90 transition-opacity"
            >
              Start a chat
            </Link>
          </div>
        )}

        {/* Main content */}
        {!isLoading && !statsError && !isEmpty && (
          <>
            {/* Hero stat row */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4" data-testid="usage-hero-row">
              <StatCard
                label="Total tokens"
                value={formatTokens(totalTokens)}
                unit="tokens"
                hero
              />
              <StatCard
                label="Input"
                value={formatTokens(totalIn)}
                unit="in"
              />
              <StatCard
                label="Output"
                value={formatTokens(totalOut)}
                unit="out"
              />
              <StatCard
                label="Cached"
                value={formatTokens(totalCached)}
                unit="cached"
              />
            </div>

            {/* Secondary stats */}
            <div className="grid grid-cols-2 gap-4">
              <StatCard
                label="Active agents"
                value={agentCount.toString()}
              />
              <StatCard
                label="Top agent"
                value={topAgent ? (topAgent.agent_name ?? topAgent.agent_id) : '—'}
              />
            </div>

            {/* Breakdown tabs */}
            <Tabs
              value={breakdownTab}
              onValueChange={(v) => setBreakdownTab(v as 'agent' | 'model' | 'session')}
            >
              <TabsList className="mb-4">
                <TabsTrigger value="agent" data-testid="tab-agent">By agent</TabsTrigger>
                <TabsTrigger value="model" data-testid="tab-model">By model</TabsTrigger>
                <TabsTrigger value="session" data-testid="tab-session">By session</TabsTrigger>
              </TabsList>

              <TabsContent value="agent" data-testid="tab-content-agent">
                <BarList items={agentItems} maxTokens={agentMax} />
              </TabsContent>

              <TabsContent value="model" data-testid="tab-content-model">
                <BarList items={modelItems} maxTokens={modelMax} />
              </TabsContent>

              <TabsContent value="session" data-testid="tab-content-session">
                <SessionsTable rows={sessionRows} />
              </TabsContent>
            </Tabs>
          </>
        )}
      </div>
    </div>
  )
}
