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
import { STATUS_LABELS as STATUS_LABEL } from '@/lib/statusColors'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

/**
 * Status-dot / label tint per child status — a literal `switch` over the
 * closed `Task['status']` set, not a dynamic record lookup (the
 * design-system static scanners cannot resolve a style value read out of a
 * record via a runtime key). Each branch is the matching
 * `--status-<name>-foreground` token (`src/styles/tokens.generated.css`),
 * which resolves to the same value `src/design-system/status.ts`'s
 * `statusContract` publishes for that status. Mirrors `BoardView.tsx`'s
 * `statusHeaderColorVar`.
 */
function childStatusColorVar(status: Task['status']): string {
  switch (status) {
    case 'inbox':
      return 'var(--status-inbox-foreground)'
    case 'next':
      return 'var(--status-next-foreground)'
    case 'in_progress':
      return 'var(--status-in-progress-foreground)'
    case 'blocked':
      return 'var(--status-blocked-foreground)'
    case 'done':
      return 'var(--status-done-foreground)'
    case 'failed':
      return 'var(--status-failed-foreground)'
    default:
      return 'var(--status-inbox-foreground)'
  }
}

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
      <div className="mt-[var(--space-2)] space-y-[var(--space-1)] pl-[var(--space-2)] border-l-2 border-[var(--color-border)]">
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
      <div className="mt-[var(--space-2)] pl-[var(--space-2)] border-l-2 border-[var(--color-error)]/40">
        <Button
          variant="ghost"
          onClick={(e) => {
            e.stopPropagation()
            refetch()
          }}
          className="h-auto gap-[var(--space-1)] rounded px-[var(--space-1)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-error)]"
        >
          <ArrowsClockwise size={11} />
          Couldn&apos;t load subtasks — Retry
        </Button>
      </div>
    )
  }

  if (children.length === 0) return null

  return (
    // A native <ul>/<li> pair gives the list/listitem semantics for free —
    // putting `role="listitem"` directly ON the row <button> (the previous
    // shape) REPLACES that button's own implicit role="button" with
    // "listitem" instead of layering the two, so AT stopped announcing the
    // row as pressable at all. Wrapping each row's button in its own <li>
    // keeps exactly one tab stop per row (the button) while the <li> itself
    // carries the listitem semantics.
    <ul className="mt-[var(--space-2)] list-none space-y-[var(--space-1)] pl-[var(--space-2)] border-l-2 border-[var(--color-border)]" aria-label="Subtasks">
      {children.map((child) => (
        <li key={child.id}>
          <Button
            variant="ghost"
            onClick={(e) => {
              e.stopPropagation()
              onChildClick(child)
            }}
            className={cn(
              'h-auto w-full items-center justify-start gap-[var(--space-1)] rounded px-[var(--space-1)] py-[var(--space-1)] text-left',
              'text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
              'hover:bg-[var(--color-surface-2)]',
            )}
            aria-label={`Subtask: ${child.title} — ${STATUS_LABEL[child.status]}`}
          >
            {/* Status dot */}
            <span
              className="flex-shrink-0 rounded-full"
              style={{
                width: 6,
                height: 6,
                backgroundColor: childStatusColorVar(child.status),
              }}
            />
            <span className="flex-1 truncate leading-tight">{child.title}</span>
            <span
              className="flex-shrink-0 text-[length:var(--type-caption-size)] font-medium"
              style={{ color: childStatusColorVar(child.status) }}
            >
              {STATUS_LABEL[child.status]}
            </span>
          </Button>
        </li>
      ))}
    </ul>
  )
}
