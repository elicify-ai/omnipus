import { AgentIcon } from '@/components/ui/agent-icon'
import type { Agent } from '@/lib/api'
import { AgentColor } from '@/lib/api/generated/schemas'

/**
 * AgentMark — the ONE way an agent's avatar is drawn anywhere in the app.
 *
 * Every surface that shows an agent (roster cards, chat messages, activity
 * rows, pickers, the sidebar, task and team views) renders this, so the mark
 * is identical everywhere: the agent's own figure, role badge and colour,
 * drawn by the catalogued AgentIcon. There is no second avatar system — the
 * legacy per-agent Phosphor `icon` is not read here or anywhere else.
 *
 * An agent that is not loaded (unknown id, list still fetching) draws the
 * founder-chosen fallback: the Omnipus figure with the General badge in the
 * create-default colour. It never guesses a figure from the name.
 */

/** Create default colour (docs/agents.md: omitted colour → #9CA3AF). */
const DEFAULT_COLOR = AgentColor.options[9]

export type AgentMarkSize = 18 | 26 | 40 | 48

/** The identity fields the mark needs. A full Agent satisfies this. */
export type AgentIdentity = Partial<Pick<Agent, 'name' | 'figure' | 'role' | 'color'>>

export interface AgentMarkProps {
  /** The loaded agent (or its identity fields), or undefined when it is not (yet) known. */
  agent: AgentIdentity | undefined
  /** Display name used when `agent` is undefined (e.g. a name from a frame). */
  name?: string
  size: AgentMarkSize
  /** Default true: the mark sits next to a visible name. false: role="img" labelled with the name. */
  decorative?: boolean
  motion?: 'none' | 'thinking' | 'working' | 'waiting'
}

export function AgentMark({ agent, name, size, decorative = true, motion }: AgentMarkProps) {
  const label = agent?.name ?? name ?? ''
  const common = {
    figure: agent?.figure ?? 'Omnipus',
    role: agent?.role ?? 'general',
    color: agent?.color ?? DEFAULT_COLOR,
    size,
    motion,
  } as const
  return decorative
    ? <AgentIcon {...common} name={label} />
    : <AgentIcon {...common} name={label} decorative={false} />
}
