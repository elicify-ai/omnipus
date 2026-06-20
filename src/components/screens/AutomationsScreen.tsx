import { useQuery } from '@tanstack/react-query'
import { Lightning } from '@phosphor-icons/react'
import {
  fetchSchedules,
  fetchAgents,
  fetchTokenStats,
  tokenStatsQueryKeys,
  fetchAuditLog,
  auditLogQueryKeys,
} from '@/lib/api'
import type { AuditEntry } from '@/lib/api'
import type { Schedule, ScheduleTrigger } from '@/lib/api/generated/openapi-types'
import { useAuthStore } from '@/store/auth'
import { SkeletonList, EmptyState, ErrorState } from '@/components/shared/ListStates'
import { Badge } from '@/components/ui/badge'
import { triggerSummary } from '@/components/command-center/SchedulesList'
import { sessionModeLabel } from '@/components/command-center/ScheduleFormSheet'

// ── Trigger → Action rules (read-only) ────────────────────────────────────────

// actionSummary describes what a schedule does when it fires. A deliver=true
// schedule sends its message straight to the channel; otherwise the owning
// agent processes it (autonomy).
function actionSummary(schedule: Schedule): string {
  if (schedule.deliver) {
    const target = schedule.chat_id
      ? `${schedule.channel ?? 'channel'} · ${schedule.chat_id}`
      : (schedule.channel ?? 'channel')
    return `Send to ${target}`
  }
  return `Run agent`
}

function triggerLabel(trigger: ScheduleTrigger): string {
  return triggerSummary(trigger)
}

function AutomationRulesSection() {
  const { data: schedules = [], isLoading, isError } = useQuery({
    queryKey: ['schedules'],
    queryFn: fetchSchedules,
  })

  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })
  const agentName = (id: string) => agents.find((a) => a.id === id)?.name ?? id

  return (
    <div className="px-4 py-2">
      {isError ? (
        <ErrorState message="Could not load automations." />
      ) : isLoading ? (
        <SkeletonList />
      ) : schedules.length === 0 ? (
        <EmptyState
          icon={<Lightning size={32} className="text-[var(--color-muted)]" />}
          message="No automations yet."
        />
      ) : (
        <div className="space-y-2">
          {schedules.map((schedule) => (
            <div
              key={schedule.id}
              data-testid={`automation-card-${schedule.id}`}
              className="p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
            >
              <div className="flex items-center gap-2 flex-wrap mb-2">
                <span className="font-medium text-sm text-[var(--color-secondary)] truncate">
                  {schedule.name}
                </span>
                <Badge variant={schedule.enabled ? 'success' : 'muted'} className="text-[10px]">
                  {schedule.enabled ? 'Active' : 'Paused'}
                </Badge>
                <Badge variant="secondary" className="text-[10px]">
                  {sessionModeLabel(schedule.session_mode)}
                </Badge>
              </div>
              {/* Trigger → Action framing */}
              <div className="flex items-center gap-2 text-sm flex-wrap">
                <span className="text-[var(--color-muted)]">When</span>
                <span className="text-[var(--color-secondary)] font-medium">
                  {triggerLabel(schedule.trigger)}
                </span>
                <span className="text-[var(--color-accent)]" aria-hidden>→</span>
                <span className="text-[var(--color-muted)]">Do</span>
                <span className="text-[var(--color-secondary)] font-medium">
                  {actionSummary(schedule)}
                </span>
              </div>
              <div className="mt-1.5 flex items-center gap-2 flex-wrap text-[11px] text-[var(--color-muted)]">
                <span>Agent: {agentName(schedule.owner_agent_id)}</span>
                {schedule.state?.next_run_at_ms && (
                  <>
                    <span aria-hidden>·</span>
                    <span>Next: {new Date(schedule.state.next_run_at_ms).toLocaleString()}</span>
                  </>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ── Token Usage table ─────────────────────────────────────────────────────────

function TokenUsageSection() {
  const { data, isLoading, isError } = useQuery({
    queryKey: tokenStatsQueryKeys.monthly(),
    queryFn: fetchTokenStats,
    staleTime: 60_000,
    refetchInterval: 60_000,
  })

  if (isLoading) {
    return (
      <div className="flex flex-col gap-2 p-4">
        {[1, 2, 3].map((i) => (
          <div key={i} className="h-8 rounded bg-[var(--color-surface-2)] animate-pulse" />
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <p className="px-4 py-3 text-sm text-[var(--color-error)]">
        Failed to load token usage. Please try again.
      </p>
    )
  }

  if (!data || data.agents.length === 0) {
    return (
      <p className="px-4 py-3 text-sm text-[var(--color-muted)]">
        No data for this month.
      </p>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-[var(--color-border)]">
            <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              Agent
            </th>
            <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              Tokens in
            </th>
            <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              Tokens out
            </th>
            <th className="px-4 py-2 text-right text-xs font-semibold uppercase tracking-wide text-[var(--color-muted)]">
              Total
            </th>
          </tr>
        </thead>
        <tbody>
          {data.agents.map((row) => (
            <tr
              key={row.agent_id}
              className="border-b border-[var(--color-border)]/50 last:border-0 hover:bg-[var(--color-surface-2)] transition-colors"
            >
              <td className="px-4 py-2.5 text-[var(--color-secondary)] font-medium">
                {row.agent_name}
              </td>
              <td className="px-4 py-2.5 text-right text-[var(--color-muted)] font-mono text-xs">
                {row.tokens_in.toLocaleString()}
              </td>
              <td className="px-4 py-2.5 text-right text-[var(--color-muted)] font-mono text-xs">
                {row.tokens_out.toLocaleString()}
              </td>
              <td className="px-4 py-2.5 text-right text-[var(--color-secondary)] font-mono text-xs font-medium">
                {row.tokens_total.toLocaleString()}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ── AuditLogSection ───────────────────────────────────────────────────────────

function AuditLogSection() {
  const role = useAuthStore((s) => s.role)

  const { data: auditEntries = [], isLoading, isError } = useQuery<AuditEntry[]>({
    queryKey: auditLogQueryKeys.list(),
    queryFn: fetchAuditLog,
    staleTime: 30_000,
    refetchInterval: 60_000,
    enabled: role === 'admin',
  })

  if (role !== 'admin') {
    return <p className="px-4 py-3 text-xs text-[var(--color-muted)]">Audit log requires admin access.</p>
  }

  if (isLoading) {
    return (
      <p className="px-4 py-3 text-xs text-[var(--color-muted)]">Loading audit log…</p>
    )
  }

  if (isError) {
    return (
      <p className="px-4 py-3 text-xs text-[var(--color-muted)]">Audit log unavailable.</p>
    )
  }

  if (auditEntries.length === 0) {
    return (
      <p className="px-4 py-3 text-xs text-[var(--color-muted)]">No audit entries.</p>
    )
  }

  return (
    <div className="divide-y divide-[var(--color-border)]">
      {auditEntries.map((entry) => (
        <div key={`${entry.timestamp}-${entry.event}`} className="px-4 py-2 flex items-start gap-3 text-xs">
          <span className="text-[var(--color-muted)] shrink-0 font-mono">
            {new Date(entry.timestamp).toLocaleString()}
          </span>
          <span className="text-[var(--color-secondary)] flex-1 break-all">
            {entry.event}
            {entry.tool && <span className="text-[var(--color-muted)]"> — {entry.tool}</span>}
            {entry.command && <span className="text-[var(--color-muted)]"> — {entry.command}</span>}
          </span>
          {entry.decision && (
            <span className={entry.decision === 'allow' ? 'text-green-400 shrink-0' : 'text-red-400 shrink-0'}>
              {entry.decision}
            </span>
          )}
        </div>
      ))}
    </div>
  )
}

// ── AutomationsScreen ─────────────────────────────────────────────────────────

export function AutomationsScreen() {
  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      {/* Page header */}
      <div className="px-4 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] sticky top-0 z-10">
        <h2 className="font-headline text-lg font-semibold text-[var(--color-secondary)]">
          Automations
        </h2>
        <p className="text-xs text-[var(--color-muted)] mt-0.5">
          Trigger → Action rules that run agents on a schedule. Read-only — manage rules from the Command Center.
        </p>
      </div>

      {/* Trigger → Action rules section (read-only) */}
      <section className="border-b border-[var(--color-border)]">
        <div className="px-4 py-2.5">
          <h3 className="text-xs font-semibold uppercase tracking-widest text-[var(--color-muted)]">
            Rules
          </h3>
        </div>
        <AutomationRulesSection />
      </section>

      {/* Token Usage section */}
      <section className="border-b border-[var(--color-border)]">
        <div className="px-4 py-2.5 border-b border-[var(--color-border)]">
          <h3 className="text-xs font-semibold uppercase tracking-widest text-[var(--color-muted)]">
            Token Usage (this month)
          </h3>
        </div>
        <TokenUsageSection />
      </section>

      {/* Audit Log section (FR-021) */}
      <section>
        <div className="px-4 py-2.5 border-b border-[var(--color-border)]">
          <h3 className="text-xs font-semibold uppercase tracking-widest text-[var(--color-muted)]">
            Audit Log
          </h3>
        </div>
        <AuditLogSection />
      </section>
    </div>
  )
}
