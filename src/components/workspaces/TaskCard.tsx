import { cn } from '@/lib/utils'
import type { Task, Agent, Milestone } from '@/lib/api'
import { CheckSquare } from '@phosphor-icons/react'
import { RollupBadge } from './RollupBadge'
import { TaskChildren } from './TaskChildren'
import type { BoardAltitude } from '@/store/workspacesStore'
import type { DraggableAttributes, DraggableSyntheticListeners } from '@dnd-kit/core'

// Priority badge config: P1 red, P2 orange, P3 yellow, P4 blue, P5 muted
export const PRIORITY_BADGE: Record<number, { label: string; className: string }> = {
  1: { label: 'P1', className: 'bg-red-500/20 text-red-400 border-red-500/30' },
  2: { label: 'P2', className: 'bg-orange-500/20 text-orange-400 border-orange-500/30' },
  3: { label: 'P3', className: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30' },
  4: { label: 'P4', className: 'bg-blue-500/20 text-blue-400 border-blue-500/30' },
  5: { label: 'P5', className: 'bg-[var(--color-muted)]/20 text-[var(--color-muted)] border-[var(--color-muted)]/30' },
}

/**
 * dnd-kit drag wiring for a single card — all three fields come from one
 * `useDraggable` call, so they are all-or-nothing in practice; grouping them
 * into one optional prop (rather than three independently-optional props)
 * makes that invariant explicit at the type level instead of by convention.
 *
 * `listeners` is narrowed to the non-undefined `SyntheticListenerMap` (dnd-kit's
 * own `DraggableSyntheticListeners = SyntheticListenerMap | undefined`) — a
 * `TaskCardDrag` with `drag` present but `listeners` undefined would be
 * representable otherwise, and that combination is exactly the "Space to
 * move" aria-label promising a dead key: dnd-kit's `useDraggable` returns
 * `listeners: undefined` only while `disabled: true` (never set here), but
 * the type itself doesn't rule it out. BoardView's DraggableTaskCard collapses
 * both optionality levels before constructing this prop (see `drag={listeners
 * ? {...} : undefined}` there) so a real, non-undefined `TaskCardDrag` always
 * carries real, non-undefined listeners.
 */
export interface TaskCardDrag {
  attributes: DraggableAttributes
  listeners: NonNullable<DraggableSyntheticListeners>
  activatorRef: (element: HTMLElement | null) => void
}

interface TaskCardProps {
  task: Task
  milestones?: Milestone[]
  /**
   * Agents cache — required for delegation roll-up avatar rendering.
   * When absent, roll-up avatars fall back to Robot icon + status colour.
   */
  agents?: Agent[]
  /**
   * Board altitude. 'top-level' (default) = children collapsed;
   * 'show-all' = children expanded inline under this card.
   */
  altitude?: BoardAltitude
  onClick: () => void
  onChildClick?: (child: Task) => void
  /**
   * dnd-kit drag wiring — supplied only by BoardView's DraggableTaskCard,
   * whose own wrapper `<div>` is a plain, non-interactive measurement node
   * (see BoardView.tsx). Applying these to THIS component's own root keeps
   * each card a single tab stop (WCAG 4.1.2) instead of nesting a second
   * focusable/role="button" element around it.
   */
  drag?: TaskCardDrag
}

export function TaskCard({
  task,
  milestones = [],
  agents = [],
  altitude = 'top-level',
  onClick,
  onChildClick,
  drag,
}: TaskCardProps) {
  const priority = task.priority ?? 3
  const badge = PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]
  const milestone = task.milestone_id ? milestones.find((m) => m.id === task.milestone_id) : null
  const todos = task.todos ?? []
  const doneTodos = todos.filter((t) => t.status === 'completed').length
  const rollup = task.rollup ?? []
  const hasRollup = rollup.length > 0
  const showChildren = altitude === 'show-all'
  const isDraggable = Boolean(drag)

  // dnd-kit hands back its pointer/keyboard activators typed as bare
  // `Function`s (DraggableSyntheticListeners = Record<string, Function>);
  // narrow them to the real per-event signature so they can be invoked and
  // composed with this card's own onKeyDown below. The KeyboardSensor's
  // onKeyDown activator lifts the card on Space — it must keep firing
  // alongside (not instead of) TaskCard's own Enter-to-open handling.
  const dragKeyDown = drag?.listeners.onKeyDown as
    | ((event: React.KeyboardEvent<HTMLDivElement>) => void)
    | undefined
  const dragPointerDown = drag?.listeners.onPointerDown as
    | ((event: React.PointerEvent<HTMLDivElement>) => void)
    | undefined

  return (
    <div
      ref={drag?.activatorRef}
      role="button"
      tabIndex={0}
      aria-disabled={drag?.attributes['aria-disabled']}
      aria-pressed={drag?.attributes['aria-pressed']}
      aria-roledescription={drag?.attributes['aria-roledescription']}
      aria-describedby={drag?.attributes['aria-describedby']}
      aria-label={isDraggable ? `${task.title} — Enter to open, Space to move` : undefined}
      onClick={onClick}
      onPointerDown={dragPointerDown}
      onKeyDown={(e) => {
        dragKeyDown?.(e)
        // Space is reserved for dnd-kit's keyboard-drag lift when the card IS
        // draggable (see BoardView's KeyboardSensor `keyboardCodes` override)
        // — Enter opens the task there. When there's no drag context at all
        // (e.g. ExecutionView's read-only cards), Space has no other job, so
        // a role="button" that ignores it would break WCAG 4.1.2 — let it
        // open the task too.
        if (e.key === 'Enter' || (!isDraggable && e.key === ' ')) {
          e.preventDefault()
          onClick()
        }
      }}
      className={cn(
        'rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 cursor-pointer',
        'transition-colors hover:border-[var(--color-border)]/60 hover:bg-[var(--color-surface-2)]/40',
        hasRollup && 'border-[var(--color-accent)]/30',
      )}
    >
      {/* Top row: priority badge + title */}
      <div className="flex items-start gap-2">
        <span
          className={cn(
            'flex-shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-bold leading-tight mt-0.5',
            badge.className,
          )}
        >
          {badge.label}
        </span>
        <p className="flex-1 text-sm font-medium text-[var(--color-secondary)] leading-snug line-clamp-2">
          {task.title}
        </p>
      </div>

      {/* Todos checklist progress */}
      {todos.length > 0 && (
        <div className="mt-2 flex items-center gap-1.5 text-[10px] text-[var(--color-muted)]">
          <CheckSquare size={11} />
          <span>{doneTodos}/{todos.length}</span>
        </div>
      )}

      {/* Delegation roll-up badge (only on parent cards with active sub-agent runs) */}
      {hasRollup && (
        <RollupBadge rollup={rollup} agents={agents} />
      )}

      {/* Bottom row: agent badge + milestone tag */}
      {(task.agent_name || task.agent_id || milestone) && (
        <div className="mt-2 flex items-center gap-1.5 flex-wrap">
          {(task.agent_name || task.agent_id) && (
            <span className="rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-2 py-0.5 text-[10px] text-[var(--color-muted)]">
              {task.agent_name ?? task.agent_id}
            </span>
          )}
          {milestone && (
            <span className="rounded-full bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/20 px-2 py-0.5 text-[10px] text-[var(--color-accent)]">
              {milestone.name}
            </span>
          )}
        </div>
      )}

      {/* Nested children — only when altitude = 'show-all' */}
      {showChildren && (
        <TaskChildren
          parentTaskId={task.id}
          onChildClick={onChildClick ?? onClick}
        />
      )}
    </div>
  )
}
