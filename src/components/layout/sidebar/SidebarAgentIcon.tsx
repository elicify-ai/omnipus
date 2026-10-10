import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { cn } from '@/lib/utils'
import type { AttentionMotion } from './attentionCue'
import './attention.css'

/**
 * One slot in front of the agent name. Wave 1 replaces the inner avatar
 * with the shared AgentIcon; callers keep this component.
 */
export function SidebarAgentIcon({
  name,
  halo,
  motion,
}: {
  name: string
  halo: boolean
  motion: AttentionMotion
}) {
  const initial = name.trim().charAt(0).toUpperCase()
  return (
    <span
      data-attention-halo={halo ? 'warning' : undefined}
      className={cn(
        'inline-flex shrink-0 rounded-full',
        halo && 'sidebar-attention-halo',
        halo && motion === 'loop' && 'sidebar-attention-halo-loop',
      )}
    >
      <Avatar size="sm" aria-hidden="true">
        <AvatarFallback>{initial || '?'}</AvatarFallback>
      </Avatar>
    </span>
  )
}
