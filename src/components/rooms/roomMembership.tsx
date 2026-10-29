// roomMembership — the group membership controls shared by the group
// surfaces (#1449: moved out of RoomHeader for the Participants panel, logic
// unchanged): the member's main-chat link, the mute/unmute/remove menu, its
// update call and Delete group (squads and ad-hoc groups only, FR-208).

import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { DotsThreeVertical } from '@phosphor-icons/react'
import type { Room, RoomMembersUpdateRequest } from '@/lib/api/generated/openapi-types'
import type { Workspace } from '@/lib/api'
import { deleteRoom, roomsQueryKeys, updateRoomMembers } from '@/lib/api/rooms'
import { mutationFailureMessage } from '@/lib/mutationOutcome'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { memberMainSessionId } from './roomModel'

export function MainChatLink({ workspace, agentId, label }: { workspace: Workspace | undefined; agentId: string; label: string }) {
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

export function useMembership(room: Pick<Room, 'id' | 'workspace_id'>) {
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

export function MemberMenu({ room, agentId, name, muted, update }: {
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

export function DeleteGroup({ room }: { room: Room }) {
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
