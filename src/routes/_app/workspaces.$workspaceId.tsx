import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: WorkspaceDetailScreen is heavy (board + list + milestones) and only
// needed on this detail route — lazy-load it into its own chunk.
const WorkspaceDetailScreen = lazy(() =>
  import('@/components/screens/WorkspaceDetailScreen').then((m) => ({ default: m.WorkspaceDetailScreen })),
)

function WorkspaceDetailRoute() {
  const { workspaceId } = Route.useParams()
  return (
    <Suspense fallback={<RouteFallback />}>
      <WorkspaceDetailScreen workspaceId={workspaceId} />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId')({
  component: WorkspaceDetailRoute,
})
