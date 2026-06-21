import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceTasksTab } from '@/components/workspaces/WorkspaceTasksTab'

// Board tab — task kanban by the 7-state lifecycle. Renders the existing
// BoardView under the tab. F2 extends it with delegation roll-ups.
function WorkspaceBoardRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceTasksTab workspaceId={workspaceId} mode="board" />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/board')({
  component: WorkspaceBoardRoute,
})
