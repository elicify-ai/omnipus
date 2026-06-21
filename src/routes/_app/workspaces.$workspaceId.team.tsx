import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceTeamTab } from '@/components/workspaces/WorkspaceTeamTab'

// Team tab — the per-workspace delegation-graph editor (F2). Placeholder body
// for this wave; the route + tab are wired so F2 only swaps the
// WorkspaceTeamTab body.
function WorkspaceTeamRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceTeamTab workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/team')({
  component: WorkspaceTeamRoute,
})
