// The delivery tick of one user row. Identical to UserMessageDeliveryStatus
// except for a HELD row (R2-S3-01): the agent that is stopped is the one the
// message was ADDRESSED to, not the chat's own agent, so the waiting copy
// reads the name from the message's leading "@<Name>" against the live roster.
// The roster subscription lives in a child that only held rows mount, so a
// long transcript does not spawn one query observer per row.
import type { ComponentProps } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchAgents } from '@/lib/api'
import { addressableAgents, parseAddressedAgent } from '@/lib/recipientAddress'
import { useSessionStore } from '@/store/session'
import { UserMessageDeliveryStatus } from './ConnectionStatus'

/** Used when the roster no longer matches the address (e.g. the agent was renamed since). */
export const UNMATCHED_ADDRESSEE = 'the addressed agent'

type DeliveryProps = ComponentProps<typeof UserMessageDeliveryStatus>

function HeldDeliveryStatus({ content, ...props }: DeliveryProps & { content: string }) {
  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  // The send path's own candidate filter, so the row names who was addressed.
  const addressee = parseAddressedAgent(content, addressableAgents(agents, activeAgentId))?.name ?? UNMATCHED_ADDRESSEE
  return <UserMessageDeliveryStatus {...props} agentName={addressee} />
}

export function RowDeliveryStatus({ content, ...props }: DeliveryProps & { content: string }) {
  if (props.state === 'held' && !props.isClearCommand) return <HeldDeliveryStatus content={content} {...props} />
  return <UserMessageDeliveryStatus {...props} />
}
