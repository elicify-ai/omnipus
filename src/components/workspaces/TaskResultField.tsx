// TaskResultField — the task's completion result, extracted from
// TaskDetailPanel so the calendar's recurring-task EDIT slide-over can show
// a fired recurring task's result too (recurring tasks are excluded from
// Board/List, US-3 — the calendar slide-over is their only surface).
//
// Self-gates: renders nothing unless the resolved status is done/failed AND
// carries a result — identical gate to TaskDetailPanel's `showResult`.
//
// Run-aware (ADR-050 RD8 / task-run-history-spec §4.3): an optional `run`
// prop lets the calendar's occurrence slide-over show THAT run's result
// instead of task.result — a series whose latest fire failed but whose
// clicked occurrence succeeded (or vice versa) must show ITS OWN result, not
// the task-level mirror. Absent → falls back to task.status/task.result
// (unchanged existing behaviour).

import { cn } from '@/lib/utils'
import { useUiStore } from '@/store/ui'
import { Copy } from '@phosphor-icons/react'
import type { Task, TaskRun } from '@/lib/api'

export interface TaskResultFieldProps {
  task: Task
  /** A specific occurrence's resolved run (ADR-050 RD8). See file header. */
  run?: TaskRun
}

export function TaskResultField({ task, run }: TaskResultFieldProps) {
  const { addToast } = useUiStore()

  const status = run ? run.status : task.status
  const result = run ? run.result : task.result
  const isFailed = status === 'failed'
  const showResult = (status === 'done' || status === 'failed') && !!result

  async function handleCopyResult() {
    if (!result) return
    try {
      await navigator.clipboard.writeText(result)
      addToast({ message: 'Result copied to clipboard.', variant: 'success' })
    } catch {
      addToast({ message: 'Failed to copy to clipboard.', variant: 'error' })
    }
  }

  if (!showResult || !result) return null

  return (
    <div className="space-y-1.5">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Result</p>
      <div className={cn('relative', isFailed && 'ring-1 ring-[var(--color-error)]/30 rounded-md')}>
        <pre
          data-testid="task-result-text"
          className="text-xs font-mono text-[var(--color-secondary)] bg-[var(--color-surface-2)] rounded-md p-3 max-h-[200px] overflow-y-auto whitespace-pre-wrap break-words leading-relaxed"
        >
          {result}
        </pre>
        <button tabIndex={0}
          type="button"
          onClick={handleCopyResult}
          className="absolute top-2 right-2 flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-1)] transition-colors"
          aria-label="Copy result"
        >
          <Copy size={11} /> Copy
        </button>
      </div>
    </div>
  )
}
