import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceChatTab } from '@/components/workspaces/WorkspaceChatTab'

// Chat tab — the workspace's default front view (chat-first). Agent picker is
// scoped to the workspace team; sessions are filtered by this workspace_id.
function WorkspaceChatRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceChatTab workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/chat')({
  component: WorkspaceChatRoute,
})
