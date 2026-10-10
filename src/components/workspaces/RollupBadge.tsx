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
import { AgentMark } from '@/components/agents/AgentMark'
import type { Agent, Task } from '@/lib/api'

/** A single item from Task['rollup'] */
export type RollupItem = NonNullable<Task['rollup']>[number]

/**
 * Status accent for a rollup item — a literal `switch` over the closed
 * status-family set, not a dynamic record lookup (the design-system static
 * scanners cannot resolve a style value read out of a record via a runtime
 * key). Each branch is the matching `--status-<name>-foreground` token
 * (`src/styles/tokens.generated.css`), which resolves to the same value
 * `src/design-system/status.ts`'s `statusContract` publishes for that
 * status. Mirrors `BoardView.tsx`'s `statusHeaderColorVar`.
 */
function rollupStatusColorVar(status: RollupItem['status']): string {
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

type RollupAgent = Pick<Agent, 'id'> & Partial<Pick<Agent, 'name' | 'color' | 'figure' | 'role'>>

interface RollupBadgeProps {
  rollup: RollupItem[]
  agents: RollupAgent[]
  plain?: boolean
}

/** Resolve agent data by id from the agents cache */
function agentById(agents: RollupAgent[], agentId: string): RollupAgent | undefined {
  return agents.find((a) => a.id === agentId)
}

/** Single avatar chip for one rollup item: the agent's own mark inside a
 *  status ring. The ring and tint carry the item's status; the mark carries
 *  who the agent is — the same AgentMark every surface draws. */
function RollupAvatar({ item, agent }: { item: RollupItem; agent: RollupAgent | undefined }) {
  const isLive = item.status === 'in_progress'

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
      className="inline-flex h-[var(--space-4)] w-[var(--space-4)] items-center justify-center rounded-full border"
      style={{
        // `color-mix` over the status token (never a hex-alpha concat — see
        // the design-system skill's "Alpha on a brand color" rule). The status
        // helper is called inline, not bound to a status-named local, which
        // scripts/design-system-locks/status.mjs could not statically prove.
        backgroundColor: `color-mix(in srgb, ${rollupStatusColorVar(item.status)} 13.3%, transparent)`,
        borderColor: rollupStatusColorVar(item.status),
      }}
    >
      <AgentMark agent={agent} name={agent?.name ?? item.agent_id} size={18} />
    </motion.span>
  )
}

export function RollupBadge({ rollup, agents, plain = false }: RollupBadgeProps) {
  if (!rollup || rollup.length === 0) return null

  const activeCount = rollup.filter((r) => r.status === 'in_progress').length
  const totalCount = rollup.length
  const countLabel = activeCount > 0 ? activeCount : totalCount
  const isAnyLive = activeCount > 0

  return (
    <div
      className="mt-[var(--space-2)] flex items-center gap-[var(--space-1)]"
      aria-label={`${countLabel} sub-agent${countLabel !== 1 ? 's' : ''} ${isAnyLive ? 'running' : 'delegated'}`}
    >
      {/* Chevron indicator + count */}
      <span
        className="text-[length:var(--type-caption-size)] font-semibold leading-none"
        style={{ color: isAnyLive ? rollupStatusColorVar('in_progress') : rollupStatusColorVar('inbox') }}
      >
        &#9658; {countLabel} sub-agent{countLabel !== 1 ? 's' : ''} {isAnyLive ? 'running' : 'delegated'}
      </span>

      {/* Avatar row — capped at 5 to avoid overflow */}
      <span className="flex items-center gap-[var(--space-0-5)]" role="list" aria-label={plain ? 'Sub-agents' : 'Sub-agent avatars'}>
        {rollup.slice(0, 5).map((item) => (
          <span key={item.agent_id} role="listitem">
            {plain ? <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]" aria-label={`${agentById(agents, item.agent_id)?.name ?? item.agent_id} — ${item.status}`}>
              {agentById(agents, item.agent_id)?.name ?? item.agent_id}
            </span> : <RollupAvatar item={item} agent={agentById(agents, item.agent_id)} />}
          </span>
        ))}
        {rollup.length > 5 && (
          <span
            className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] ml-[var(--space-0-5)]"
            aria-label={`and ${rollup.length - 5} more`}
          >
            +{rollup.length - 5}
          </span>
        )}
      </span>
    </div>
  )
}
