import { AgentIcon } from '@/components/ui/agent-icon'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import type { Agent } from '@/lib/api/generated/openapi-types'
import { AgentColor } from '@/lib/api/generated/schemas'
import { cn } from '@/lib/utils'
import type { AttentionMotion } from './attentionCue'
import './attention.css'

/** The supplied agent's identity mark inside the existing attention halo. */
export function SidebarAgentIcon({
  agent,
  name,
  halo,
  motion,
}: {
  agent: Agent | undefined
  name: string
  halo: boolean
  motion: AttentionMotion
}) {
  const initial = name.trim().charAt(0).toUpperCase()
  // Governed identity ink, not chrome. Generated option 9 is the existing Grey default.
  const color: NonNullable<Agent['color']> = agent?.color ?? AgentColor.options[9]
  return (
    <span
      data-attention-halo={halo ? 'warning' : undefined}
      className={cn(
        'inline-flex shrink-0 rounded-full',
        halo && 'sidebar-attention-halo',
        halo && motion === 'loop' && 'sidebar-attention-halo-loop',
      )}
    >
      {agent ? (
        <AgentIcon figure={agent.figure} role={agent.role} color={color} size={26} />
      ) : (
        <Avatar size="sm" aria-hidden="true">
          <AvatarFallback>{initial || '?'}</AvatarFallback>
        </Avatar>
      )}
    </span>
  )
}
