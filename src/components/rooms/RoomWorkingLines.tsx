// RoomWorkingLines — the calm live view of a group chat (#1449 US-5; D5;
// FR-027, FR-028; ADR-20261028 A3.3, §11 C-I7).
//
// One line per member whose latest request in this group is admitted,
// working, waiting on the person or awaiting approval: "<Agent> is
// working...". No partial answer text is ever shown — the finished answer
// replaces the line (the answer is an entry; the delivery moves to
// `answered`, which draws no line). A request that ended failed,
// interrupted, refused, unavailable, held or cancelled shows that state's
// existing label instead. While the member waits on the person and the card
// is NOT in this group (a helper's card, unrelated work, a step that also
// answers other input or another group — D15, D23, D28), its avatar shows
// the shared attention halo and the line links to its main chat.

import { Link } from '@tanstack/react-router'
import type { RoomDeliveryState } from '@/lib/api/generated/asyncapi-types'
import type { Agent, Workspace } from '@/lib/api'
import { AgentMark } from '@/components/agents/AgentMark'
import { AttentionHalo } from '@/components/agents/AttentionHalo'
import { useAttentionMotion } from '@/components/layout/sidebar/attentionCue'
import { deliveryLabel, memberMainSessionId } from './roomModel'
import { needsAttention } from './useRoomView'

const WORKING_STATES = new Set<RoomDeliveryState['state']>(['admitted', 'working', 'waiting_on_user', 'awaiting_approval'])
const LABELLED_STATES = new Set<RoomDeliveryState['state']>(['failed', 'interrupted', 'refused', 'unavailable', 'held', 'cancelled'])

export function RoomWorkingLines({
  memberStates,
  agents,
  agentName,
  workspace,
  groupCards,
}: {
  memberStates: Record<string, RoomDeliveryState>
  agents: Agent[] | undefined
  agentName: (agentId: string) => string
  workspace: Workspace | undefined
  groupCards: ReadonlySet<string>
}) {
  const motion = useAttentionMotion()
  const lines = Object.entries(memberStates)
    .filter(([, delivery]) => WORKING_STATES.has(delivery.state) || LABELLED_STATES.has(delivery.state))
  return (
    // Polite live region, like the normal chat's status indicator.
    <div data-testid="room-working-lines" aria-live="polite" className="flex flex-col gap-[var(--space-1)] px-[var(--space-3)] py-[var(--space-1)] empty:hidden">
      {lines.map(([agentId, delivery]) => {
        const name = agentName(agentId)
        const agent = agents?.find((candidate) => candidate.id === agentId)
        const working = WORKING_STATES.has(delivery.state)
        const attention = needsAttention(delivery, groupCards)
        const mainChat = attention ? memberMainSessionId(workspace, agentId) : undefined
        return (
          <div key={agentId} data-testid={`room-working-line-${agentId}`} data-delivery-state={delivery.state} className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            <AttentionHalo halo={attention} motion={motion}>
              <AgentMark agent={agent} name={name} size={18} motion={working && !attention ? 'working' : 'none'} />
            </AttentionHalo>
            <span className={working ? 'italic' : undefined}>
              {working ? `${name} is working...` : `${name} — ${deliveryLabel(delivery, name)}`}
            </span>
            {mainChat && (
              <Link tabIndex={0} to="/sessions/$sessionId" params={{ sessionId: mainChat }} className="text-[var(--color-accent)] underline-offset-2 hover:underline">
                Open {name}&apos;s main chat
              </Link>
            )}
          </div>
        )
      })}
    </div>
  )
}
