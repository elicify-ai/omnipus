import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: AgentProfile (accordion, tools/permissions panels) is heavy and
// only needed on this detail route — lazy-load it into its own chunk.
const AgentProfile = lazy(() =>
  import('@/components/agents/AgentProfile').then((m) => ({ default: m.AgentProfile })),
)

function AgentProfileRoute() {
  const { agentId } = Route.useParams()
  return (
    <Suspense fallback={<RouteFallback />}>
      <AgentProfile agentId={agentId} />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/agents/$agentId')({
  component: AgentProfileRoute,
})
