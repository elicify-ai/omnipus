import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the trust-graph screen lazy-loads into its own chunk, matching the
// agents.index / agents.$agentId pattern. Static `/agents/trust` takes priority
// over the dynamic `/agents/$agentId` sibling in TanStack Router, so the literal
// "trust" segment resolves here, not as an agent id.
const TrustGraphScreen = lazy(() =>
  import('@/components/screens/TrustGraphScreen').then((m) => ({
    default: m.TrustGraphScreen,
  })),
)

function TrustGraphRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <TrustGraphScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/agents/trust')({
  component: TrustGraphRoute,
})
