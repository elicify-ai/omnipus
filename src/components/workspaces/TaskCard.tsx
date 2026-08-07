import { useId } from 'react'
import { cn } from '@/lib/utils'
import type { Task, Agent, Plan } from '@/lib/api'
import { CheckSquare } from '@phosphor-icons/react'
import { RollupBadge } from './RollupBadge'
import { TaskChildren } from './TaskChildren'
import { TaskActionButton } from './TaskActionButton'
import { taskDisplayColor, taskDisplayLabel } from '@/lib/statusColors'
import type { BoardAltitude } from '@/store/workspacesStore'
import type { DraggableAttributes, DraggableSyntheticListeners } from '@dnd-kit/core'

// ── Goal-loop status affordance (ADR-049 FR-090, SD-C12) ────────────────────
//
// "attempt N/M" is sourced from the real, server-set `Task.attempt_count`
// wire field (contract C17) against `Task.max_attempts` (or the inherited
// PlanningConfig.task_max_attempts default of 3) — never fabricated. The
// "paused" suffix is likewise grounded in real data: a task's owning Plan
// (looked up via `Task.plan_id` in the `plans` prop) reporting
// `state === 'running' && paused_reason` — NOT a fake/always-false flag.
// Standalone (non-plan) task-loop judge-unavailable pauses are only knowable
// live via the `task_status_changed`/`goal_status` WS frames (out of this
// wave's scope — frame consumption lands with US-12); this card renders the
// plan-derived pause honestly and simply omits the "paused" suffix when no
// such data is available, rather than inventing a state.
export const DEFAULT_TASK_MAX_ATTEMPTS = 3

export function goalLoopStatusLabel(
  task: Pick<Task, 'attempt_count' | 'max_attempts'>,
  paused: boolean,
): string | null {
  if (task.attempt_count == null || task.attempt_count <= 0) return null
  const max = task.max_attempts ?? DEFAULT_TASK_MAX_ATTEMPTS
  return `attempt ${task.attempt_count}/${max}${paused ? ' · paused' : ''}`
}

// Priority badge config: P1 red, P2 orange, P3 yellow, P4 blue, P5 muted
// Flat priority pills — tint fill + colour, no outline (minimalist flat design).
export const PRIORITY_BADGE: Record<number, { label: string; className: string }> = {
  1: { label: 'P1', className: 'bg-red-500/20 text-red-400' },
  2: { label: 'P2', className: 'bg-orange-500/20 text-orange-400' },
  3: { label: 'P3', className: 'bg-yellow-500/20 text-yellow-400' },
  4: { label: 'P4', className: 'bg-blue-500/20 text-blue-400' },
  5: { label: 'P5', className: 'bg-[var(--color-muted)]/20 text-[var(--color-muted)]' },
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
  /**
   * Plans in this workspace (ADR-049) — used only to look up the owning
   * Plan's `paused_reason` for the goal-loop "paused" chip via `task.plan_id`.
   * When absent, the paused suffix simply never renders (no fake state).
   */
  plans?: Plan[]
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
  /**
   * Render the ADR-052 §6.8 ▶/■ action button (`TaskActionButton`). Default
   * true. BoardView's `DragOverlay` sets this false on its purely-visual
   * drag-ghost clone — an interactive-looking action button following the
   * cursor mid-drag would be confusing, and the ghost is `aria-hidden`
   * anyway (never keyboard/pointer reachable).
   */
  showActions?: boolean
}

export function TaskCard({
  task,
  plans = [],
  agents = [],
  altitude = 'top-level',
  onClick,
  onChildClick,
  drag,
  showActions = true,
}: TaskCardProps) {
  const priority = task.priority ?? 3
  const badge = PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]
  const tags = task.tags ?? []
  const visibleTags = tags.slice(0, 3)
  const overflowTagCount = tags.length - visibleTags.length
  const owningPlan = task.plan_id ? plans.find((p) => p.id === task.plan_id) : undefined
  const planPaused = owningPlan?.state === 'running' && !!owningPlan.paused_reason
  const goalLoopLabel = goalLoopStatusLabel(task, planPaused)
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

  // "Enter to open, Space to move" used to be baked into aria-label, which
  // REPLACED the accessible name entirely (name = title + key metadata comes
  // from the card's own visible content — priority badge, title, checklist
  // count, agent/milestone tags — when aria-label is absent) and duplicated
  // dnd-kit's own drag instructions (`drag.attributes['aria-describedby']`,
  // wired to BOARD_SCREEN_READER_INSTRUCTIONS in BoardView, which already
  // explains the Space-to-lift gesture). Moving it into aria-describedby
  // instead keeps the name = content, and combines with dnd-kit's own
  // description id (rather than replacing it) so neither hint is lost.
  const enterSpaceHintId = useId()
  const describedBy = isDraggable
    ? [drag?.attributes['aria-describedby'], enterSpaceHintId].filter(Boolean).join(' ')
    : undefined

  return (
    <div
      ref={drag?.activatorRef}
      role="button"
      tabIndex={0}
      aria-disabled={drag?.attributes['aria-disabled']}
      aria-pressed={drag?.attributes['aria-pressed']}
      aria-roledescription={drag?.attributes['aria-roledescription']}
      aria-describedby={describedBy}
      onClick={onClick}
      onPointerDown={dragPointerDown}
      onKeyDown={(e) => {
        // Ignore keydowns that bubbled up from a nested interactive control
        // (a subtask row button in TaskChildren, or its error-state Retry
        // button) — this card's own Enter/Space handling only applies when
        // the CARD ITSELF is the event target, otherwise preventDefault()
        // below cancels the nested control's own native Enter-activation and
        // hijacks the keypress into opening THIS (parent) card instead.
        if (e.target !== e.currentTarget) return
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
        'group relative rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 cursor-pointer',
        'transition-colors hover:border-[var(--color-border)]/60 hover:bg-[var(--color-surface-2)]/40',
        hasRollup && 'border-[var(--color-accent)]/30',
      )}
    >
      {isDraggable && (
        <span id={enterSpaceHintId} className="sr-only">
          Enter to open, Space to move.
        </span>
      )}

      {/* ADR-052 §6.8 ▶/■ action button — hover-revealed on pointer-fine
          devices, always visible on touch (mirrors PlansFilterBand's tile
          action row). Renders nothing when the task offers no action
          (TaskActionButton itself returns null for e.g. an in-plan idle
          task or a `done` task). */}
      {showActions && (
        <div className="absolute right-1.5 top-1.5 z-10 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100">
          <TaskActionButton task={task} />
        </div>
      )}

      {/* Top row: priority badge + title */}
      <div className="flex items-start gap-2">
        <span
          className={cn(
            'flex-shrink-0 rounded px-1.5 py-0.5 text-[10px] font-bold leading-tight mt-0.5',
            badge.className,
          )}
        >
          {badge.label}
        </span>
        {/* UAT Finding 2 fix: a long UNBROKEN title (no spaces — e.g. the
            200-char maxLength case) used to blow out this flex chain. `<p>`
            is a flex item (`flex-1`) whose default `min-width: auto`
            resolves to its CONTENT's min-content size — for an unbroken
            string that's the string's full rendered width, which cascaded
            up through every ancestor flex container (this row → the card →
            StatusColumn, a flex item of the columns ROW) and inflated the
            column's own width well past its `min-w-[162px]` floor, pushing
            later columns off-screen with no way to scroll to them.
            `min-w-0` removes the auto floor on this item; `wrap-anywhere`
            (overflow-wrap: anywhere) is the one wrapping mode the spec
            requires browsers to factor into MIN-CONTENT sizing itself (unlike
            `break-word`, which they're allowed to ignore for min-content) —
            together they cap this paragraph's contribution at a single
            glyph's width, so the column can never be forced wider by title
            content. `line-clamp-2` still truncates (with the native `title`
            tooltip below carrying the full text) — wrap-anywhere just makes
            sure that clamp actually happens within the card's own width. */}
        <p
          className="min-w-0 flex-1 text-sm font-medium text-[var(--color-secondary)] leading-snug line-clamp-2 wrap-anywhere pr-6"
          title={task.title}
        >
          {task.title}
        </p>
      </div>

      {/* Cancelled/Failed marker (ADR-052 FR-015/US-8) — a `failed` task
          gets an explicit state chip so a user-Stopped (orange "Cancelled")
          task reads as distinct from a genuine failure (red "Failed") within
          the same Failed board column. */}
      {task.status === 'failed' && (
        <div className="mt-2 flex items-center gap-1.5">
          <span
            className="rounded-full px-2 py-0.5 text-[10px] font-semibold"
            style={{ color: taskDisplayColor(task), backgroundColor: `${taskDisplayColor(task)}1a` }}
          >
            {taskDisplayLabel(task)}
          </span>
        </div>
      )}

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

      {/* Bottom row: agent badge */}
      {(task.agent_name || task.agent_id) && (
        <div className="mt-2 flex items-center gap-1.5 flex-wrap">
          <span className="rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-2 py-0.5 text-[10px] text-[var(--color-muted)]">
            {task.agent_name ?? task.agent_id}
          </span>
        </div>
      )}

      {/* Tag chips (ADR-049 — replaces the milestone chip, SD-C14). Migrated
          `milestone:<name>` tags render as ordinary chips, verbatim. */}
      {tags.length > 0 && (
        <div className="mt-2 flex items-center gap-1.5 flex-wrap">
          {visibleTags.map((tag) => (
            <span
              key={tag}
              title={tag}
              className="max-w-[100px] truncate rounded-full bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/20 px-2 py-0.5 text-[10px] text-[var(--color-accent)]"
            >
              {tag}
            </span>
          ))}
          {overflowTagCount > 0 && (
            <span className="text-[10px] text-[var(--color-muted)]">+{overflowTagCount}</span>
          )}
        </div>
      )}

      {/* Goal-loop status affordance (FR-090) — "attempt N/M" (+"· paused"
          when the owning plan reports paused_reason while running). */}
      {goalLoopLabel && (
        <div className="mt-2 flex items-center gap-1.5">
          <span
            className={cn(
              'rounded-full px-2 py-0.5 text-[10px] font-medium',
              planPaused
                ? 'bg-[var(--color-warning)]/10 text-[color:var(--color-warning)]'
                : 'bg-[var(--color-surface-2)] text-[var(--color-muted)]',
            )}
          >
            {goalLoopLabel}
          </span>
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
