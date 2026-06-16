import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: ProjectDetailScreen is heavy (board + list + milestones) and only
// needed on this detail route — lazy-load it into its own chunk.
const ProjectDetailScreen = lazy(() =>
  import('@/components/screens/ProjectDetailScreen').then((m) => ({ default: m.ProjectDetailScreen })),
)

function WorkspaceDetailRoute() {
  const { workspaceId } = Route.useParams()
  return (
    <Suspense fallback={<RouteFallback />}>
      <ProjectDetailScreen workspaceId={workspaceId} />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/workspaces/$workspaceId')({
  component: WorkspaceDetailRoute,
})
