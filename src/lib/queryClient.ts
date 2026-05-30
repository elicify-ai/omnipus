import { QueryClient } from '@tanstack/react-query'
import { ApiSchemaError } from './api'

// Singleton QueryClient — created once and shared between:
//   - main.tsx (passed to QueryClientProvider)
//   - chat store (for WS-driven query invalidation)
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      // Never retry ApiSchemaError — retrying cannot fix a schema mismatch and
      // would produce a toast storm (4 toasts per failure with default retry:3).
      retry: (failureCount, err) =>
        !(err instanceof ApiSchemaError) && failureCount < 3,
      retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 30_000),
    },
    mutations: {
      // Same guard for mutations: schema mismatches are not transient errors.
      retry: (failureCount, err) =>
        !(err instanceof ApiSchemaError) && failureCount < 3,
    },
  },
})

// Centralized ApiSchemaError handler — surfaces backend schema mismatches in
// production (not just DEV) so operators know when the server version drifts
// from the SPA's contract expectations. Toasts are lazy-imported to keep the
// ui store out of the api-module init path and allow dead-code elimination.
// Dedup map: keyed by endpoint, value is the timestamp of the last toast.
// Prevents a retry storm (retry:3 = 4 toasts) from the same endpoint within 5s.
const _schemaErrorLastToast = new Map<string, number>()
const _SCHEMA_ERROR_DEDUP_MS = 5_000


function _handleApiSchemaError(err: unknown): void {
  if (!(err instanceof ApiSchemaError)) return
  if (typeof window === 'undefined') return

  // Dedup: skip if we already toasted this endpoint within the dedup window.
  const now = Date.now()
  const lastToast = _schemaErrorLastToast.get(err.endpoint) ?? 0
  if (now - lastToast < _SCHEMA_ERROR_DEDUP_MS) return
  _schemaErrorLastToast.set(err.endpoint, now)

  console.error('[apiSchemaError]', err.endpoint, err.message, err)
  void import('@/store/ui')
    .then(({ useUiStore }) => {
      useUiStore.getState().addToast({
        message: 'Backend response failed validation. Server may be a different version. Please refresh.',
        variant: 'error',
      })
    })
    .catch((importErr) => console.error('[apiSchemaError] failed to load toast store', importErr))
}

queryClient.getQueryCache().subscribe((event) => {
  if (event.type === 'updated' && event.action.type === 'error') {
    _handleApiSchemaError(event.action.error)
  }
})

queryClient.getMutationCache().subscribe((event) => {
  if (event.type === 'updated' && event.mutation.state.status === 'error') {
    _handleApiSchemaError(event.mutation.state.error)
  }
})
