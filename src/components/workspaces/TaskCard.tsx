import { cn } from '@/lib/utils'
import type { Task, Agent, Milestone } from '@/lib/api'
import { CheckSquare } from '@phosphor-icons/react'
import { RollupBadge } from './RollupBadge'
import { TaskChildren } from './TaskChildren'
import type { BoardAltitude } from '@/store/workspacesStore'

// Priority badge config: P1 red, P2 orange, P3 yellow, P4 blue, P5 muted
export const PRIORITY_BADGE: Record<number, { label: string; className: string }> = {
  1: { label: 'P1', className: 'bg-red-500/20 text-red-400 border-red-500/30' },
  2: { label: 'P2', className: 'bg-orange-500/20 text-orange-400 border-orange-500/30' },
  3: { label: 'P3', className: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30' },
  4: { label: 'P4', className: 'bg-blue-500/20 text-blue-400 border-blue-500/30' },
  5: { label: 'P5', className: 'bg-[var(--color-muted)]/20 text-[var(--color-muted)] border-[var(--color-muted)]/30' },
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
}

export function TaskCard({
  task,
  milestones = [],
  agents = [],
  altitude = 'top-level',
  onClick,
  onChildClick,
}: TaskCardProps) {
  const priority = task.priority ?? 3
  const badge = PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]
  const milestone = task.milestone_id ? milestones.find((m) => m.id === task.milestone_id) : null
  const todos = task.todos ?? []
  const doneTodos = todos.filter((t) => t.status === 'completed').length
  const rollup = task.rollup ?? []
  const hasRollup = rollup.length > 0
  const showChildren = altitude === 'show-all'

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick()
        }
      }}
      className={cn(
        'rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 cursor-pointer',
        'transition-colors hover:border-[var(--color-border)]/60 hover:bg-[var(--color-surface-2)]/40',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--color-accent)]',
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
