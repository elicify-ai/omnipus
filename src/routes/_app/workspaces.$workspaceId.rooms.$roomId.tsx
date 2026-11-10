import { createFileRoute, redirect } from '@tanstack/react-router'
import { RoomScreen } from '@/components/rooms/RoomScreen'
import { ROOMS_ENABLED } from '@/lib/featureSwitches'

// /workspaces/{id}/rooms/{roomId} — one group chat (omnipus-system-agent
// FR-174, FR-175, FR-199). Opened from the sidebar's Group chats section, the
// squad dialog, Add participant, and the "Open group" link on room traffic in
// a main chat. A room is not a session: this route attaches the room only.
function RoomRoute() {
  const { workspaceId, roomId } = Route.useParams()
  return <RoomScreen workspaceId={workspaceId} roomId={roomId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/rooms/$roomId')({
  // Hidden until WU-15 (stage 3), #1314: a direct URL lands on the workspace's
  // chat instead of a room page whose server does not exist yet.
  beforeLoad: ({ params }) => {
    if (!ROOMS_ENABLED) {
      throw redirect({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: params.workspaceId }, replace: true })
    }
  },
  component: RoomRoute,
})
