import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the Profile screen (personal preferences, password, workspace
// context) is its own chunk. Distinct from Settings (app configuration) per the
// Spec-6 Profile vs Settings split.
const ProfileScreen = lazy(() =>
  import('@/components/screens/ProfileScreen').then((m) => ({ default: m.ProfileScreen })),
)

function ProfileRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <ProfileScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/profile')({
  component: ProfileRoute,
})
