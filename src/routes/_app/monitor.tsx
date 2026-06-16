import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the Monitor screen (schedules + token usage) is only needed when
// this route is visited. The screen body lives in @/components/screens/MonitorScreen.
const MonitorScreen = lazy(() =>
  import('@/components/screens/MonitorScreen').then((m) => ({ default: m.MonitorScreen })),
)

function MonitorRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <MonitorScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/monitor')({
  component: MonitorRoute,
})
