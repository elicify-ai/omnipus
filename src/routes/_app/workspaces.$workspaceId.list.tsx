import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceTasksTab } from '@/components/workspaces/WorkspaceTasksTab'

// List tab — flat, filterable task table. Renders the existing ListView.
function WorkspaceListRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceTasksTab workspaceId={workspaceId} mode="list" />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/list')({
  component: WorkspaceListRoute,
})
