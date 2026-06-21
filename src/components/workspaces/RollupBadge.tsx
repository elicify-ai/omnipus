/**
 * RollupBadge — one-line delegation roll-up for a parent task card.
 *
 * Renders "▸ N sub-agents running" with a row of agent avatar chips.
 * Chips are tinted by rollup item status and pulse (Framer Motion spring)
 * when the child is in_progress. Muted/static when done/failed/etc.
 *
 * Detail #6 (sprint4-ui-spec): "a parent card shows a one-line '▸ N
 * sub-agents running' badge w/ avatars; a short pulse on the parent while
 * running, folds into history when done."
 */

import { motion } from 'framer-motion'
import { getIconComponent } from '@/lib/agentIcons'
import type { Agent, Task } from '@/lib/api'

/** A single item from Task['rollup'] */
export type RollupItem = NonNullable<Task['rollup']>[number]

/** Status colour map — mirrors the Sovereign Deep 7-state palette */
const STATUS_COLORS: Record<RollupItem['status'], string> = {
  inbox:       '#9ca3af',
  next:        '#3B82F6',
  planning:    '#A855F7',
  in_progress: '#EAB308',
  blocked:     '#F97316',
  done:        '#10b981',
  failed:      '#ef4444',
}

interface RollupBadgeProps {
  rollup: RollupItem[]
  agents: Agent[]
}

/** Resolve agent data by id from the agents cache */
function agentById(agents: Agent[], agentId: string): Agent | undefined {
  return agents.find((a) => a.id === agentId)
}

/** Single avatar chip for one rollup item */
function RollupAvatar({ item, agent }: { item: RollupItem; agent: Agent | undefined }) {
  const isLive = item.status === 'in_progress'
  const color = (agent?.color ?? STATUS_COLORS[item.status]) as string
  const Icon = getIconComponent(agent?.icon)
  const statusColor = STATUS_COLORS[item.status]

  return (
    <motion.span
      aria-label={`${agent?.name ?? item.agent_id} — ${item.status}`}
      title={`${agent?.name ?? item.label}: ${item.status}`}
      animate={isLive ? { opacity: [1, 0.55, 1] } : { opacity: 1 }}
      transition={
        isLive
          ? { duration: 1.6, repeat: Infinity, ease: 'easeInOut' }
          : { duration: 0 }
      }
      className="inline-flex items-center justify-center rounded-full border"
      style={{
        width: 18,
        height: 18,
        backgroundColor: `${color}22`,
        borderColor: statusColor,
      }}
    >
      <Icon size={10} weight="bold" style={{ color }} />
    </motion.span>
  )
}

export function RollupBadge({ rollup, agents }: RollupBadgeProps) {
  if (!rollup || rollup.length === 0) return null

  const activeCount = rollup.filter((r) => r.status === 'in_progress').length
  const totalCount = rollup.length
  const countLabel = activeCount > 0 ? activeCount : totalCount
  const isAnyLive = activeCount > 0

  return (
    <div
      className="mt-2 flex items-center gap-1.5"
      aria-label={`${countLabel} sub-agent${countLabel !== 1 ? 's' : ''} ${isAnyLive ? 'running' : 'delegated'}`}
    >
      {/* Chevron indicator + count */}
      <span
        className="text-[10px] font-semibold leading-none"
        style={{ color: isAnyLive ? '#EAB308' : '#9ca3af' }}
      >
        &#9658; {countLabel} sub-agent{countLabel !== 1 ? 's' : ''} {isAnyLive ? 'running' : 'delegated'}
      </span>

      {/* Avatar row — capped at 5 to avoid overflow */}
      <span className="flex items-center gap-0.5" role="list" aria-label="Sub-agent avatars">
        {rollup.slice(0, 5).map((item) => (
          <span key={item.agent_id} role="listitem">
            <RollupAvatar item={item} agent={agentById(agents, item.agent_id)} />
          </span>
        ))}
        {rollup.length > 5 && (
          <span
            className="text-[10px] text-[var(--color-muted)] ml-0.5"
            aria-label={`and ${rollup.length - 5} more`}
          >
            +{rollup.length - 5}
          </span>
        )}
      </span>
    </div>
  )
}
