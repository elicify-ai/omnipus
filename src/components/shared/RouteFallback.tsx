import { SkeletonList } from './ListStates'

// RouteFallback — Suspense fallback shown while a lazily-loaded route chunk is
// fetched. Lightweight and dependency-free so it lives in the eager bundle and
// renders instantly. Reuses the existing SkeletonList shimmer for visual
// consistency with in-screen loading states.
export function RouteFallback() {
  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="max-w-4xl mx-auto px-4 py-6">
        <SkeletonList />
      </div>
    </div>
  )
}
