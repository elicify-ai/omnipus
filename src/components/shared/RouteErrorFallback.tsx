// RouteErrorFallback — shown when a lazily-loaded route chunk fails to load
// (e.g. the user's tab references a chunk hash an old deployment no longer
// serves). Reloading re-fetches the current asset manifest, which resolves
// the stale-chunk case. Mirrors the pre-existing AgentsError pattern
// (src/routes/_app/agents.tsx) so the same recovery affordance is available
// on every route, not just /agents.
import { Button } from '@/components/ui/button'

export function RouteErrorFallback() {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-[var(--space-2-5)] text-center px-[var(--space-3)]">
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">Something went wrong loading this page.</p>
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
