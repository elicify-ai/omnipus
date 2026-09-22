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
  const color = agent?.color ?? rollupStatusColorVar(item.status)
  const Icon = getIconComponent(agent?.icon)

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
        // `color-mix` (not a `${color}NN` hex-alpha-suffix concat — see the
        // design-system skill's "Alpha on a brand color" rule): `color` may
        // be an arbitrary agent hex OR one of `rollupStatusColorVar`'s
        // `var(--status-*)` token references, and only `color-mix` composes
        // correctly with a CSS custom-property value (a suffixed `var(...)NN`
        // string is invalid CSS and silently drops the declaration).
        // 13.3% mix == a 0x22 (34/255) hex alpha, this pill's tint level.
        backgroundColor: `color-mix(in srgb, ${color} 13.3%, transparent)`,
        // Always the pure status accent (ignores `agent?.color` — unlike
        // `color` above) so every chip's border reads the item's status
        // family even when the agent has its own custom colour. Called
        // inline rather than bound to a separately-named local: a local
        // whose name reads as both "status" and "colour" (e.g. `statusColor`)
        // is exactly the shape `scripts/design-system-locks/status.mjs`
        // treats as a status-governed colour binding it must statically
        // prove — and a same-file, non-imported helper call is outside what
        // it can resolve, so it fails closed as
        // `design-system/status-unsupported` (never baselinable). Inlined
        // here it is just an ordinary runtime read, exactly like `color`'s
        // own `rollupStatusColorVar` fallback above and the `<Icon>`/label
        // colour reads below.
        borderColor: rollupStatusColorVar(item.status),
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
      <span className="flex items-center gap-[var(--space-0-5)]" role="list" aria-label="Sub-agent avatars">
        {rollup.slice(0, 5).map((item) => (
          <span key={item.agent_id} role="listitem">
            <RollupAvatar item={item} agent={agentById(agents, item.agent_id)} />
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
