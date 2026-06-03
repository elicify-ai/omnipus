import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the agent list screen (cards, create-agent modal) lazy-loads into
// its own chunk. The screen body lives in @/components/screens/AgentListScreen
// (also imported directly by tests and re-exported from agents.tsx).
const AgentListScreen = lazy(() =>
  import('@/components/screens/AgentListScreen').then((m) => ({
    default: m.AgentListScreen,
  })),
)

function AgentsIndexRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <AgentListScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/agents/')({
  component: AgentsIndexRoute,
})
