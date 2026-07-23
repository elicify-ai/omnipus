import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceMediaTab } from '@/components/workspaces/WorkspaceMediaTab'

// Media tab — workspace media library (ADR-051 Rev 4 / Slice H). Lists the
// workspace's persistent uploads and supports explicit delete (FR-008).
function WorkspaceMediaRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceMediaTab workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/media')({
  component: WorkspaceMediaRoute,
})
