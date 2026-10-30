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
import { isApiError } from '@/lib/api-error'
import type { SupportedCli } from '@/lib/cliDetect'

const DEBOUNCE_MS = 400

/** ErrorResponse code of the Check CLI person-gate 403
 *  (pkg/gateway/middleware.PersonSessionRequiredCode). */
const PERSON_SESSION_REQUIRED_CODE = 'person_session_required'

/** The server's own 403 message (ErrorResponse.error), falling back to the
 *  client's generic text when the body carries none. */
function forbiddenMessage(body: string | undefined, fallback: string): string {
  if (body) {
    try {
      const parsed = JSON.parse(body) as { error?: unknown }
      if (typeof parsed.error === 'string' && parsed.error.trim() !== '') return parsed.error
    } catch {
      // Not JSON: use the fallback below.
    }
  }
  return fallback
}

export type CliValidationState =
  | { kind: 'idle' }
  | { kind: 'pending' }
  | { kind: 'result'; result: CliValidateResponse }
  | { kind: 'error' }
  // T11 slice A: the server refused the check itself (HTTP 403). Editing the
  // path cannot fix this, so it is not the transient 'error' state.
  // personRequired is true only for the person-gate refusal (ErrorResponse
  // code "person_session_required"); any other 403 keeps its own message.
  // Non-blocking like 'error' (FR-019).
  | { kind: 'forbidden'; personRequired: boolean; message: string }

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
          .catch((err: unknown) => {
            // Network error, timeout, rate-limited (429), or an aborted
            // request racing this handler — FR-019 requires this to be
            // non-blocking, never a trap. A 403 is a lasting refusal of the
            // check itself and gets its own state so it is not presented as
            // "retry on the next edit".
            if (controller.signal.aborted) return
            if (isApiError(err) && err.status === 403) {
              setStatus({
                kind: 'forbidden',
                personRequired: err.code === PERSON_SESSION_REQUIRED_CODE,
                message: forbiddenMessage(err.body, err.userMessage),
              })
              return
            }
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

/** True only for the two definitive reasons that block Create/Save
 *  (FR-008/FR-018) — missing-binary and handshake-failed. Any other reason
 *  (ok, unauthenticated, unknown-cli, or a future/stale value the SPA
 *  bundle doesn't recognize) MUST allow save (FR-019 / F-17 stale-bundle
 *  safety). The single source of truth for this predicate — used by
 *  `cliValidationBlocked()` below (gates the edit-form autosave). */
export function isBlockingCliReason(reason: CliValidateResponse['reason'] | undefined): boolean {
  return reason === 'missing-binary' || reason === 'handshake-failed'
}

/** True only for a definitive missing-binary/handshake-failed result — the
 *  only two reasons that block Create/Save (FR-008/FR-018). Any other state
 *  (idle, pending, error, ok, unauthenticated, or an unrecognized/stale
 *  `reason` value) MUST allow save (FR-019 / F-17 stale-bundle safety). */
export function cliValidationBlocked(state: CliValidationState): boolean {
  return state.kind === 'result' && isBlockingCliReason(state.result.reason)
}
