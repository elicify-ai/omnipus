// RouteErrorFallback — shown when a lazily-loaded route chunk fails to load
// (e.g. the user's tab references a chunk hash an old deployment no longer
// serves). Reloading re-fetches the current asset manifest, which resolves
// the stale-chunk case. Mirrors the pre-existing AgentsError pattern
// (src/routes/_app/agents.tsx) so the same recovery affordance is available
// on every route, not just /agents.
import { useRouter, type ErrorComponentProps } from '@tanstack/react-router'
import { Button } from '@/components/ui/button'

// Both props are optional: RootErrorBoundary (src/main.tsx) renders this
// outside the router with neither; the router passes both for a route error.
export function RouteErrorFallback({ error, reset }: Partial<ErrorComponentProps>) {
  // warn:false — outside RouterProvider there is no router; Try again then
  // only resets (or is hidden when there is no reset to call).
  const router = useRouter({ warn: false })
  const message = error instanceof Error ? error.message : error ? String(error) : ''
  const tryAgain = () => {
    // Re-run the route's loaders, then clear the error boundary in place.
    void router?.invalidate()
    reset?.()
  }
  return (
    <div className="flex flex-col items-center justify-center h-full gap-[var(--space-2-5)] text-center px-[var(--space-3)]">
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">Something went wrong loading this page.</p>
      {message && (
        <p className="max-w-prose break-words text-[length:var(--type-utility-xs-size)] text-[var(--color-text-muted)]">{message}</p>
      )}
      {reset && (
        <Button variant="outline" onClick={tryAgain}>
          Try again
        </Button>
      )}
      <Button
        variant="link"
        onClick={() => window.location.reload()}
        className="text-[length:var(--type-utility-xs-size)] underline underline-offset-2"
      >
        Reload page
      </Button>
    </div>
  )
}
