// RoomHeader — a group chat's header (omnipus-system-agent FR-158, FR-171,
// FR-175, FR-201, FR-204, FR-207, FR-208; §6.11, §6.14).
//
// #1449: no longer mounted (the group page uses the shared chat stack, the
// header line's Participants button and the Participants panel). Kept, with
// its tests, until the replacement tests pass in CI (SC-010, ADR C-I9); the
// membership controls it used now live in roomMembership.tsx.

import type { Room } from '@/lib/api/generated/openapi-types'
import type { RoomDeliveryState } from '@/lib/api/generated/asyncapi-types'
import type { Workspace } from '@/lib/api'
import { AddParticipant } from '@/components/chat/AddParticipant'
import { Button } from '@/components/ui/button'
import { deliveryLabel, deliveryNeedsMainChatLink, formatAllocatedCost, memberMainSessionId } from './roomModel'
import { DeleteGroup, MainChatLink, MemberMenu, useMembership } from './roomMembership'

export interface RoomHeaderProps {
  room: Room
  workspace: Workspace | undefined
  agentName: (agentId: string) => string
  memberStates: Record<string, RoomDeliveryState>
  onAskAgain: (agentId: string) => void
}

function MemberState({ agentId, name, delivery, workspace, onAskAgain }: {
  agentId: string
  name: string
  delivery: RoomDeliveryState | undefined
  workspace: Workspace | undefined
  onAskAgain: (agentId: string) => void
}) {
  if (!delivery) return null
  return (
    <span data-testid={`room-member-state-${agentId}`} className="flex flex-wrap items-center gap-[var(--space-1)]">
      <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{deliveryLabel(delivery, name)}</span>
      {deliveryNeedsMainChatLink(delivery) && (
        <MainChatLink workspace={workspace} agentId={agentId} label={`Open ${name}'s main chat`} />
      )}
      {delivery.state === 'interrupted' && (
        <Button variant="link" size="sm" className="h-auto p-0" onClick={() => onAskAgain(agentId)}>Ask again</Button>
      )}
    </span>
  )
}

function originAgentId(room: Room, workspace: Workspace | undefined): string | undefined {
  if (!room.origin_session_id) return undefined
  return room.members.find((agentId) => memberMainSessionId(workspace, agentId) === room.origin_session_id)
}

export function RoomHeader({ room, workspace, agentName, memberStates, onAskAgain }: RoomHeaderProps) {
  const membership = useMembership(room)
  const origin = originAgentId(room, workspace)
  const formerMembers = Object.keys(memberStates).filter((agentId) => !room.members.includes(agentId))
  return (
    <header data-testid="room-header" className="flex flex-col gap-[var(--space-2)] border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <AddParticipant target={{ kind: 'room', room }} />
        <h1 className="flex-1 truncate text-[length:var(--type-body-size)] font-semibold text-[var(--color-secondary)]">{room.name}</h1>
        {origin && <MainChatLink workspace={workspace} agentId={origin} label={`Back to ${agentName(origin)}'s main chat`} />}
        <span className="flex items-baseline gap-[var(--space-1)]">
          <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">allocated cost</span>
          <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{formatAllocatedCost(room.allocated_cost_micro_usd)}</span>
          {room.allocated_cost_unpriced && (
            <span className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">plus unpriced turns</span>
          )}
        </span>
        <DeleteGroup room={room} />
      </div>
      {room.paused && (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
          Group paused — new messages are kept here and not sent to members until /resume.
        </p>
      )}
      <ul aria-label="Members" className="flex flex-wrap gap-x-[var(--space-4)] gap-y-[var(--space-1)]">
        {room.members.map((agentId) => {
          const name = agentName(agentId)
          const muted = room.muted.includes(agentId)
          return (
            <li key={agentId} className="flex flex-wrap items-center gap-[var(--space-1)]">
              <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{name}</span>
              {muted && <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">muted</span>}
              <MemberMenu room={room} agentId={agentId} name={name} muted={muted} update={membership.update} />
              <MemberState agentId={agentId} name={name} delivery={memberStates[agentId]} workspace={workspace} onAskAgain={onAskAgain} />
            </li>
          )
        })}
      </ul>
      {formerMembers.length > 0 && (
        // FR-252: an agent that left keeps its last request state ("cancelled — removed").
        <ul aria-label="Former members" className="flex flex-wrap gap-x-[var(--space-4)] gap-y-[var(--space-1)]">
          {formerMembers.map((agentId) => (
            <li key={agentId} className="flex flex-wrap items-center gap-[var(--space-1)] text-[var(--color-muted)]">
              <span className="text-[length:var(--type-body-compact-size)]">{agentName(agentId)}</span>
              <MemberState agentId={agentId} name={agentName(agentId)} delivery={memberStates[agentId]} workspace={workspace} onAskAgain={onAskAgain} />
            </li>
          ))}
        </ul>
      )}
      {membership.error && (
        <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{membership.error}</p>
      )}
    </header>
  )
}
