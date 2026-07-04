import { createFileRoute } from '@tanstack/react-router'
import { CalendarScreen } from '@/components/screens/CalendarScreen'

// Calendar tab — scheduled/recurring tasks by fire time, under the workspace
// tab. autoCodeSplitting extracts this component (and CalendarScreen) into a
// lazy chunk; the router-level defaultPendingComponent supplies the fallback.
// A named wrapper is kept because the screen needs the workspaceId route param
// passed as a prop.
function WorkspaceCalendarRoute() {
  const { workspaceId } = Route.useParams()
  return <CalendarScreen workspaceId={workspaceId} />
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/calendar')({
  component: WorkspaceCalendarRoute,
})
