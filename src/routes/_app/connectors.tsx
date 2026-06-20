import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the connectors screen (cards + config slide-over) lazy-loads into
// its own chunk. The screen body lives in @/components/screens/ConnectorsScreen
// (also imported directly by tests).
const ConnectorsScreen = lazy(() =>
  import('@/components/screens/ConnectorsScreen').then((m) => ({ default: m.ConnectorsScreen })),
)

function ConnectorsRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <ConnectorsScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/connectors')({
  component: ConnectorsRoute,
})
