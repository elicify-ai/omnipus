import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceGraphTab } from '@/components/workspaces/WorkspaceGraphTab'

// Graph tab — the Task DAG (F2). Placeholder body for this wave; the route +
// tab are wired so F2 only swaps the WorkspaceGraphTab body.
function WorkspaceGraphRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceGraphTab workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/graph')({
  component: WorkspaceGraphRoute,
})
