import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChartBar, ChatCircle, CaretUp, CaretDown, Scales } from '@phosphor-icons/react'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Progress } from '@/components/ui/progress'
import { Badge } from '@/components/ui/badge'
import { fetchTokenStats, fetchSessions, tokenStatsQueryKeys, type TokenStatsPeriod, type Session } from '@/lib/api'
import { formatTokens } from '@/lib/formatTokens'
import { ScreenHeader } from '@/components/layout/ScreenHeader'
import { TokenBudgetSection } from '@/components/screens/TokenBudgetSection'

export { formatTokens }

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
                <Link to={item.href} tabIndex={0} className="hover:text-[var(--color-accent)] transition-colors">
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
  // Session classification (ADR-052 FR-036) — used only to flag verifier
  // (Judge) rows with a "Verifier" tag; sort/filter logic ignores it.
  type: Session['type']
}

type SortKey = 'tokens' | 'title'

type SortDir = 'asc' | 'desc'

function SessionsTable({ rows }: { rows: SessionRow[] }) {
  const [sortKey, setSortKey] = useState<SortKey>('tokens')
  // Defaults match the pre-existing (non-toggleable) behaviour: tokens
  // descending (biggest first), title ascending (A→Z).
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  function handleSort(key: SortKey) {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
      return
    }
    setSortKey(key)
    setSortDir(key === 'tokens' ? 'desc' : 'asc')
  }

  const sorted = [...rows].sort((a, b) => {
    const ascending = sortKey === 'tokens' ? a.tokens - b.tokens : a.title.localeCompare(b.title)
    return sortDir === 'asc' ? ascending : -ascending
  })

  if (sorted.length === 0) {
    return <p className="text-sm text-[var(--color-muted)] py-4 text-center">No session data.</p>
  }

  const titleSortIcon = sortKey === 'title' ? (sortDir === 'asc' ? <CaretUp size={10} weight="bold" aria-hidden="true" /> : <CaretDown size={10} weight="bold" aria-hidden="true" />) : null
  const tokensSortIcon = sortKey === 'tokens' ? (sortDir === 'asc' ? <CaretUp size={10} weight="bold" aria-hidden="true" /> : <CaretDown size={10} weight="bold" aria-hidden="true" />) : null

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs" data-testid="sessions-table">
        <thead>
          <tr className="border-b border-[var(--color-border)] text-[var(--color-muted)]">
            <th
              scope="col"
              className="text-left py-2 pr-4 font-medium"
              aria-sort={sortKey === 'title' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}
            >
              <button tabIndex={0}
                type="button"
                onClick={() => handleSort('title')}
                className={`inline-flex items-center gap-1 hover:text-[var(--color-secondary)] transition-colors${sortKey === 'title' ? ' text-[var(--color-accent)]' : ''}`}
              >
                Session
                {titleSortIcon}
              </button>
            </th>
            <th
              scope="col"
              className="text-right py-2 pl-4 font-medium"
              aria-sort={sortKey === 'tokens' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}
            >
              <button tabIndex={0}
                type="button"
                onClick={() => handleSort('tokens')}
                className={`inline-flex items-center gap-1 hover:text-[var(--color-secondary)] transition-colors${sortKey === 'tokens' ? ' text-[var(--color-accent)]' : ''}`}
              >
                {tokensSortIcon}
                Tokens
              </button>
            </th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr key={row.id} className="border-b border-[var(--color-border)]/50 hover:bg-[var(--color-surface-1)] transition-colors">
              <td className="py-2 pr-4 max-w-[200px] text-[var(--color-secondary)]">
                <div className="flex items-center gap-1.5 min-w-0" title={row.title}>
                  <Link
                    to="/sessions/$sessionId"
                    params={{ sessionId: row.id }}
                    tabIndex={0}
                    className="truncate min-w-0 hover:text-[var(--color-accent)] transition-colors"
                  >
                    {row.title || 'Untitled'}
                  </Link>
                  {row.type === 'verifier' && (
                    <Badge
                      variant="muted"
                      data-testid="session-verifier-tag"
                      className="shrink-0 gap-0.5 px-1.5 py-0 text-[9px] font-medium uppercase tracking-wider"
                    >
                      <Scales size={9} weight="bold" aria-hidden="true" />
                      Verifier
                    </Badge>
                  )}
                </div>
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

  // ADR-052 FR-036 / SC-014: UsageScreen is the ONE surface that must show
  // verifier-role (the Judge) LLM spend, unlike Sidebar/SearchModal which
  // exclude it. Verified against the live backend (pkg/gateway/rest_stats.go
  // HandleTokenStats, 2026-07): it aggregates ALL sessions returned by
  // ListAllSessions with NO session-type filter (it only excludes
  // subagent_3p agents) — so the hero stats, "By agent", and "By model"
  // breakdowns below already include the Judge's token/cost totals with
  // ZERO changes needed here. `include_verifier` only affects the separate
  // GET /sessions list endpoint.
  const {
    data: summary,
    isLoading: statsLoading,
    isError: statsError,
  } = useQuery({
    queryKey: tokenStatsQueryKeys.byPeriod(period),
    queryFn: () => fetchTokenStats(period),
  })

  // "By session" tab ONLY: built from GET /sessions, which excludes
  // verifier-type sessions by default (FR-036) unless include_verifier=true
  // is passed. This is the one caller in the app that opts in — Sidebar and
  // SearchModal must keep excluding them — so individual verifier session
  // rows appear here (tagged "Verifier", see SessionsTable), closing the
  // SC-014 gap: their aggregate spend was already counted in the hero/
  // by-agent/by-model views above via the unfiltered token-stats endpoint,
  // and now the per-session row list is complete too.
  //
  // ADR-057 FR-104 (W16h): also passes flat:true. GET /sessions defaults to
  // ROOTS ONLY under US-19's nested-listing design, which would silently
  // drop every delegated child's spend from this per-session accounting —
  // a real audit regression, not a display nuance (a chat that delegated
  // most of its work would show only its own small share). flat:true
  // returns every session — roots and subordinates — as one flat page, so
  // the sum of per-session totals stays equal to the true total (BDD-111).
  const {
    data: sessions = [],
    isLoading: sessionsLoading,
  } = useQuery({
    queryKey: ['sessions', 'includeVerifier', 'flat'],
    queryFn: () => fetchSessions(undefined, undefined, { includeVerifier: true, flat: true }),
    staleTime: 30_000,
  })

  const isLoading = statsLoading || sessionsLoading

  // Derive totals.
  // tokens_cache_read and tokens_cache_write are a SUBSET of tokens_total
  // (already counted in it — NOT additive). totalUncached reconciles with
  // totalCached so that Cached + Uncached == Total and neither double-counts.
  const totalTokens = summary?.agents?.reduce((acc, a) => acc + (a.tokens_total ?? 0), 0) ?? 0
  const totalCacheRead = summary?.tokens_cache_read ?? 0
  const totalCacheWrite = summary?.tokens_cache_write ?? 0
  const totalCached = totalCacheRead + totalCacheWrite
  const totalUncached = totalTokens - totalCached

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

  // By-session items — use sessions list (includes verifier sessions, see
  // the fetchSessions call above) with total_tokens.
  const sessionRows: SessionRow[] = sessions
    .filter((s) => s.total_tokens != null && s.total_tokens > 0)
    .map((s) => ({
      id: s.id,
      title: s.title || 'Untitled',
      tokens: s.total_tokens ?? 0,
      type: s.type,
    }))
    .sort((a, b) => b.tokens - a.tokens)
    .slice(0, 50)

  const isEmpty = !isLoading && totalTokens === 0

  return (
    <div className="absolute inset-0 flex flex-col">
      <ScreenHeader title="Usage" />
      <div className="flex-1 overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8 space-y-8">
        {/* Header + Period selector */}
        <div className="flex items-start justify-between gap-4 flex-wrap">
          <div className="flex items-center gap-3">
            <ChartBar size={22} weight="fill" className="text-[var(--color-accent)] shrink-0" />
            <div>
              {/* ScreenHeader above already renders "Usage" as the page's h2 —
                  this is a decorative restatement, not a second heading. */}
              <div className="font-headline text-xl font-bold text-[var(--color-secondary)]">Usage</div>
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
              <button tabIndex={0}
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

        {/* ADR-053 D12 / FE-6 — app-level OVERALL token budget. Rendered
            independently of the stats loading/empty states so the unbounded
            advisory (R§8.3a) is persistent. The section owns its own
            loading/error affordances. */}
        <TokenBudgetSection />

        {/* Loading state */}
        {isLoading && <UsageSkeleton />}

        {/* Error state */}
        {!isLoading && statsError && (
          <div className="rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm text-[var(--color-error)]">
            Could not load usage data. Check your connection and try again.
          </div>
        )}

        {/* Partial-data warning — some session stores failed to load, so totals may under-count. */}
        {!isLoading && !statsError && summary?.partial && (
          <div
            className="rounded-lg border border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 px-4 py-3 text-sm text-[var(--color-secondary)]"
            data-testid="usage-partial-warning"
            role="status"
          >
            Some sessions could not be read — these totals may under-count.
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
              tabIndex={0}
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-[var(--color-primary)] text-sm font-medium hover:opacity-90 transition-opacity"
            >
              Start a chat
            </Link>
          </div>
        )}

        {/* Main content */}
        {!isLoading && !statsError && !isEmpty && (
          <>
            {/* Hero stat row — Total / Cached (subset of total) / Uncached / Sessions */}
            {/* Cached + Uncached == Total. Cache tokens are a subset, not additive. */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4" data-testid="usage-hero-row">
              <StatCard
                label="Total tokens"
                value={formatTokens(totalTokens)}
                unit="tokens"
                hero
              />
              <StatCard
                label="Cached"
                value={formatTokens(totalCached)}
                unit="of total"
              />
              <StatCard
                label="Uncached"
                value={formatTokens(totalUncached)}
                unit="of total"
              />
              <StatCard
                label="Active agents"
                value={agentCount.toString()}
              />
            </div>

            {/* Secondary stat */}
            <div className="grid grid-cols-2 gap-4">
              <StatCard
                label="Top agent"
                value={topAgent ? (topAgent.agent_name ?? topAgent.agent_id) : '—'}
              />
              <StatCard
                label="Cache breakdown"
                value={totalCached > 0 ? `${formatTokens(totalCacheRead)} r / ${formatTokens(totalCacheWrite)} w` : '—'}
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
    </div>
  )
}
