// RouteErrorFallback — shown when a lazily-loaded route chunk fails to load
// (e.g. the user's tab references a chunk hash an old deployment no longer
// serves). Reloading re-fetches the current asset manifest, which resolves
// the stale-chunk case. Mirrors the pre-existing AgentsError pattern
// (src/routes/_app/agents.tsx) so the same recovery affordance is available
// on every route, not just /agents.
export function RouteErrorFallback() {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-center px-4">
      <p className="text-sm text-[var(--color-error)]">Something went wrong loading this page.</p>
      <button
        type="button"
        onClick={() => window.location.reload()}
        className="text-xs text-[var(--color-accent)] underline underline-offset-2"
      >
        Reload page
      </button>
    </div>
  )
}
