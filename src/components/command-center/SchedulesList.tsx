import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Clock, Play, Pause, Trash, ArrowSquareOut, PencilSimple } from '@phosphor-icons/react'
import { Circle } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  fetchSchedules,
  fetchAgents,
  runSchedule,
  pauseSchedule,
  deleteSchedule,
  isApiError,
} from '@/lib/api'
import type { Schedule, ScheduleTrigger, ScheduleRunRecord } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { SkeletonList, EmptyState, ErrorState } from '@/components/shared/ListStates'
import { ScheduleFormSheet } from './ScheduleFormSheet'

interface SchedulesListProps {
  // When set, the feed is filtered to schedules owned by this agent (agent profile tab).
  agentId?: string
}

// triggerSummary renders a one-line human description of when a schedule fires.
// Once (at) / Every Nm (every) / cron expr (cron) — mirrors the spec's card copy.
export function triggerSummary(trigger: ScheduleTrigger): string {
  switch (trigger.kind) {
    case 'at': {
      if (!trigger.at_ms) return 'Once'
      return `Once · ${new Date(trigger.at_ms).toLocaleString()}`
    }
    case 'every': {
      if (!trigger.every_ms) return 'Every'
      return `Every ${formatInterval(trigger.every_ms)}`
    }
    case 'cron':
      return trigger.cron_expr ? `Cron · ${trigger.cron_expr}` : 'Cron'
    default:
      return 'Schedule'
  }
}

function formatInterval(ms: number): string {
  const sec = Math.round(ms / 1000)
  if (sec % 86400 === 0) return `${sec / 86400}d`
  if (sec % 3600 === 0) return `${sec / 3600}h`
  if (sec % 60 === 0) return `${sec / 60}m`
  return `${sec}s`
}

function formatTime(ms?: number): string {
  if (!ms) return '—'
  return new Date(ms).toLocaleString()
}

// statusBadge maps a schedule's last-run status to a Badge variant.
function lastStatusBadge(status?: string): { variant: 'success' | 'error' | 'warning' | 'muted'; label: string } {
  switch (status) {
    case 'ok':
      return { variant: 'success', label: 'Last: ok' }
    case 'error':
      return { variant: 'error', label: 'Last: error' }
    case 'timeout':
      return { variant: 'error', label: 'Last: timeout' }
    case 'skipped':
      return { variant: 'warning', label: 'Last: skipped' }
    default:
      return { variant: 'muted', label: 'Never run' }
  }
}

function runStatusVariant(status: ScheduleRunRecord['status']): 'success' | 'error' | 'warning' {
  if (status === 'ok') return 'success'
  if (status === 'skipped') return 'warning'
  return 'error'
}

export function SchedulesList({ agentId }: SchedulesListProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [detailSchedule, setDetailSchedule] = useState<Schedule | null>(null)
  const [editSchedule, setEditSchedule] = useState<Schedule | null>(null)

  const { data: schedules = [], isLoading, isError } = useQuery({
    queryKey: ['schedules'],
    queryFn: fetchSchedules,
  })

  // Agent id → name lookup for the owner label on each card.
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })
  const agentName = (id: string) => agents.find((a) => a.id === id)?.name ?? id

  const visible = agentId ? schedules.filter((s) => s.owner_agent_id === agentId) : schedules

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['schedules'] })

  const { mutate: doRun } = useMutation({
    mutationFn: (id: string) => runSchedule(id),
    onSuccess: (result) => {
      invalidate()
      if (result.status === 'skipped') {
        addToast({ message: 'Run skipped — a run is already in progress.', variant: 'warning' })
      } else if (result.status === 'ok') {
        addToast({ message: 'Schedule run started.', variant: 'success' })
      } else {
        addToast({ message: result.error || `Run ${result.status}.`, variant: 'error' })
      }
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Run failed', variant: 'error' }),
  })

  const { mutate: doPause } = useMutation({
    mutationFn: (id: string) => pauseSchedule(id),
    onSuccess: (s) => {
      invalidate()
      addToast({ message: s.enabled ? 'Schedule resumed' : 'Schedule paused', variant: 'success' })
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Pause failed', variant: 'error' }),
  })

  const { mutate: doDelete } = useMutation({
    mutationFn: (id: string) => deleteSchedule(id),
    onSuccess: () => {
      invalidate()
      addToast({ message: 'Schedule deleted', variant: 'success' })
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Delete failed', variant: 'error' }),
  })

  if (isError) return <ErrorState message="Could not load schedules." />
  if (isLoading) return <SkeletonList />
  if (visible.length === 0) {
    return (
      <EmptyState
        icon={<Clock size={40} weight="thin" />}
        message="No schedules yet."
      />
    )
  }

  return (
    <div className="space-y-2">
      {visible.map((schedule) => {
        const last = lastStatusBadge(schedule.state?.last_status)
        return (
          <div
            key={schedule.id}
            data-testid={`schedule-card-${schedule.id}`}
            role="button"
            tabIndex={0}
            onClick={() => setDetailSchedule(schedule)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                setDetailSchedule(schedule)
              }
            }}
            className="flex items-center gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] cursor-pointer hover:border-[var(--color-accent)]/50 transition-colors"
          >
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <Circle
                  size={7}
                  weight="fill"
                  className={schedule.enabled ? 'text-[var(--color-success)]' : 'text-[var(--color-muted)]'}
                />
                <span className="font-medium text-sm text-[var(--color-secondary)] truncate">
                  {schedule.name}
                </span>
                <Badge variant={schedule.enabled ? 'success' : 'muted'} className="text-[10px]">
                  {schedule.enabled ? 'Enabled' : 'Paused'}
                </Badge>
                <Badge variant="secondary" className="text-[10px]">
                  {schedule.session_mode}
                </Badge>
                <Badge variant={last.variant} className="text-[10px]">
                  {last.label}
                </Badge>
              </div>
              <div className="mt-1 flex items-center gap-2 flex-wrap text-[11px] text-[var(--color-muted)]">
                <span className="flex items-center gap-1">
                  <Clock size={11} />
                  {triggerSummary(schedule.trigger)}
                </span>
                <span aria-hidden>·</span>
                <span>Owner: {agentName(schedule.owner_agent_id)}</span>
                <span aria-hidden>·</span>
                <span>Next: {formatTime(schedule.state?.next_run_at_ms)}</span>
              </div>
            </div>
            <div
              className="flex items-center gap-1 shrink-0"
              onClick={(e) => e.stopPropagation()}
            >
              <IconAction
                label="Run now"
                onClick={() => doRun(schedule.id)}
                icon={<Play size={14} weight="fill" />}
              />
              <IconAction
                label={schedule.enabled ? 'Pause' : 'Resume'}
                onClick={() => doPause(schedule.id)}
                icon={<Pause size={14} />}
              />
              <IconAction
                label="Edit"
                onClick={() => setEditSchedule(schedule)}
                icon={<PencilSimple size={14} />}
              />
              <IconAction
                label="Delete"
                destructive
                onClick={() => {
                  if (window.confirm(`Delete schedule "${schedule.name}"?`)) doDelete(schedule.id)
                }}
                icon={<Trash size={14} />}
              />
            </div>
          </div>
        )
      })}

      {/* Detail slide-over — run history */}
      <ScheduleDetailSheet
        schedule={detailSchedule}
        agentName={detailSchedule ? agentName(detailSchedule.owner_agent_id) : ''}
        onOpenChange={(open) => {
          if (!open) setDetailSchedule(null)
        }}
        runStatusVariant={runStatusVariant}
      />

      {/* Edit form slide-over */}
      {editSchedule && (
        <ScheduleFormSheet
          open={true}
          schedule={editSchedule}
          onOpenChange={(open) => {
            if (!open) setEditSchedule(null)
          }}
        />
      )}
    </div>
  )
}

function IconAction({
  label,
  icon,
  onClick,
  destructive,
}: {
  label: string
  icon: React.ReactNode
  onClick: () => void
  destructive?: boolean
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className={`p-1.5 rounded-md transition-colors ${
        destructive
          ? 'text-[var(--color-muted)] hover:text-[var(--color-error)] hover:bg-[var(--color-surface-2)]'
          : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
      }`}
    >
      {icon}
    </button>
  )
}

function ScheduleDetailSheet({
  schedule,
  agentName,
  onOpenChange,
  runStatusVariant,
}: {
  schedule: Schedule | null
  agentName: string
  onOpenChange: (open: boolean) => void
  runStatusVariant: (status: ScheduleRunRecord['status']) => 'success' | 'error' | 'warning'
}) {
  // runs are stored newest-first per the contract; render as-is.
  const runs = schedule?.runs ?? []
  return (
    <Sheet open={Boolean(schedule)} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[480px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto"
      >
        {schedule && (
          <>
            <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
              <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)]">
                {schedule.name}
              </SheetTitle>
            </SheetHeader>

            <div className="pt-5 space-y-5">
              <div className="space-y-1.5 text-xs text-[var(--color-muted)]">
                <div>Owner: <span className="text-[var(--color-secondary)]">{agentName}</span></div>
                <div>Trigger: <span className="text-[var(--color-secondary)]">{triggerSummary(schedule.trigger)}</span></div>
                <div>Session mode: <span className="text-[var(--color-secondary)]">{schedule.session_mode}</span></div>
                <div>Next run: <span className="text-[var(--color-secondary)]">{formatTime(schedule.state?.next_run_at_ms)}</span></div>
              </div>

              <div>
                <h3 className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wider mb-2">
                  Run history
                </h3>
                {runs.length === 0 ? (
                  <p className="text-xs text-[var(--color-muted)]">No runs recorded yet.</p>
                ) : (
                  <div className="space-y-2">
                    {runs.map((run, i) => (
                      <div
                        key={`${run.ran_at_ms}-${i}`}
                        data-testid="schedule-run-row"
                        className="flex items-center gap-3 p-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)]"
                      >
                        <Badge variant={runStatusVariant(run.status)} className="text-[10px] shrink-0">
                          {run.status}
                        </Badge>
                        <div className="flex-1 min-w-0 text-[11px] text-[var(--color-muted)]">
                          <div className="text-[var(--color-secondary)]">{formatTime(run.ran_at_ms)}</div>
                          <div>
                            {run.duration_ms != null ? `${(run.duration_ms / 1000).toFixed(1)}s` : '—'}
                            {run.error ? ` · ${run.error}` : ''}
                          </div>
                        </div>
                        {run.session_id && (
                          <a
                            href={`/sessions/${encodeURIComponent(run.session_id)}`}
                            className="flex items-center gap-1 text-[11px] text-[var(--color-accent)] hover:text-[var(--color-accent-hover)] shrink-0"
                            onClick={(e) => e.stopPropagation()}
                          >
                            Session
                            <ArrowSquareOut size={11} />
                          </a>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}
