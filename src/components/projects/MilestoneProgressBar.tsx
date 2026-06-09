import { cn } from '@/lib/utils'
import type { MilestoneWithProgress } from '@/lib/api'

interface MilestoneProgressBarProps {
  milestone: MilestoneWithProgress
}

function daysUntil(dateStr: string): number | null {
  if (!dateStr) return null
  const due = new Date(dateStr)
  if (isNaN(due.getTime())) return null
  const now = new Date()
  now.setHours(0, 0, 0, 0)
  const diff = Math.ceil((due.getTime() - now.getTime()) / (1000 * 60 * 60 * 24))
  return diff
}

function formatDueDate(dateStr: string): string {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) return dateStr
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

export function MilestoneProgressBar({ milestone }: MilestoneProgressBarProps) {
  const progress = milestone.progress ?? 0
  const pct = Math.round(progress * 100)
  const days = milestone.due_date ? daysUntil(milestone.due_date) : null
  const isOverdue = days !== null && days < 0 && pct < 100

  return (
    <div className="flex items-center gap-3 text-xs">
      {/* Milestone name */}
      <span
        className="w-24 truncate font-medium text-[var(--color-secondary)] flex-shrink-0"
        title={milestone.name}
      >
        {milestone.name}
      </span>

      {/* Due date */}
      {milestone.due_date ? (
        <span
          className={cn(
            'w-16 flex-shrink-0',
            isOverdue ? 'text-red-400 font-medium' : 'text-[var(--color-muted)]',
          )}
        >
          {formatDueDate(milestone.due_date)}
        </span>
      ) : (
        <span className="w-16 flex-shrink-0 text-[var(--color-muted)]">—</span>
      )}

      {/* Progress bar */}
      <div className="flex-1 h-1.5 rounded-full bg-[var(--color-surface-2)] overflow-hidden min-w-[60px]">
        <div
          className={cn(
            'h-full rounded-full transition-all',
            pct >= 100 ? 'bg-green-500' : isOverdue ? 'bg-red-400' : 'bg-[var(--color-accent)]',
          )}
          style={{ width: `${pct}%` }}
        />
      </div>

      {/* Progress label */}
      <span className="w-8 text-right text-[var(--color-muted)] flex-shrink-0">
        {pct}%
      </span>

      {/* Days until due */}
      {days !== null && pct < 100 && (
        <span
          className={cn(
            'w-16 text-right flex-shrink-0',
            isOverdue ? 'text-red-400' : 'text-[var(--color-muted)]',
          )}
        >
          {isOverdue ? `${Math.abs(days)}d over` : `${days}d`}
        </span>
      )}
    </div>
  )
}
