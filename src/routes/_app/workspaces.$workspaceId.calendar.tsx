import { createFileRoute } from '@tanstack/react-router'
import { lazy, Suspense } from 'react'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: CalendarScreen is a dedicated surface (board tasks + milestones
// by date). Lazy-loaded so it does not inflate the main bundle.
const CalendarScreen = lazy(() =>
  import('@/components/screens/CalendarScreen').then((m) => ({ default: m.CalendarScreen })),
)

function CalendarRoute() {
  const { workspaceId } = Route.useParams()
  return (
    <Suspense fallback={<RouteFallback />}>
      <CalendarScreen workspaceId={workspaceId} />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId/calendar')({
  component: CalendarRoute,
})
