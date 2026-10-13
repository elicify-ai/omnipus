import { AgentMark } from '@/components/agents/AgentMark'
import type { Agent } from '@/lib/api'
import { cn } from '@/lib/utils'
import type { AttentionMotion } from './attentionCue'
import './attention.css'

/**
 * One slot in front of the agent name: the agent's own AgentMark (the same
 * mark every surface draws), wrapped in the attention halo. An agent that is
 * not loaded draws the Omnipus fallback — never a guessed figure or initial.
 */
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
  return (
    <span
      data-attention-halo={halo ? 'warning' : undefined}
      className={cn(
        'inline-flex shrink-0 rounded-full',
        halo && 'sidebar-attention-halo',
        halo && motion === 'loop' && 'sidebar-attention-halo-loop',
      )}
    >
      <AgentMark agent={agent} name={name} size={26} />
    </span>
  )
}
