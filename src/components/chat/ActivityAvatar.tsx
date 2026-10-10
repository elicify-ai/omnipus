// ActivityAvatar — small shared avatar for Activity Bar / Activity Panel rows.
//
// Visual grammar per kind:
//   - bash            → monochrome terminal icon, muted/bordered surface.
//   - any agent       → the agent's own AgentMark (figure, role badge,
//                        colour) — the same mark every surface draws. That
//                        includes external-CLI workers, which carry their own
//                        figure. An agent that is not resolved draws the
//                        Omnipus fallback; it must never throw when agentId is
//                        absent or unknown (see useRunningActivity's resolveAgent).

import { Terminal, Scales } from '@phosphor-icons/react'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { AgentMark } from '@/components/agents/AgentMark'
import type { ActivityItem } from '@/hooks/useRunningActivity'

export interface ActivityAvatarProps {
  item: ActivityItem
  size?: 'sm' | 'md'
}

export function ActivityAvatar({ item, size = 'md' }: ActivityAvatarProps) {
  const iconSize = size === 'sm' ? 12 : 14

  if (item.kind === 'bash') {
    return (
      <Avatar size={size} className="border border-[var(--color-border)]">
        <AvatarFallback className="bg-[var(--color-surface-2)] text-[var(--color-muted)]">
          <Terminal size={iconSize} aria-hidden="true" />
        </AvatarFallback>
      </Avatar>
    )
  }

  // ADR-049 D2/D4/US-13: judge verdicts — a distinct, Forge-Gold-tinted
  // "scales" glyph so a judge verdict row reads as a different kind of thing
  // at a glance — a verdict, not an agent at work.
  if (item.kind === 'judge') {
    return (
      <Avatar size={size} className="border border-[var(--color-accent)]/30">
        <AvatarFallback className="bg-[var(--color-accent)]/10 text-[var(--color-accent)]">
          <Scales size={iconSize} weight="fill" aria-hidden="true" />
        </AvatarFallback>
      </Avatar>
    )
  }

  // Every agent kind (native, external CLI, unresolved) draws its AgentMark;
  // the 28px `sm` slot fits the 26px mark.
  return <AgentMark agent={item.agent} name={item.agentName ?? ''} size={26} />
}
