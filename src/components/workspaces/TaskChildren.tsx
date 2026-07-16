/**
 * TaskChildren — nested subtask list that renders under a parent card on the Board.
 *
 * Rendered only when the board altitude is 'show-all'. Shows child tasks
 * as compact read-only rows. They do NOT appear as top-level cards in any
 * column — children are exclusively visible here (Detail #6).
 *
 * Children are loaded lazily via fetchSubtasks when the parent expands
 * (i.e. altitude = show-all). Uses TanStack Query with a stable query key
 * so data is shared across re-renders without duplicate requests.
 */

import { useQuery } from '@tanstack/react-query'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { fetchSubtasks, tasksQueryKeys } from '@/lib/api'
import type { Task } from '@/lib/api'
import { STATUS_COLORS as STATUS_DOT, STATUS_LABELS as STATUS_LABEL } from '@/lib/statusColors'
import { cn } from '@/lib/utils'

interface TaskChildrenProps {
  parentTaskId: string
  /** Pre-loaded subtasks from the parent's rollup (used to avoid re-fetching
   * when already available); if absent, TaskChildren fetches them itself. */
  preloaded?: Task[]
  onChildClick: (task: Task) => void
}

export function TaskChildren({ parentTaskId, preloaded, onChildClick }: TaskChildrenProps) {
  const { data: children = preloaded ?? [], isLoading, isError, refetch } = useQuery({
    // Only fetch if we don't have preloaded data
    queryKey: tasksQueryKeys.subtasks(parentTaskId),
    queryFn: () => fetchSubtasks(parentTaskId),
    staleTime: 15_000,
    enabled: !preloaded,
  })

  if (isLoading) {
    return (
      <div className="mt-2 space-y-1.5 pl-2 border-l-2 border-[var(--color-border)]">
        {[1, 2].map((i) => (
          <div key={i} className="h-5 rounded bg-[var(--color-surface-2)] animate-pulse" />
        ))}
      </div>
    )
  }

  // A failed fetch must not render identically to "this task genuinely has
  // no children" (empty → null below) — that would silently hide subtasks
  // that actually exist. Give the operator a distinct, visible error state
  // with a way to retry, scaled to fit the compact nested-list context.
  if (isError) {
    return (
      <div className="mt-2 pl-2 border-l-2 border-[var(--color-error)]/40">
        <button tabIndex={0}
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            refetch()
          }}
          className="flex items-center gap-1.5 rounded px-1.5 py-1 text-[11px] text-[var(--color-error)] hover:bg-[var(--color-surface-2)] transition-colors"
        >
          <ArrowsClockwise size={11} />
          Couldn&apos;t load subtasks — Retry
        </button>
      </div>
    )
  }

  if (children.length === 0) return null

  return (
    <div
      className="mt-2 space-y-1 pl-2 border-l-2 border-[var(--color-border)]"
      role="list"
      aria-label="Subtasks"
    >
      {children.map((child) => (
        <button tabIndex={0}
          key={child.id}
          type="button"
          role="listitem"
          onClick={(e) => {
            e.stopPropagation()
            onChildClick(child)
          }}
          className={cn(
            'w-full flex items-center gap-1.5 rounded px-1.5 py-1 text-left',
            'text-[11px] text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            'hover:bg-[var(--color-surface-2)] transition-colors',
          )}
          aria-label={`Subtask: ${child.title} — ${STATUS_LABEL[child.status]}`}
        >
          {/* Status dot */}
          <span
            className="flex-shrink-0 rounded-full"
            style={{
              width: 6,
              height: 6,
              backgroundColor: STATUS_DOT[child.status],
            }}
          />
          <span className="flex-1 truncate leading-tight">{child.title}</span>
          <span
            className="flex-shrink-0 text-[10px] font-medium"
            style={{ color: STATUS_DOT[child.status] }}
          >
            {STATUS_LABEL[child.status]}
          </span>
        </button>
      ))}
    </div>
  )
}
