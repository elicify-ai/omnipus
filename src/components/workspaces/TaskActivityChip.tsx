import { useEffect, useState } from 'react'
import { cn } from '@/lib/utils'
import type { Task } from '@/lib/api'
import { useToolApprovalStore, type PendingToolApproval } from '@/store/toolApproval'

// ── Last activity on a running task (founder decision 2026-09-14) ───────────
//
// Long-running work must be visible without a fixed time limit. A task attempt
// in UAT E-15c ran for 48 minutes, with the model reasoning for up to ~21
// minutes per call and nothing on screen; from the outside it looked hung.
// `Task.last_activity_at` is the server's read-time answer to "when did this
// run last do anything" (streamed reasoning or tool-call bytes, or a
// transcript write), and this chip renders its age while the task is in
// progress: "In progress · last activity 5 s ago".
//
// The age ticks locally once a second; the timestamp itself refreshes with the
// tasks query (the board already refetches every 15 s), so no new polling or
// frames are added for it.

/**
 * Age at which the chip switches to the warning tone. Mirrors the server's
 * default provider silence limit (`model_list[].stream_stall_timeout`, 300 s):
 * past it, the task is not streaming anything from the model — it is running
 * a long tool, waiting between attempts, or stuck. The text stays factual
 * either way; only the tone changes.
 */
export const TASK_ACTIVITY_STALE_MS = 5 * 60_000

/** "just now", "5 s ago", "12 min ago", "3 h ago", "2 d ago". */
export function formatActivityAge(ageMs: number): string {
  const seconds = Math.floor(Math.max(0, ageMs) / 1000)
  if (seconds < 1) return 'just now'
  if (seconds < 60) return `${seconds} s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} h ago`
  return `${Math.floor(hours / 24)} d ago`
}

export interface TaskActivity {
  /** Relative age, e.g. "5 s ago". */
  age: string
  /** True once the last activity is at least TASK_ACTIVITY_STALE_MS old. */
  stale: boolean
}

/**
 * The activity to show for `task` at `nowMs`, or null when there is nothing
 * honest to show: the task is not in progress, or the server reported no
 * activity evidence (absent or unparseable `last_activity_at`).
 */
export function taskActivity(
  task: Pick<Task, 'status' | 'last_activity_at'>,
  nowMs: number,
): TaskActivity | null {
  if (task.status !== 'in_progress' || !task.last_activity_at) return null
  const at = Date.parse(task.last_activity_at)
  if (Number.isNaN(at)) return null
  const ageMs = nowMs - at
  return { age: formatActivityAge(ageMs), stale: ageMs >= TASK_ACTIVITY_STALE_MS }
}

// ── Waiting for an approval (founder decision 2026-09-15) ───────────────────
//
// An "ask" tool inside a task run asks the operator: the run registers a
// normal tool approval and waits for it server-side, with or without a browser
// attached. Its last activity then goes quiet for as long as nobody answers,
// which on its own reads as a hung task. The approval queue is the source of
// truth for the wait — every tab receives every tool_approval_required frame,
// and the session_state snapshot restores the queue after a reload — so the
// chip names the wait instead of a stale age. The approval is matched on the
// task run's own session; a delegated helper's approval carries the helper's
// session and still shows in the approval dialog and the cross-workspace
// banner, just not on this chip.

/** The pending approval `task`'s run is waiting on, or null. */
export function taskAwaitingApproval(
  task: Pick<Task, 'status' | 'session_id'>,
  queue: readonly Pick<PendingToolApproval, 'sessionId' | 'toolName'>[],
): Pick<PendingToolApproval, 'sessionId' | 'toolName'> | null {
  if (task.status !== 'in_progress' || !task.session_id) return null
  return queue.find((approval) => approval.sessionId === task.session_id) ?? null
}

/** Current time, re-read every `intervalMs` while `active`; idle otherwise. */
function useNowWhile(active: boolean, intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return undefined
    const id = window.setInterval(() => setNow(Date.now()), intervalMs)
    return () => window.clearInterval(id)
  }, [active, intervalMs])
  return now
}

interface TaskActivityChipProps {
  task: Pick<Task, 'status' | 'last_activity_at' | 'session_id'>
  /**
   * 'card' (default) renders "In progress · last activity 5 s ago" in its own
   * row. 'panel' renders "Last activity 5 s ago" inline, for placement next to
   * a status badge that already says the task is in progress.
   */
  variant?: 'card' | 'panel'
}

export function TaskActivityChip({ task, variant = 'card' }: TaskActivityChipProps) {
  const queue = useToolApprovalStore((s) => s.queue)
  const waiting = taskAwaitingApproval(task, queue)
  const visible = !waiting && task.status === 'in_progress' && !!task.last_activity_at
  const now = useNowWhile(visible, 1000)

  if (waiting) {
    const waitingChip = (
      <span
        data-testid="task-awaiting-approval"
        className="rounded-full px-[var(--space-2)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] font-medium bg-[var(--color-warning)]/10 text-[color:var(--color-warning)]"
      >
        {variant === 'card'
          ? `In progress · waiting for your approval to use ${waiting.toolName}`
          : `Waiting for your approval to use ${waiting.toolName}`}
      </span>
    )
    if (variant === 'panel') return waitingChip
    return <div className="mt-[var(--space-2)] flex items-center gap-[var(--space-1)]">{waitingChip}</div>
  }

  const activity = taskActivity(task, now)
  if (!activity) return null

  const chip = (
    <span
      data-testid="task-last-activity"
      data-stale={activity.stale ? 'true' : 'false'}
      className={cn(
        'rounded-full px-[var(--space-2)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] font-medium',
        activity.stale
          ? 'bg-[var(--color-warning)]/10 text-[color:var(--color-warning)]'
          : 'bg-[var(--color-surface-2)] text-[var(--color-muted)]',
      )}
    >
      {variant === 'card' ? `In progress · last activity ${activity.age}` : `Last activity ${activity.age}`}
    </span>
  )

  if (variant === 'panel') return chip
  return <div className="mt-[var(--space-2)] flex items-center gap-[var(--space-1)]">{chip}</div>
}
