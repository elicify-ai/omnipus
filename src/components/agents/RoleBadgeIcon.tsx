import type { AgentRole } from '@/lib/api/generated/openapi-types'
import { roleBadgeInner } from '@/lib/agentIconArt'

/**
 * RoleBadgeIcon — one role badge glyph on its own (no figure), drawn in the
 * current text colour. It is the same art the agent mark carries in its
 * corner, used where the role is shown by itself (the Role badge picker).
 * Decorative: the role's name is always rendered as text next to it.
 */
export function RoleBadgeIcon({ role, size = 16 }: { role: AgentRole; size?: 16 | 18 }) {
  return (
    <svg
      aria-hidden="true"
      data-role-badge={role}
      width={size}
      height={size}
      viewBox="0 0 256 256"
      fill="currentColor"
      className="shrink-0"
      dangerouslySetInnerHTML={{ __html: roleBadgeInner(role) }}
    />
  )
}
