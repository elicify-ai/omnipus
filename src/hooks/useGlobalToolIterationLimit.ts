import { useContext } from 'react'
import { QueryClient, QueryClientContext, useQuery } from '@tanstack/react-query'
import { fetchPerformanceSettings } from '@/lib/api'

// Used only when no QueryClientProvider is mounted (the create wizard's
// step components are rendered query-client-free in their unit tests); the
// query is then disabled, so this client never fetches anything.
const detachedClient = new QueryClient()

/**
 * The global tool-iteration limit IN FORCE ("Max tool calls per turn",
 * issue #904), read from `GET /api/v1/performance`
 * (`PerformanceSettings.max_tool_iterations`). Shares the
 * `['performance-settings']` query with Settings → Performance, so a change
 * saved there is reflected here without a second fetch.
 *
 * Returns `undefined` while loading, when the request fails (e.g. 503 under
 * dev-mode bypass) or when no QueryClientProvider is mounted. Callers must
 * then render the limit without a number — the SPA never prints a literal
 * default (spec FR-004).
 *
 * `enabled: false` keeps the hook from reading at all. A caller that is
 * mounted while not shown (the shell-hosted AgentProfile, closed on every
 * page) passes its visibility here, so a page load never reads this
 * admin-only route just because the closed editor exists.
 */
export function useGlobalToolIterationLimit({ enabled = true }: { enabled?: boolean } = {}): number | undefined {
  const contextClient = useContext(QueryClientContext)
  const { data } = useQuery(
    {
      queryKey: ['performance-settings'],
      // Deferred access: a test double of '@/lib/api' without this export
      // fails the query (handled) instead of throwing during render.
      queryFn: () => fetchPerformanceSettings(),
      enabled: enabled && contextClient !== undefined,
    },
    contextClient ?? detachedClient,
  )
  return data?.max_tool_iterations
}
