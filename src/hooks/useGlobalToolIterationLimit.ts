import { useQuery } from '@tanstack/react-query'
import { fetchPerformanceSettings } from '@/lib/api'

/**
 * The global tool-iteration limit IN FORCE ("Max tool calls per turn",
 * issue #904), read from `GET /api/v1/performance`
 * (`PerformanceSettings.max_tool_iterations`). Shares the
 * `['performance-settings']` query with Settings → Performance, so a change
 * saved there is reflected here without a second fetch.
 *
 * Returns `undefined` while loading or when the request fails (e.g. 503
 * under dev-mode bypass). Callers must then render the limit without a
 * number — the SPA never prints a literal default (spec FR-004).
 */
export function useGlobalToolIterationLimit(): number | undefined {
  const { data } = useQuery({
    queryKey: ['performance-settings'],
    // Deferred access: a test double of '@/lib/api' without this export
    // fails the query (handled) instead of throwing during render.
    queryFn: () => fetchPerformanceSettings(),
  })
  return data?.max_tool_iterations
}
