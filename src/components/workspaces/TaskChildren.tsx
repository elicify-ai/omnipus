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
import { fetchSubtasks, tasksQueryKeys } from '@/lib/api'
import type { Task } from '@/lib/api'
import { cn } from '@/lib/utils'

/** Status dot colours — Sovereign Deep 7-state */
const STATUS_DOT: Record<Task['status'], string> = {
  inbox:       '#9ca3af',
  next:        '#3B82F6',
  planning:    '#A855F7',
  in_progress: '#EAB308',
  blocked:     '#F97316',
  done:        '#10b981',
  failed:      '#ef4444',
}

const STATUS_LABEL: Record<Task['status'], string> = {
  inbox:       'Inbox',
  next:        'Next',
  planning:    'Planning',
  in_progress: 'In Progress',
  blocked:     'Blocked',
  done:        'Done',
  failed:      'Failed',
}

interface TaskChildrenProps {
  parentTaskId: string
  /** Pre-loaded subtasks from the parent's rollup (used to avoid re-fetching
   * when already available); if absent, TaskChildren fetches them itself. */
  preloaded?: Task[]
  onChildClick: (task: Task) => void
}

export function TaskChildren({ parentTaskId, preloaded, onChildClick }: TaskChildrenProps) {
  const { data: children = preloaded ?? [], isLoading } = useQuery({
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

  if (children.length === 0) return null

  return (
    <div
      className="mt-2 space-y-1 pl-2 border-l-2 border-[var(--color-border)]"
      role="list"
      aria-label="Subtasks"
    >
      {children.map((child) => (
        <button
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
