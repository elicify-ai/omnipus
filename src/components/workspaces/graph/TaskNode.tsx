import { memo, useCallback } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { motion } from 'framer-motion'
import { getIconComponent } from '@/lib/agentIcons'
import { cn } from '@/lib/utils'
import { PRIORITY_LABELS, statusVisual, type TaskGraphNode } from './taskGraph'

// Priority pill colours — mirrors TaskCard's P1..P5 ladder (red→muted).
const PRIORITY_CLASS: Record<number, string> = {
  1: 'text-red-400 border-red-500/40',
  2: 'text-orange-400 border-orange-500/40',
  3: 'text-yellow-400 border-yellow-500/40',
  4: 'text-blue-400 border-blue-500/40',
  5: 'text-[var(--color-muted)] border-[var(--color-border)]',
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
  const { task, agentName, agentColor, agentIcon, onOpen } = data
  const visual = statusVisual(task.status)
  const priority = task.priority ?? 3
  const AgentIcon = getIconComponent(agentIcon)
  const hasAgent = Boolean(agentName)
  const avatarColor = agentColor ?? 'var(--color-muted)'

  // GraphView marks every node `focusable: false` so React Flow's own node
  // wrapper (role="group", tabIndex=0 by default) drops out of the tab
  // order — this element is the sole tab stop per card (WCAG 4.1.2). Mouse
  // clicks still flow through React Flow's onNodeClick; Enter/Space here
  // call the identical onTaskClick via the `onOpen` callback GraphView wires
  // into `data` (WCAG 2.1.1 — the click action now has a keyboard equivalent).
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
        'shadow-[0_2px_8px_rgba(0,0,0,0.35)] transition-colors',
        'focus-visible:border-[var(--color-accent)]',
        selected
          ? 'border-[var(--color-accent)] shadow-[0_0_0_1px_var(--color-accent),0_4px_20px_rgba(212,175,55,0.25)]'
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

      <div className="flex flex-col gap-2 py-2.5 pl-3.5 pr-3">
        {/* Top row: status chip + priority. */}
        <div className="flex items-center justify-between gap-2">
          <span
            className="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-semibold leading-none"
            style={{
              color: visual.color,
              backgroundColor: `${visual.color}1f`, // ~12% alpha tint
            }}
          >
            <span
              aria-hidden
              className={cn(
                'h-1.5 w-1.5 rounded-full',
                visual.animated && 'animate-pulse',
              )}
              style={{ backgroundColor: visual.color }}
            />
            {visual.label}
          </span>

          <span
            className={cn(
              'flex-shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-bold leading-none',
              PRIORITY_CLASS[priority] ?? PRIORITY_CLASS[3],
            )}
          >
            {PRIORITY_LABELS[priority] ?? 'P3'}
          </span>
        </div>

        {/* Title — Outfit, two-line clamp. */}
        <p className="font-headline text-[13px] font-semibold leading-snug text-[var(--color-secondary)] line-clamp-2">
          {task.title}
        </p>

        {/* Bottom row: assigned agent avatar + name. */}
        {hasAgent && (
          <div className="flex items-center gap-1.5">
            <span
              className="flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full"
              style={{ backgroundColor: `${toTint(avatarColor)}` }}
            >
              <AgentIcon size={10} weight="bold" style={{ color: avatarColor }} />
            </span>
            <span className="truncate text-[11px] text-[var(--color-muted)]">
              {agentName}
            </span>
          </div>
        )}
      </div>
    </motion.div>
  )
}

/**
 * A faint tinted disc behind the agent icon. Hex colours get an alpha suffix;
 * CSS-variable colours fall back to a neutral surface so we never emit invalid
 * CSS like `var(--x)2a`.
 */
function toTint(color: string): string {
  return color.startsWith('#') ? `${color}2a` : 'var(--color-surface-3)'
}

export const TaskNode = memo(TaskNodeComponent)
