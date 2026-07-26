import { QueryClient } from '@tanstack/react-query'
import { ApiSchemaError, ApiError } from './api'
import { forceLogout } from './authLogout'

// ── Global 401 logout handler ─────────────────────────────────────────────────
//
// Debounced: once a 401 is detected, subsequent concurrent 401 failures within
// the same 2-second window are suppressed so concurrent polling queries don't
// each trigger a redirect. The redirect fires synchronously on the first call;
// only the debounce-flag reset uses setTimeout (see authLogout.ts).

function _handleAuthError(err: unknown): void {
  if (!(err instanceof ApiError)) return
  if (err.status !== 401) return
  forceLogout()
}

// ── Retry predicates ─────────────────────────────────────────────────────────
//
// Exported (rather than inlined into `defaultOptions` below) so tests can
// exercise the REAL predicate instead of hand-maintaining a parallel copy that
// can silently drift from production behaviour.

/**
 * Retry predicate for QUERIES. Never retry ApiSchemaError — retrying cannot
 * fix a schema mismatch and would produce a toast storm (4 toasts per failure
 * with default retry:3). Never retry 401/403 — these are auth errors that
 * will keep failing with the same token and would flood the console before
 * the redirect fires. Never retry 404 — a missing resource will not appear on
 * retry (e.g. a just-deleted schedule whose refetch fires after the delete
 * onSuccess). Every other error (including any other 4xx, every 5xx, and
 * network failures) retries up to 3 times.
 */
export function shouldRetryQuery(failureCount: number, err: unknown): boolean {
  return (
    !(err instanceof ApiSchemaError) &&
    !(err instanceof ApiError && (err.status === 401 || err.status === 403 || err.status === 404)) &&
    failureCount < 3
  )
}

/**
 * Retry predicate for MUTATIONS.
 *
 * S3 UAT finding: this predicate previously mirrored `shouldRetryQuery`
 * exactly (only 401/403/404 excluded), so a deterministic 400 (e.g. plan
 * creation's "invalid owner_agent_id") was retried like a transient failure —
 * up to 4 identical POSTs for a single Create click, all doomed to fail the
 * same way, with the user-facing error toast only appearing after the last
 * one (~7s later). Fixed here: ANY 4xx is excluded for mutations, not just
 * 401/403/404 — a client/validation error can never succeed by resending the
 * exact same request, and mutations are frequently non-idempotent (POST
 * create), so blindly retrying also risks duplicate side effects on top of
 * the wasted round trips.
 *
 * Preserved deliberately: 5xx (the server may recover) and network failures
 * (status 0 — the request never reached the server) are still retried up to
 * 3 times, same as queries. That distinction is the point of this predicate
 * existing separately from `shouldRetryQuery` — do not collapse them back
 * into one shared function.
 */
export function shouldRetryMutation(failureCount: number, err: unknown): boolean {
  return (
    !(err instanceof ApiSchemaError) &&
    !(err instanceof ApiError && err.status >= 400 && err.status < 500) &&
    failureCount < 3
  )
}

// Singleton QueryClient — created once and shared between:
//   - main.tsx (passed to QueryClientProvider)
//   - chat store (for WS-driven query invalidation)
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: shouldRetryQuery,
      retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 30_000),
    },
    mutations: {
      retry: shouldRetryMutation,
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
    _handleAuthError(event.action.error)
  }
})

queryClient.getMutationCache().subscribe((event) => {
  if (event.type === 'updated' && event.mutation.state.status === 'error') {
    _handleApiSchemaError(event.mutation.state.error)
    _handleAuthError(event.mutation.state.error)
  }
})
