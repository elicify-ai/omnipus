// useQueryRefreshFailed — "a refresh failed and the screen still shows the
// last good data" (omnipus-system-agent spec §6.3, the "Stale / partial"
// column), read straight from the query cache.
//
// useQuery's observer delivers state changes on a deferred tick, so a stale
// hint driven by it appears one tick after the failed refresh settles. The
// cache's own listeners run synchronously when the query's state changes;
// subscribing to them here makes the hint appear in the same update as the
// failure it describes.
import { useCallback, useSyncExternalStore } from 'react'
import { hashKey, useQueryClient, type QueryKey } from '@tanstack/react-query'

export function useQueryRefreshFailed(queryKey: QueryKey): boolean {
  const client = useQueryClient()
  const keyHash = hashKey(queryKey)
  const subscribe = useCallback(
    (onChange: () => void) =>
      client.getQueryCache().subscribe((event) => {
        if (event.query.queryHash === keyHash) onChange()
      }),
    [client, keyHash],
  )
  const getSnapshot = useCallback(() => {
    const state = client.getQueryCache().get(keyHash)?.state
    return !!state && state.status === 'error' && state.data !== undefined
  }, [client, keyHash])
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
