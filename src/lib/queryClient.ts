import { QueryClient } from '@tanstack/react-query'
import { ApiSchemaError, ApiError, validateToken } from './api'
import { forceLogout } from './authLogout'
import { checkTokenValidity, resetTokenValidationCache, type TokenVerdict } from '@/routes/authValidation'

// ── Global 401 logout handler ─────────────────────────────────────────────────
//
// A 401 on any ONE query/mutation does NOT necessarily mean the session is
// dead — the same status also covers a resource-scoped authorization
// decision (session is fine, this caller just isn't allowed THIS resource).
// Blindly forcing a logout on every 401 fabricates a "your session ended"
// story that the app's own session-validity check (GET /api/v1/auth/validate,
// via checkTokenValidity() — the exact same source of truth _app.tsx's route
// guard uses) can simultaneously be contradicting. Confirm before forcing the
// user out: only a CONFIRMED-invalid verdict triggers forceLogout(); an 'ok'
// or 'transient' (inconclusive network/5xx) verdict leaves the failure to the
// query's own isError UI (e.g. QueryErrorState.tsx / ProvidersSection's
// "Failed to load providers" state) instead of an incorrect global bounce.
//
// resetTokenValidationCache() forces a FRESH check rather than trusting
// authValidation.ts's 30s "ok" cache: the 401 we just observed is live
// evidence that may postdate the cached verdict, so re-validating on this
// signal is strictly more accurate than riding a stale cache window.
// Concurrent 401s (e.g. several panels failing at once) share ONE in-flight
// recheck via `_pendingValidityRecheck` so a burst doesn't fire a burst of
// /auth/validate calls.
//
// Debounce of the LOGOUT itself (once confirmed) is delegated entirely to
// forceLogout()'s own guard — see authLogout.ts.

let _pendingValidityRecheck: Promise<TokenVerdict> | null = null

function _recheckSessionValidity(): Promise<TokenVerdict> {
  if (!_pendingValidityRecheck) {
    resetTokenValidationCache()
    _pendingValidityRecheck = checkTokenValidity(validateToken).finally(() => {
      _pendingValidityRecheck = null
    })
  }
  return _pendingValidityRecheck
}

// Exported (like shouldRetryQuery/shouldRetryMutation above) so tests exercise
// the REAL handler instead of a hand-copied replica that can silently drift.
export async function handleAuthError(err: unknown): Promise<void> {
  if (!(err instanceof ApiError)) return
  if (err.status !== 401) return
  const verdict = await _recheckSessionValidity()
  if (verdict === 'unauthorized') {
    forceLogout()
  }
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
 * The narrow set of 4xx statuses that ARE retried for mutations, despite the
 * blanket 4xx exclusion below. Both are unambiguous, universal HTTP
 * "transient, try again" signals regardless of which endpoint returned them:
 *
 *   - 408 (Request Timeout) — the server gave up waiting on the client; the
 *     same request may well succeed on a fresh attempt.
 *   - 429 (Too Many Requests) — rate-limited/congested, not rejected as
 *     invalid. Confirmed against this backend's actual 429 producers (e.g.
 *     `HandleSystemCliValidate`'s per-caller in-flight cap,
 *     pkg/gateway/rest_clivalidate.go) — genuinely retryable congestion, not
 *     a validation error.
 *
 * 409 was evaluated and DELIBERATELY EXCLUDED (regression review on
 * bc66345f): this backend overloads 409 with two incompatible meanings on
 * the very SAME endpoint — `PATCH /tasks/{id}` (the board's task-move
 * mutation) returns 409 both for `agent.ErrDispatchCapReached` (genuine
 * transient congestion — retryable) AND for `agent.ErrPlanNotExecuting` /
 * `agent.ErrPlanStateUnresolvable` (a hard plan-state conflict — NOT
 * retryable), per pkg/gateway/rest_tasks.go's handleTaskPatch. Sibling
 * endpoints (task stop/restart) use 409 purely for state conflicts ("already
 * stopped", "not restartable"). The error body is a plain
 * `{"error": string}` (`jsonErr`, pkg/gateway/rest.go) with no
 * machine-readable field distinguishing the two cases, so the client cannot
 * tell them apart — blanket-retrying 409 would reintroduce exactly the
 * doomed-to-fail retry storm the original 4xx exclusion (S3 UAT finding,
 * below) was fixing, this time for plan-state conflicts instead of
 * validation errors. A non-idempotent create's "already exists" 409 must
 * likewise never be retried. If a future contract change gives 409 a
 * `code` field that distinguishes congestion from conflict, this set can be
 * widened to include it conditionally.
 */
const RETRYABLE_MUTATION_4XX_STATUSES = new Set([408, 429])

/**
 * Retry predicate for MUTATIONS.
 *
 * S3 UAT finding: this predicate previously mirrored `shouldRetryQuery`
 * exactly (only 401/403/404 excluded), so a deterministic 400 (e.g. plan
 * creation's "invalid owner_agent_id") was retried like a transient failure —
 * up to 4 identical POSTs for a single Create click, all doomed to fail the
 * same way, with the user-facing error toast only appearing after the last
 * one (~7s later). Fixed by excluding every 4xx for mutations — a
 * client/validation error can never succeed by resending the exact same
 * request, and mutations are frequently non-idempotent (POST create), so
 * blindly retrying also risks duplicate side effects on top of the wasted
 * round trips.
 *
 * Regression follow-up (code review on that same fix): the blanket 4xx
 * exclusion over-corrected — it also silently discarded 408/429, which this
 * backend explicitly documents as retryable congestion (see
 * `RETRYABLE_MUTATION_4XX_STATUSES`'s doc comment for the specific,
 * CHECKED — not assumed — evidence). Those two are carved back out.
 *
 * Preserved deliberately: 5xx (the server may recover) and network failures
 * (status 0 — the request never reached the server) are still retried up to
 * 3 times, same as queries. That distinction is the point of this predicate
 * existing separately from `shouldRetryQuery` — do not collapse them back
 * into one shared function.
 */
export function shouldRetryMutation(failureCount: number, err: unknown): boolean {
  if (err instanceof ApiSchemaError) return false
  if (
    err instanceof ApiError &&
    err.status >= 400 &&
    err.status < 500 &&
    !RETRYABLE_MUTATION_4XX_STATUSES.has(err.status)
  ) {
    return false
  }
  return failureCount < 3
}

/**
 * Retry delay for MUTATIONS. Honours a server `Retry-After` header when the
 * failing error carries one (`ApiError.retryAfterMs`, populated by
 * `ApiError.fromResponse` — e.g. the cli-validate in-flight-cap 429 sets
 * `Retry-After: 1`): the server is stating exactly how long its
 * congestion/rate-limit condition is expected to last, so honouring it is
 * strictly better than guessing via a fixed curve — waiting less risks
 * hitting the same cap again, waiting more is just slower than necessary.
 * Falls back to the same exponential backoff curve as `shouldRetryQuery`'s
 * paired `retryDelay` (queries, above) for every other retried error (5xx,
 * network failures, and a 408/429 with no Retry-After header).
 */
export function mutationRetryDelay(attempt: number, err: unknown): number {
  if (err instanceof ApiError && typeof err.retryAfterMs === 'number' && err.retryAfterMs > 0) {
    return err.retryAfterMs
  }
  return Math.min(1000 * 2 ** attempt, 30_000)
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
      retryDelay: mutationRetryDelay,
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
    void handleAuthError(event.action.error)
  }
})

queryClient.getMutationCache().subscribe((event) => {
  if (event.type === 'updated' && event.mutation.state.status === 'error') {
    _handleApiSchemaError(event.mutation.state.error)
    void handleAuthError(event.mutation.state.error)
  }
})
