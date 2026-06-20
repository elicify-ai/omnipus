import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the Automations screen (trigger→action rules + token usage) is
// only needed when this route is visited. The screen body lives in
// @/components/screens/AutomationsScreen.
const AutomationsScreen = lazy(() =>
  import('@/components/screens/AutomationsScreen').then((m) => ({ default: m.AutomationsScreen })),
)

function AutomationsRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <AutomationsScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/automations')({
  component: AutomationsRoute,
})
