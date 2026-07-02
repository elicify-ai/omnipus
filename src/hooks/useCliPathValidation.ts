// useCliPathValidation — shared validate-on-blur state machine for the
// external-executor CLI path field (external-executor-cli-path-detection
// spec, US-3/US-4/US-5, FR-006/FR-008/FR-013/FR-019).
//
// Used by both the create wizard (Step1Identity → ExecutorInputs) and the
// edit form (AgentProfile) so the debounce + in-flight-cancel + blocking
// semantics stay identical in both places.
//
// FR minor F-17: validate-on-blur is debounced >= 400ms and cancels any
// in-flight request before starting a new one.
// FR-019: before a result exists (idle/pending) or when the request cannot
// complete (network error / rate-limited / timeout), callers MUST treat the
// state as non-blocking ("not verified yet" / "couldn't verify") — never a
// permanent trap. See `cliValidationBlocked()` below, which only returns
// true for a definitive `result` with reason missing-binary/handshake-failed.

import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchCliValidate } from '@/lib/api'
import type { CliValidateResponse } from '@/lib/api'
import type { SupportedCli } from '@/lib/cliDetect'

const DEBOUNCE_MS = 400

export type CliValidationState =
  | { kind: 'idle' }
  | { kind: 'pending' }
  | { kind: 'result'; result: CliValidateResponse }
  | { kind: 'error' }

export interface UseCliPathValidation {
  status: CliValidationState
  /** Debounced (>=400ms), cancels any in-flight/pending validate call before
   *  starting a new one. A blank path resets to idle without a network call. */
  validate: (cli: SupportedCli, cliPath: string) => void
  /** Cancels any pending/in-flight validate and resets to idle. */
  reset: () => void
}

export function useCliPathValidation(): UseCliPathValidation {
  const [status, setStatus] = useState<CliValidationState>({ kind: 'idle' })
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const cancelPending = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current)
      timerRef.current = null
    }
    if (abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
    }
  }, [])

  const reset = useCallback(() => {
    cancelPending()
    setStatus({ kind: 'idle' })
  }, [cancelPending])

  const validate = useCallback(
    (cli: SupportedCli, cliPath: string) => {
      cancelPending()
      const trimmed = cliPath.trim()
      if (!trimmed) {
        setStatus({ kind: 'idle' })
        return
      }
      setStatus({ kind: 'pending' })
      timerRef.current = setTimeout(() => {
        timerRef.current = null
        const controller = new AbortController()
        abortRef.current = controller
        fetchCliValidate(cli, trimmed, { signal: controller.signal })
          .then((result) => {
            if (controller.signal.aborted) return
            setStatus({ kind: 'result', result })
          })
          .catch(() => {
            // Network error, timeout, rate-limited (429), or an aborted
            // request racing this handler — FR-019 requires this to be
            // non-blocking, never a trap.
            if (controller.signal.aborted) return
            setStatus({ kind: 'error' })
          })
      }, DEBOUNCE_MS)
    },
    [cancelPending],
  )

  // Cancel any pending timer/in-flight request on unmount.
  useEffect(() => cancelPending, [cancelPending])

  return { status, validate, reset }
}

/** True only for a definitive missing-binary/handshake-failed result — the
 *  only two reasons that block Create/Save (FR-008/FR-018). Any other state
 *  (idle, pending, error, ok, unauthenticated, or an unrecognized/stale
 *  `reason` value) MUST allow save (FR-019 / F-17 stale-bundle safety). */
export function cliValidationBlocked(state: CliValidationState): boolean {
  return state.kind === 'result' && (state.result.reason === 'missing-binary' || state.result.reason === 'handshake-failed')
}
