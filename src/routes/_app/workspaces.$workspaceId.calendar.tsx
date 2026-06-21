import { createFileRoute } from '@tanstack/react-router'
import { lazy, Suspense } from 'react'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Calendar tab — scheduled/recurring tasks by fire time. Renders the existing
// CalendarScreen under the workspace tab. Code-split (it's a heavy surface).
const CalendarScreen = lazy(() =>
  import('@/components/screens/CalendarScreen').then((m) => ({ default: m.CalendarScreen })),
)

function WorkspaceCalendarRoute() {
  const { workspaceId } = Route.useParams()
  return (
    <Suspense fallback={<RouteFallback />}>
      <CalendarScreen workspaceId={workspaceId} />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/calendar')({
  component: WorkspaceCalendarRoute,
})
