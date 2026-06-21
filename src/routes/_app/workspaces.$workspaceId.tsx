import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceTabContainer } from '@/components/workspaces/WorkspaceTabContainer'

// The workspace detail screen is a tabbed container (Chat/Board/List/Graph/
// Calendar/Team/Settings). This route resolves the workspace, renders the
// sticky tab bar, and hosts the active tab via <Outlet/>. Tab bodies live in
// the workspaces.$workspaceId.<tab>.tsx sibling route files.
function WorkspaceDetailRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceTabContainer workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId')({
  component: WorkspaceDetailRoute,
})
