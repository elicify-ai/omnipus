// RoomHeader — a group chat's header (omnipus-system-agent FR-158, FR-171,
// FR-175, FR-201, FR-204, FR-207, FR-208; §6.11, §6.14).
//
// Shows the members with each one's most recent request state (§6.11 labels;
// the approval and question waits link to that member's main chat, where the
// card lives — nothing is approved or answered here, FR-169), the room's
// "allocated cost" (§6.14), the pause state, the link back to the main chat a
// group was started from (FR-207), Add participant (FR-208) and membership
// editing: mute/unmute (every kind), remove and delete (squads and ad-hoc).

import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { DotsThreeVertical } from '@phosphor-icons/react'
import type { Room, RoomMembersUpdateRequest } from '@/lib/api/generated/openapi-types'
import type { RoomDeliveryState } from '@/lib/api/generated/asyncapi-types'
import type { Workspace } from '@/lib/api'
import { deleteRoom, roomsQueryKeys, updateRoomMembers } from '@/lib/api/rooms'
import { mutationFailureMessage } from '@/lib/mutationOutcome'
import { AddParticipant } from '@/components/chat/AddParticipant'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { deliveryLabel, deliveryNeedsMainChatLink, formatAllocatedCost, memberMainSessionId } from './roomModel'

export interface RoomHeaderProps {
  room: Room
  workspace: Workspace | undefined
  agentName: (agentId: string) => string
  memberStates: Record<string, RoomDeliveryState>
  onAskAgain: (agentId: string) => void
}

function MainChatLink({ workspace, agentId, label }: { workspace: Workspace | undefined; agentId: string; label: string }) {
  const sessionId = memberMainSessionId(workspace, agentId)
  if (!sessionId) return null
  return (
    <Link
      tabIndex={0}
      to="/sessions/$sessionId"
      params={{ sessionId }}
      className="text-[length:var(--type-caption-size)] text-[var(--color-accent)] underline-offset-2 hover:underline"
    >
      {label}
    </Link>
  )
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

function useMembership(room: Room) {
  const client = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  async function update(request: RoomMembersUpdateRequest) {
    setError(null)
    try {
      const updated = await updateRoomMembers(room.id, request)
      client.setQueryData(roomsQueryKeys.detail(room.id), updated)
      void client.invalidateQueries({ queryKey: roomsQueryKeys.list(room.workspace_id) })
    } catch (failure) {
      setError(mutationFailureMessage(failure))
    }
  }
  return { error, update }
}

function MemberMenu({ room, agentId, name, muted, update }: {
  room: Room
  agentId: string
  name: string
  muted: boolean
  update: (request: RoomMembersUpdateRequest) => Promise<void>
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <IconButton size="sm" aria-label={`Options for ${name}`}>
          <DotsThreeVertical aria-hidden="true" size={14} />
        </IconButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="border border-[var(--color-border)] bg-[var(--color-surface-1)]">
        <DropdownMenuItem onSelect={() => void update({ action: muted ? 'unmute' : 'mute', agent_ids: [agentId] })}>
          {muted ? 'Unmute' : 'Mute'}
        </DropdownMenuItem>
        {room.kind !== 'team' && (
          <DropdownMenuItem onSelect={() => void update({ action: 'remove', agent_ids: [agentId] })}>
            Remove from group
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function DeleteGroup({ room }: { room: Room }) {
  const navigate = useNavigate()
  const client = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (room.kind === 'team') return null
  async function remove() {
    try {
      await deleteRoom(room.id)
      void client.invalidateQueries({ queryKey: roomsQueryKeys.list(room.workspace_id) })
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: room.workspace_id } })
    } catch (failure) {
      setError(mutationFailureMessage(failure))
    }
  }
  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setConfirming(true)}>Delete group</Button>
      {error && <span role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{error}</span>}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${room.name}?`}
        description="The group and its conversation are deleted. Work already running in members' main chats finishes there."
        cancelLabel="Keep group"
        confirmLabel="Delete group"
        onConfirm={() => void remove()}
      />
    </>
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
