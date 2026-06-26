import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the Usage analytics screen is heavy and only needed on this route.
const UsageScreen = lazy(() =>
  import('@/components/screens/UsageScreen').then((m) => ({ default: m.UsageScreen })),
)

function UsageRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <UsageScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/usage')({
  component: UsageRoute,
})
