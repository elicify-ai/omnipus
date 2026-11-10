// WorkspaceGroupChats — the sidebar's "Group chats" section of one workspace
// (omnipus-system-agent FR-160, FR-161, FR-174; §6.3; K23; BDD-17.1/2).
//
// Under the workspace's agent rows: the team room first, then squads, then
// ad-hoc groups, each with the attention marker. A hidden team room (fewer
// than two conversation agents) is not listed; its history is kept by the
// server. Loading, true-empty and failed states are distinct (§6.3): a failed
// read is never shown as "No group chats yet". Creating a group happens here,
// from the web app only (FR-161).

import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Plus, UsersThree } from '@phosphor-icons/react'
import type { Room } from '@/lib/api/generated/openapi-types'
import type { Workspace } from '@/lib/api'
import { fetchWorkspaceRooms, roomsQueryKeys } from '@/lib/api/rooms'
import { queryClient } from '@/lib/queryClient'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { ErrorState } from '@/components/ui/error-state'
import { SquadDialog } from '@/components/rooms/SquadDialog'

const KIND_ORDER: Record<Room['kind'], number> = { team: 0, squad: 1, adhoc: 2 }

/** FR-174: team room first, then squads, then ad-hoc groups; hidden team rooms are not listed. */
export function sidebarRooms(rooms: readonly Room[]): Room[] {
  return rooms
    .filter((room) => !room.hidden)
    .map((room, index) => ({ room, index }))
    .sort((a, b) => KIND_ORDER[a.room.kind] - KIND_ORDER[b.room.kind] || a.index - b.index)
    .map(({ room }) => room)
}

function RoomRow({ room, onOverlayClose }: { room: Room; onOverlayClose: () => void }) {
  const navigate = useNavigate()
  return (
    <Button
      variant="ghost"
      data-testid={`room-row-${room.id}`}
      onClick={() => {
        void navigate({ to: '/workspaces/$workspaceId/rooms/$roomId', params: { workspaceId: room.workspace_id, roomId: room.id } })
        onOverlayClose()
      }}
      className="h-auto w-full justify-start gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] pointer-coarse:min-h-[var(--target-touch-minimum)]"
    >
      <UsersThree size={14} aria-hidden="true" />
      <span className="truncate">{room.name}</span>
      {room.needs_attention && (
        <span role="status" aria-label="Needs attention" className="ml-auto size-[var(--space-2)] shrink-0 rounded-full bg-[var(--color-accent)]" />
      )}
    </Button>
  )
}

export function WorkspaceGroupChats({ workspace, onOverlayClose }: { workspace: Workspace; onOverlayClose: () => void }) {
  const [dialog, setDialog] = useState<'squad' | 'adhoc' | null>(null)
  // The app's one client, passed explicitly: WorkspaceAgentList is props-driven and renders without a provider.
  const roomsQuery = useQuery({ queryKey: roomsQueryKeys.list(workspace.id), queryFn: () => fetchWorkspaceRooms(workspace.id) }, queryClient)
  const rooms = sidebarRooms(roomsQuery.data ?? [])
  const stale = roomsQuery.isError && roomsQuery.data !== undefined
  return (
    <div data-testid={`group-chats-${workspace.id}`} className="mt-[var(--space-2)] flex flex-col">
      <div className="flex items-center pr-[var(--space-2)]">
        <span className="flex-1 px-[var(--space-2-5)] text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
          Group chats
        </span>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton size="sm" aria-label={`New group chat in ${workspace.name}`}>
              <Plus size={12} aria-hidden="true" />
            </IconButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="border border-[var(--color-border)] bg-[var(--color-surface-1)]">
            <DropdownMenuItem onSelect={() => setDialog('squad')}>New squad…</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setDialog('adhoc')}>New quick group…</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {stale && (
        <ErrorState
          message="Could not refresh group chats — showing cached, out-of-date groups"
          onRetry={() => void roomsQuery.refetch()}
          className="px-[var(--space-2-5)] py-[var(--space-1)]"
        />
      )}
      {roomsQuery.isError && !stale ? (
        <div className="flex items-center gap-[var(--space-2)] pr-[var(--space-3)] py-[var(--space-1)]">
          <span className="flex-1 px-[var(--space-2-5)] text-[length:var(--type-caption-size)] text-[var(--color-error)]">Could not load group chats</span>
          <Button
            variant="ghost"
            onClick={() => void roomsQuery.refetch()}
            className="h-auto shrink-0 px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)]"
          >
            Retry
          </Button>
        </div>
      ) : roomsQuery.isPending ? (
        <span role="status" className="px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Loading group chats…</span>
      ) : rooms.length === 0 && !stale ? (
        <span className="px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">No group chats yet</span>
      ) : (
        rooms.map((room) => <RoomRow key={room.id} room={room} onOverlayClose={onOverlayClose} />)
      )}
      {dialog && (
        <SquadDialog
          open
          onOpenChange={(open) => { if (!open) setDialog(null) }}
          workspaceId={workspace.id}
          kind={dialog}
        />
      )}
    </div>
  )
}
