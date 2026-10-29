import { AgentMark } from '@/components/agents/AgentMark'
import { AttentionHalo } from '@/components/agents/AttentionHalo'
import type { Agent } from '@/lib/api'
import type { AttentionMotion } from './attentionCue'

/**
 * One slot in front of the agent name: the agent's own AgentMark (the same
 * mark every surface draws), wrapped in the attention halo. An agent that is
 * not loaded draws the Omnipus fallback — never a guessed figure or initial.
 * The halo itself is the shared AttentionHalo (#1449: the group chat uses
 * the same one).
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
    <AttentionHalo halo={halo} motion={motion}>
      <AgentMark agent={agent} name={name} size={26} />
    </AttentionHalo>
  )
}
