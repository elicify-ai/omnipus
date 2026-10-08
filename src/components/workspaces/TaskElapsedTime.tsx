import { useEffect, useState } from 'react'
import type { Task } from '@/lib/api'
import { executionTimeLabel, taskExecutionSeconds } from './taskExecution'

/** A seconds-granularity display over existing generated execution timestamps.
 * Only a live run owns a timer; terminal durations always use completed_at.
 */
export function TaskElapsedTime({ task, live = true, separator = false }: { task: Pick<Task, 'status' | 'started_at' | 'completed_at'>; live?: boolean; separator?: boolean }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!live || task.status !== 'in_progress' || !task.started_at || !Number.isFinite(Date.parse(task.started_at))) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [live, task.status, task.started_at])
  const seconds = taskExecutionSeconds(task, now)
  if (seconds === null) return null
  return <span className="inline-flex shrink-0 items-center gap-[var(--space-1)]">
    {separator && <span aria-hidden="true">·</span>}
    <time dateTime={`PT${seconds}S`} aria-label={`Execution time ${executionTimeLabel(seconds)}`} className="tabular-nums">{executionTimeLabel(seconds)}</time>
  </span>
}
