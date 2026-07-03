// useExecutorSmokeTest — imperative "send a real test message" action for a
// subagent_3p (external-CLI) worker's executor settings. Backs the
// "Send a test message" affordance added to `CommandPreview.tsx`.
//
// Deliberately NOT reactive like `useCommandPreview` (which auto-fires on
// every field-VALUE change, because a command-line preview is a free,
// stateless computation that never spawns anything). `POST
// /agents/executor-smoke-test` spends real model usage and holds a real
// subprocess open for up to ~30s (`rest_executor_smoketest.go`'s bounded
// timeout/turn cap), so it must NEVER fire on its own — only when the
// operator deliberately calls `run()` from a button click.
//
// Modeled on `useCliPathValidation`'s imperative `validate()` shape, but
// simpler: a single deliberate click doesn't need the >=400ms debounce a
// blur-driven field validator needs. Only an `AbortController` is used, so a
// rapid re-click (`run()` called again before the previous request settles)
// or an unmount cancels whatever request is still in flight rather than
// racing a stale response into a newer call's state — same cancel-in-flight
// semantics as `useCliPathValidation`/`useCommandPreview`, minus the timer.
//
// A domain-level failure (the CLI ran but the run didn't succeed —
// `ExecutorSmokeTestResponse.ok === false`) is NOT the `error` state here —
// it resolves to `result` same as a success, because the response is a real,
// valid, structured outcome (see `fetchExecutorSmokeTest`'s doc comment).
// `error` is reserved for a genuine transport/auth/rate-limit failure that
// prevented the run from producing any structured response at all.

import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchExecutorSmokeTest, isApiError } from '@/lib/api'
import type { ExecutorSmokeTestRequest, ExecutorSmokeTestResponse } from '@/lib/api'

export type ExecutorSmokeTestStatus =
  | { kind: 'idle' }
  | { kind: 'pending' }
  | { kind: 'result'; result: ExecutorSmokeTestResponse }
  | { kind: 'error'; message: string }

export interface UseExecutorSmokeTest {
  status: ExecutorSmokeTestStatus
  /** Fires the real smoke-test request for `req`. Cancels any in-flight
   *  request from a previous call first, so a rapid re-click can never race
   *  a stale response into the newer call's state. */
  run: (req: ExecutorSmokeTestRequest) => void
  /** Cancels any in-flight request and resets to idle. */
  cancel: () => void
}

/**
 * Imperative "send a real test message" action. Never fires on mount or on
 * any dependency change — only `run(req)` triggers a call, and only one
 * call is ever in flight at a time.
 */
export function useExecutorSmokeTest(): UseExecutorSmokeTest {
  const [status, setStatus] = useState<ExecutorSmokeTestStatus>({ kind: 'idle' })
  const abortRef = useRef<AbortController | null>(null)

  const cancelPending = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
    }
  }, [])

  const cancel = useCallback(() => {
    cancelPending()
    setStatus({ kind: 'idle' })
  }, [cancelPending])

  const run = useCallback(
    (req: ExecutorSmokeTestRequest) => {
      cancelPending()
      setStatus({ kind: 'pending' })
      const controller = new AbortController()
      abortRef.current = controller
      fetchExecutorSmokeTest(req, { signal: controller.signal })
        .then((result) => {
          if (controller.signal.aborted) return
          setStatus({ kind: 'result', result })
        })
        .catch((err: unknown) => {
          // Network error, timeout, rate-limited (429), auth failure, or an
          // aborted request racing this handler. Never silently swallowed —
          // this is a deliberate, costly operator action, so the caller must
          // see why it didn't complete.
          if (controller.signal.aborted) return
          const message = isApiError(err)
            ? err.userMessage
            : err instanceof Error
              ? err.message
              : 'The test run failed unexpectedly.'
          setStatus({ kind: 'error', message })
        })
    },
    [cancelPending],
  )

  // Cancel any in-flight request on unmount — a real subprocess held open
  // server-side for up to ~30s must never resolve into a state update on an
  // unmounted component.
  useEffect(() => cancelPending, [cancelPending])

  return { status, run, cancel }
}
