// ActivityAvatar — small shared avatar for Activity Bar / Activity Panel rows.
//
// Visual grammar per kind:
//   - bash            → monochrome terminal icon, muted/bordered surface.
//   - agent (native)  → colored avatar (agent.color + agent.icon), same
//                        treatment MessageItem.tsx uses for assistant messages.
//   - agent (3p)      → DELIBERATELY distinct from native: a bordered,
//                        monospace-initials badge (no colored gradient) so
//                        external-CLI subagents (claude-code/codex/opencode)
//                        read as a different kind of thing at a glance.
//   - agent (unknown) → generic muted fallback icon. Must never throw even
//                        when agentId is absent or doesn't match any known
//                        agent (see useRunningActivity's resolveAgent).

import { Terminal, UserCircle } from '@phosphor-icons/react'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { ActivityItem } from '@/hooks/useRunningActivity'

export interface ActivityAvatarProps {
  item: ActivityItem
  size?: 'sm' | 'md'
}

/** First 1-2 letters of a name, uppercased — used for the 3p badge only. */
function initials(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return '?'
  const words = trimmed.split(/\s+/).filter(Boolean)
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase()
  return trimmed.slice(0, 2).toUpperCase()
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

  switch (item.agentType) {
    case '3p':
      return (
        <Avatar size={size} className="border border-[var(--color-border)]">
          <AvatarFallback className="bg-[var(--color-surface-1)] text-[var(--color-secondary)] font-mono text-[10px] tracking-tight">
            {initials(item.agentName)}
          </AvatarFallback>
        </Avatar>
      )
    case 'unknown':
      return (
        <Avatar size={size}>
          <AvatarFallback className="bg-[var(--color-surface-3)] text-[var(--color-muted)]">
            <UserCircle size={iconSize} aria-hidden="true" />
          </AvatarFallback>
        </Avatar>
      )
    case 'native':
      return (
        <Avatar size={size}>
          <AvatarFallback
            style={{ backgroundColor: item.agentColor ?? 'var(--color-surface-3)', color: 'var(--color-secondary)' }}
          >
            {item.agentIcon ? (
              <IconRenderer icon={item.agentIcon} size={iconSize} />
            ) : (
              <UserCircle size={iconSize} aria-hidden="true" />
            )}
          </AvatarFallback>
        </Avatar>
      )
    default: {
      // Exhaustiveness guard (mirrors ActivityPanel.tsx's ActivityStatus switch) —
      // a future 4th agentType value fails to compile here instead of silently
      // rendering as native.
      const _exhaustive: never = item.agentType
      void _exhaustive
      return (
        <Avatar size={size}>
          <AvatarFallback className="bg-[var(--color-surface-3)] text-[var(--color-muted)]">
            <UserCircle size={iconSize} aria-hidden="true" />
          </AvatarFallback>
        </Avatar>
      )
    }
  }
}
