import { createFileRoute } from '@tanstack/react-router'
import { RoomScreen } from '@/components/rooms/RoomScreen'

// /workspaces/{id}/rooms/{roomId} — one group chat (omnipus-system-agent
// FR-174, FR-175, FR-199). Opened from the sidebar's Group chats section, the
// squad dialog, Add participant, and the "Open group" link on room traffic in
// a main chat. A room is not a session: this route attaches the room only.
function RoomRoute() {
  const { workspaceId, roomId } = Route.useParams()
  return <RoomScreen workspaceId={workspaceId} roomId={roomId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/rooms/$roomId')({
  component: RoomRoute,
})
