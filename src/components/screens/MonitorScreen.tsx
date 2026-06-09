import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Plus } from '@phosphor-icons/react'
import { SchedulesList } from '@/components/command-center/SchedulesList'
import { ScheduleFormSheet } from '@/components/command-center/ScheduleFormSheet'
import { fetchTokenStats, tokenStatsQueryKeys, fetchAuditLog } from '@/lib/api'
import type { AuditEntry } from '@/lib/api'

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
  const { data: auditEntries = [], isLoading: auditLoading, isError: auditError } = useQuery<AuditEntry[]>({
    queryKey: ['audit-log'],
    queryFn: fetchAuditLog,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })

  if (auditLoading) {
    return (
      <p className="px-4 py-3 text-xs text-[var(--color-muted)]">Loading audit log…</p>
    )
  }

  if (auditError) {
    return (
      <p className="px-4 py-3 text-xs text-[var(--color-muted)]">Audit log unavailable.</p>
    )
  }

  if (auditEntries.length === 0) {
    return (
      <p className="px-4 py-3 text-xs text-[var(--color-muted)]">No audit events recorded yet.</p>
    )
  }

  return (
    <div className="divide-y divide-[var(--color-border)]">
      {auditEntries.map((entry, i) => (
        <div key={i} className="px-4 py-2 flex items-start gap-3 text-xs">
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

// ── MonitorScreen ─────────────────────────────────────────────────────────────

export function MonitorScreen() {
  const [creatingSchedule, setCreatingSchedule] = useState(false)

  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      {/* Page header */}
      <div className="px-4 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] sticky top-0 z-10">
        <h2 className="font-headline text-lg font-semibold text-[var(--color-secondary)]">
          Monitor
        </h2>
      </div>

      {/* Activity section (FR-021) */}
      <section className="border-b border-[var(--color-border)]">
        <div className="px-4 py-2.5 border-b border-[var(--color-border)]">
          <h3 className="text-xs font-semibold uppercase tracking-widest text-[var(--color-muted)]">
            Activity
          </h3>
        </div>
        <p className="px-4 py-3 text-sm text-[var(--color-muted)]">
          No recent activity.
        </p>
      </section>

      {/* Schedules section */}
      <section className="border-b border-[var(--color-border)]">
        <div className="flex items-center justify-between px-4 py-2.5">
          <h3 className="text-xs font-semibold uppercase tracking-widest text-[var(--color-muted)]">
            Schedules
          </h3>
          <button
            type="button"
            onClick={() => setCreatingSchedule(true)}
            className="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs font-medium text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors"
          >
            <Plus size={13} />
            New schedule
          </button>
        </div>
        <div className="px-4 py-2">
          <SchedulesList />
        </div>
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

      {/* New schedule slide-over */}
      {creatingSchedule && (
        <ScheduleFormSheet
          open={true}
          onOpenChange={(open) => {
            if (!open) setCreatingSchedule(false)
          }}
        />
      )}
    </div>
  )
}
