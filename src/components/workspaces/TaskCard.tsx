import { useId } from 'react'
import { cn } from '@/lib/utils'
import type { Task, Agent, Plan } from '@/lib/api'
import { CheckSquare, HandPalm, WarningCircle } from '@phosphor-icons/react'
import { useToolApprovalStore } from '@/store/toolApproval'
import { taskAwaitingApproval } from './TaskActivityChip'
import { TaskChildren } from './TaskChildren'
import { TaskActionButton } from './TaskActionButton'
import { PriorityBadge } from './PriorityBadge'
import { TaskElapsedTime } from './TaskElapsedTime'
import { RunningIndicator } from '@/components/ui/RunningIndicator'
import { TaskDetailsPopover } from './WorkItemDetails'
import { WordBoundaryText } from '@/components/ui/word-boundary-text'
import { taskDisplayColor, taskDisplayLabel } from '@/lib/statusColors'
import type { BoardAltitude } from '@/store/workspacesStore'
import type { DraggableAttributes, DraggableSyntheticListeners } from '@dnd-kit/core'

// Keep the existing helper API stable; execution diagnostics now live in info
// details instead of adding extra rows to the founder's five-row Board card.
export { DEFAULT_TASK_MAX_ATTEMPTS, goalLoopStatusLabel } from './taskExecution'

/** Every field comes from the same useDraggable call: either the whole card
 * is wired, or it is a non-draggable card. Nested controls never activate it.
 */
export interface TaskCardDrag {
  attributes: DraggableAttributes
  listeners: NonNullable<DraggableSyntheticListeners>
  activatorRef: (element: HTMLElement | null) => void
}

interface TaskCardProps {
  task: Task
  plans?: Plan[]
  agents?: Agent[]
  altitude?: BoardAltitude
  onClick: () => void
  onChildClick?: (child: Task) => void
  drag?: TaskCardDrag
  /** The purely visual drag clone exposes no action or info controls. */
  showActions?: boolean
  showDetails?: boolean
}

export function TaskCard({ task, plans = [], agents = [], altitude = 'top-level', onClick, onChildClick, drag, showActions = true, showDetails = true }: TaskCardProps) {
  const priority = task.priority ?? 3
  const running = task.status === 'in_progress'
  const queue = useToolApprovalStore((state) => state.queue)
  const approval = taskAwaitingApproval(task, queue)
  const showsExecutionTime = running || task.status === 'done' || task.status === 'failed'
  const agentName = task.agent_name ?? agents.find((agent) => agent.id === task.agent_id)?.name ?? task.agent_id
  const todos = task.todos ?? []
  const doneTodos = todos.filter((todo) => todo.status === 'completed').length
  const isDraggable = Boolean(drag)
  const dragKeyDown = drag?.listeners.onKeyDown as ((event: React.KeyboardEvent<HTMLDivElement>) => void) | undefined
  const dragPointerDown = drag?.listeners.onPointerDown as ((event: React.PointerEvent<HTMLDivElement>) => void) | undefined
  const enterSpaceHintId = useId()
  const describedBy = isDraggable ? [drag?.attributes['aria-describedby'], enterSpaceHintId].filter(Boolean).join(' ') : undefined

  return <div
    ref={drag?.activatorRef}
    role="button"
    tabIndex={0}
    aria-label={`${task.title}, status ${taskDisplayLabel(task)}`}
    aria-disabled={drag?.attributes['aria-disabled']}
    aria-pressed={drag?.attributes['aria-pressed']}
    aria-roledescription={drag?.attributes['aria-roledescription']}
    aria-describedby={describedBy}
    onClick={onClick}
    onPointerDown={dragPointerDown}
    onKeyDown={(event) => {
      // A nested info/action/subtask has its own native keyboard activation.
      if (event.target !== event.currentTarget) return
      dragKeyDown?.(event)
      // Draggable cards reserve Space for lift; all cards open on Enter.
      if (event.key === 'Enter' || (!isDraggable && event.key === ' ')) {
        event.preventDefault()
        onClick()
      }
    }}
    className={cn(
      'group relative min-h-[var(--tasks-board-card-height,auto)] rounded-lg border border-l-[length:var(--space-1)] border-[var(--color-border)] bg-[var(--color-surface-1)] cursor-pointer',
      'transition-colors hover:bg-[var(--color-surface-2)]/40',
    )}
    style={{ borderLeftColor: taskDisplayColor(task) }}
  >
    {isDraggable && <span id={enterSpaceHintId} className="sr-only">Enter to open, Space to move.</span>}
    <div data-task-card-content={showDetails ? 'item' : 'visual'} className="flex min-w-0 flex-col gap-[var(--space-1)] p-[var(--space-2)]">
      {/* T24: controls only. The title never competes with their width. */}
      <div data-task-card-row="controls" className="flex min-w-0 items-center justify-between gap-[var(--space-1)]">
        <PriorityBadge priority={priority} className="shrink-0 bg-transparent p-0 text-[length:var(--type-caption-size)] font-bold leading-tight" />
        <div className="flex shrink-0 items-center gap-[var(--space-1)]">
          {approval && <span data-testid="task-approval-alert" role="img" aria-label={`Waiting for your approval to use ${approval.toolName}`} className="inline-flex text-[color:var(--color-warning)]"><HandPalm size={13} aria-hidden="true" /></span>}
          {task.assignee_warning && <span data-testid="task-assignee-alert" role="img" aria-label={task.assignee_warning.message} className="inline-flex text-[color:var(--color-warning)]"><WarningCircle size={13} aria-hidden="true" /></span>}
          {showDetails && <TaskDetailsPopover task={task} plans={plans} agents={agents} onOpenTask={onClick} />}
          {showActions && <TaskActionButton task={task} />}
        </div>
      </div>
      {/* T25: one full-width, fixed two-line title slot, below the controls. */}
      <WordBoundaryText as="p" text={task.title} className="h-[calc(var(--type-body-compact-size)*var(--type-body-compact-line-height)*2)] w-full min-w-0 max-w-full line-clamp-2 whitespace-normal break-normal wrap-break-word hyphens-none text-[length:var(--type-body-compact-size)] font-medium leading-[var(--type-body-compact-line-height)] text-[var(--color-secondary)]" />
      {/* T26: one execution line; truncating a long agent keeps separators
          between values, never stranded at a line edge. Full name is in info. */}
      <div data-testid="task-execution-row" className="flex min-w-0 items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {agentName && <span className="min-w-0 flex-1 truncate">{agentName}</span>}
        {running && <>{agentName && <span aria-hidden="true" className="shrink-0">·</span>}<RunningIndicator className="shrink-0" /></>}
        {showsExecutionTime && <TaskElapsedTime key={task.started_at ?? 'execution'} task={task} live={showDetails} separator={Boolean(agentName) && !running} />}
      </div>
      {todos.length > 0 && <div data-testid="task-checklist-row" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        <CheckSquare size={11} aria-hidden="true" /><span>{doneTodos}/{todos.length}</span>
      </div>}
      {/* This existing optional altitude is not used by the Tasks panel's
          top-level Board; preserve explicit child expansion for its callers. */}
      {altitude === 'show-all' && <TaskChildren parentTaskId={task.id} onChildClick={onChildClick ?? onClick} />}
    </div>
  </div>
}
