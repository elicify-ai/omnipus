import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceTasksTab } from '@/components/workspaces/WorkspaceTasksTab'

// The "Tasks" tab (ADR-051 D1) — kept on the `board` route segment to avoid
// breaking deep links, but the screen itself now combines Board / List /
// Graph behind an in-screen view switcher, with a plans-as-filter band above
// it. See WorkspaceTasksTab.tsx.
function WorkspaceBoardRoute() {
  const { workspaceId } = Route.useParams()
  return <WorkspaceTasksTab workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/board')({
  component: WorkspaceBoardRoute,
})
