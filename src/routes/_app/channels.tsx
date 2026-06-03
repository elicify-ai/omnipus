import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the channels screen (cards + config slide-over) lazy-loads into
// its own chunk. The screen body lives in @/components/screens/ChannelsScreen
// (also imported directly by tests).
const ChannelsScreen = lazy(() =>
  import('@/components/screens/ChannelsScreen').then((m) => ({ default: m.ChannelsScreen })),
)

function ChannelsRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <ChannelsScreen />
    </Suspense>
  )
}

export const Route = createFileRoute('/_app/channels')({
  component: ChannelsRoute,
})
