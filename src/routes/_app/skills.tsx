import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the Skills & Tools screen (3 tabs, skill browser modal, MCP
// modal, delete dialogs) is heavy and only needed on this route. Lazy-load it
// into its own chunk. The screen body lives in @/components/screens/SkillsScreen
// (also imported directly by tests).
const SkillsScreen = lazy(() =>
  import('@/components/screens/SkillsScreen').then((m) => ({ default: m.SkillsScreen })),
)

function SkillsRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <SkillsScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/skills')({
  component: SkillsRoute,
})
