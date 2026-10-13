// The agent list is fetched. A live reply's mark needs figure, role and
// colour in the same paint as the phrase — callers that render inside
// <Suspense> suspend until that record arrives instead of flashing a blank mark.
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { fetchAgents } from '@/lib/api'
import type { Agent } from '@/lib/api'

const AGENTS_KEY = ['agents'] as const

export function useChatAgents(): Agent[] {
  const query = useQuery({ queryKey: AGENTS_KEY, queryFn: fetchAgents })
  const client = useQueryClient()
  if (query.isPending && query.fetchStatus === 'fetching' && typeof client.ensureQueryData === 'function') {
    // A failed fetch must not crash the bubble: settle the thrown promise and
    // let the query's error state render with no mark.
    throw client.ensureQueryData({ queryKey: AGENTS_KEY, queryFn: fetchAgents }).then(
      () => undefined,
      () => undefined,
    )
  }
  return query.data ?? []
}
