import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the settings screen (all section panels — providers, security,
// gateway, data, profile, devices, users, about) is heavy and only needed on
// this route. Lazy-load it into its own chunk. The screen body lives in
// @/components/screens/SettingsScreen (also imported directly by tests).
const SettingsScreen = lazy(() =>
  import('@/components/screens/SettingsScreen').then((m) => ({ default: m.SettingsScreen })),
)

function SettingsRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <SettingsScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/settings')({
  component: SettingsRoute,
})
