import { memo, useCallback } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { motion } from 'framer-motion'
import { GitMerge, FolderSimple, CheckSquare } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { PRIORITY_LABELS, taskNodeVisual, type TaskGraphNode } from './taskGraph'
import { TaskActionButton } from '../TaskActionButton'
import { RunningIndicator } from '@/components/ui/RunningIndicator'
import { TaskDetailsPopover } from '../WorkItemDetails'
import { WordBoundaryText } from '@/components/ui/word-boundary-text'

/**
 * Priority pill colours — mirrors `PriorityBadge.tsx`'s P1..P5 ladder
 * (red→muted), same `--color-priority-N` tokens (converted, byte-for-byte,
 * from this Tailwind v4 install's own `red-400`/`red-500`,
 * `orange-400`/`orange-500`, `yellow-400`/`yellow-500`, `blue-400`/`blue-500`
 * — see that file's doc comment). A literal `if`-chain over the closed
 * priority set, not a `Record` looked up by a runtime key — the
 * design-system static scanners cannot resolve a class list read out of a
 * record via a dynamic key.
 */
function priorityNodeClass(priority: number): string {
  const fallback = 'text-[var(--color-priority-3)] border-[var(--color-priority-3)]/40'
  if (priority === 1) return 'text-[var(--color-priority-1)] border-[var(--color-priority-1)]/40'
  if (priority === 2) return 'text-[var(--color-priority-2)] border-[var(--color-priority-2)]/40'
  if (priority === 4) return 'text-[var(--color-priority-4)] border-[var(--color-priority-4)]/40'
  if (priority === 5) return 'text-[var(--color-muted)] border-[var(--color-border)]'
  return fallback
}

/**
 * A single task rendered as a React Flow node — the heart of the DAG view.
 *
 * Layout (Sovereign Deep):
 *   ┌──────────────────────────────────┐
 *   │ [status chip]            [P2]     │   ← status + priority
 *   │ Task title, up to two lines…      │   ← Outfit, truncated
 *   │ (avatar) Agent name               │   ← assigned agent
 *   └──────────────────────────────────┘
 * A coloured left rail keys the node to its lifecycle status at a glance.
 */
function TaskNodeComponent({ data, selected }: NodeProps<TaskGraphNode>) {
  const { task, agentName, onOpen } = data
  // ADR-052 FR-015/US-8 — a user-cancelled task renders orange "Cancelled",
  // distinct from a genuine red "Failed" (taskNodeVisual overrides
  // statusVisual's plain status→colour/label for that one case).
  const visual = taskNodeVisual(task)
  const priority = task.priority ?? 3
  const todos = task.todos ?? []
  const doneTodos = todos.filter((todo) => todo.status === 'completed').length

  // ADR-053 FE-2 §7 (D7) — plan-member DAG signals on the Graph node.
  // `is_join` marks the authored convergence member that folds one or more
  // parallel `stream`s into a single artifact (g5 shard+assemble); `write_set`
  // is the lint-disjoint write footprint plan-lint checks at approve. Both
  // are meaningful only alongside `plan_id` (see Task.yaml) — rendering on
  // presence keeps a standalone-task node clean. Empty write_set on an
  // exploratory member (D10) renders no chip, by design.
  const isJoin = Boolean(task.is_join)
  const writeSet = task.write_set ?? []
  const writeSetLabel = writeSet.join(', ')
  const hasPlanMeta = isJoin || writeSet.length > 0

  // GraphView marks every node `focusable: false` so React Flow's own node
  // wrapper (role="group", tabIndex=0 by default) drops out of the tab
  // order — this element is the sole tab stop per card (WCAG 4.1.2). Mouse
  // clicks still flow through React Flow's onNodeClick; Enter/Space here
  // call the identical onTaskClick via the `onOpen` callback GraphView wires
  // into `data` (WCAG 2.1.1 — the click action now has a keyboard equivalent).
  //
  // `onOpen` is optional on `TaskNodeData` (see taskGraph.ts) so a dropped
  // injection compiles cleanly — it wouldn't be a type error, just a
  // keyboard-open that silently does nothing. Surface it loudly in dev so a
  // regression here is caught before it ships, not by accident in manual QA.
  if (import.meta.env.DEV && !onOpen) {
    console.warn(
      `[TaskNode] task "${task.id}" rendered with no data.onOpen — keyboard Enter/Space will not open it (GraphView must inject onOpen per node).`,
    )
  }
  const handleOpen = useCallback(() => {
    onOpen?.(task)
  }, [onOpen, task])

  return (
    <motion.div
      initial={{ opacity: 0, scale: 0.96 }}
      animate={{ opacity: 1, scale: 1 }}
      whileHover={{ y: -2, scale: 1.015 }}
      transition={{ type: 'spring', stiffness: 380, damping: 26 }}
      role="button"
      tabIndex={0}
      aria-label={`${task.title}, ${visual.label}`}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          handleOpen()
        }
      }}
      className={cn(
        'group relative w-[248px] overflow-hidden rounded-xl border bg-[var(--color-surface-1)]',
        'shadow-[0_2px_8px_color-mix(in_srgb,var(--color-primary)_35%,transparent)] transition-colors',
        'focus-visible:border-[var(--color-accent)]',
        selected
          ? 'border-[var(--color-accent)] shadow-[0_0_0_1px_var(--color-accent),0_4px_20px_color-mix(in_srgb,var(--color-accent)_25%,transparent)]'
          : 'border-[var(--color-border)] hover:border-[var(--color-border)]/80',
      )}
      data-testid={`task-node-${task.id}`}
    >
      {/* Connection handles — left = incoming dep, right = outgoing dep. */}
      <Handle
        type="target"
        position={Position.Left}
        className="!h-2 !w-2 !border-0 !bg-[var(--color-border)]"
      />
      <Handle
        type="source"
        position={Position.Right}
        className="!h-2 !w-2 !border-0 !bg-[var(--color-border)]"
      />

      {/* Status-coloured left rail. */}
      <span
        aria-hidden
        className="absolute inset-y-0 left-0 w-1"
        style={{ backgroundColor: visual.color }}
      />

      <div className="flex min-w-0 flex-col gap-[var(--space-1)] p-[var(--space-2)]">
        <div className="flex min-w-0 items-start gap-[var(--space-1)]">
          <span className={cn('shrink-0 text-[length:var(--type-caption-size)] font-bold leading-none', priorityNodeClass(priority))}>{PRIORITY_LABELS[priority] ?? 'P3'}</span>
          <WordBoundaryText as="p" text={task.title} className="h-[calc(var(--type-caption-size)*var(--type-caption-line-height)*2)] min-w-0 max-w-full flex-1 line-clamp-2 whitespace-normal break-normal wrap-break-word hyphens-none font-headline text-[length:var(--type-caption-size)] font-semibold leading-[var(--type-caption-line-height)] text-[var(--color-secondary)]" />
          <TaskDetailsPopover task={task} plans={data.plans} agents={data.agents} onOpenTask={handleOpen} />
          <TaskActionButton task={task} />
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          <span className="inline-flex items-center gap-[var(--space-1)]" style={{ color: visual.color }}>
            {task.status === 'in_progress' ? <RunningIndicator /> : <span aria-hidden="true">●</span>}<span>{visual.label}</span>
          </span>
          {agentName && <><span aria-hidden="true">·</span><span className="min-w-0 break-normal wrap-break-word">{agentName}</span></>}
          {todos.length > 0 && <><span aria-hidden="true">·</span><CheckSquare size={11} aria-hidden="true" /><span>{doneTodos}/{todos.length}</span></>}
          {(task.tags ?? []).map((tag) => <span key={tag} className="inline-flex min-w-0 max-w-full gap-[var(--space-1)]"><span aria-hidden="true">·</span><WordBoundaryText text={tag} className="break-normal wrap-break-word" /></span>)}
        </div>

        {/* ADR-053 FE-2 §7 (D7) — plan-member DAG signals. The join member
            (gold GitMerge pill) is the authored convergence point that folds
            parallel `stream`s into one artifact; the write_set chip lists the
            lint-disjoint paths this member creates/edits. Standalone-task
            nodes (no plan_id) carry neither and skip this row entirely. */}
        {hasPlanMeta && (
          <div className="flex flex-col gap-[var(--space-1)]" data-testid={`task-node-planmeta-${task.id}`}>
            {isJoin && (
              <span
                className="inline-flex w-fit items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] leading-none text-[var(--color-muted)]"
                title="Join member — converges one or more parallel streams into a single artifact"
              >
                <GitMerge size={10} weight="bold" />
                Join
              </span>
            )}
            {writeSetLabel && (
              <span
                className="inline-flex w-fit max-w-full items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] leading-none text-[var(--color-muted)]"
                title={`Writes: ${writeSetLabel}`}
              >
                <FolderSimple size={10} weight="fill" className="flex-shrink-0" />
                <span className="truncate">{writeSetLabel}</span>
              </span>
            )}
          </div>
        )}

      </div>
    </motion.div>
  )
}

export const TaskNode = memo(TaskNodeComponent)
