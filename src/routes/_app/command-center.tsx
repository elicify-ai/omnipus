import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the Command Center screen (StatusBar, TaskList, schedules,
// activity feed, task detail panel) is heavy and only needed when this route
// is visited. Lazy-load it into its own chunk; the eager bundle keeps only this
// thin wrapper + Suspense fallback. The screen body lives in
// @/components/screens/CommandCenterScreen (also imported directly by tests).
const CommandCenterScreen = lazy(() =>
  import('@/components/screens/CommandCenterScreen').then((m) => ({
    default: m.CommandCenterScreen,
  })),
)

function CommandCenterRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <CommandCenterScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/command-center')({
  component: CommandCenterRoute,
})
