// useCommandPreview — live, debounced preview of the REAL command line
// Omnipus would spawn for a subagent_3p external-CLI worker's CURRENT
// settings. Backs `CommandPreview.tsx`, the primary executor-transparency UI
// rendered in both the create wizard (Step1Identity → ExecutorInputs) and
// the edit form (AgentProfile) — replacing the generic, static
// `AutoAppliedFlags` reference list with the ACTUAL argv/command_line
// computed server-side for what the operator has typed, via
// `POST /agents/executor-preview`.
//
// Differs from `useCliPathValidation` in ONE deliberate way: that hook is
// imperative (`validate(cli, path)` called from onBlur) because a blur-only
// check just needs to happen once per "the operator moved on". A live
// command-line preview is far more useful when it visibly updates as the
// operator types, not just after they click away — so this hook is
// REACTIVE: callers pass the current `ExecutorCommandPreviewRequest` (built
// fresh from whatever state shape they own — `executor`/`model` in
// AgentProfile, the flat `WizardSubmitPayload` in the create wizard) on
// every render, and the hook itself detects when the field VALUES that
// matter have actually changed (via a derived string key, so a fresh object
// literal every render does not by itself trigger a re-fetch) and debounces
// >=400ms before calling `fetchExecutorPreview`. Any pending debounce or
// in-flight request from a previous burst is cancelled first — via
// `clearTimeout` / `AbortController.abort()`, matching
// `useCliPathValidation`'s cancel-in-flight semantics exactly.
//
// `req` is `undefined` (or has no `cli`) before a CLI has been chosen —
// `ExecutorCommandPreviewRequest.cli` is required on the wire, so that
// resets to idle without a network call, same as `useCliPathValidation`'s
// blank-path short-circuit.

import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchExecutorPreview, isApiError } from '@/lib/api'
import type { ExecutorCommandPreviewRequest, ExecutorCommandPreviewResponse } from '@/lib/api'

const DEBOUNCE_MS = 400

export type CommandPreviewState =
  | { kind: 'idle' }
  | { kind: 'pending' }
  | { kind: 'result'; result: ExecutorCommandPreviewResponse }
  /**
   * `message` is the real, human-displayable reason the fetch failed
   * (`ApiError.userMessage`, or the plain `Error.message` for a non-ApiError
   * rejection — same fallback chain `useExecutorSmokeTest.ts` uses).
   * `isValidationError` is true only for a genuine 4xx client error (the
   * request body itself was rejected — e.g. a 400 from `decodeAndValidate`
   * tripping one of `ExecutorCommandPreviewRequest`'s `maxLength`s) and
   * false for anything the operator can't fix by editing this field —
   * network failure, timeout, 429, or a 5xx. `CommandPreview.tsx` branches
   * its copy on this: a validation error must NOT claim "the settings below
   * still apply when this agent runs", since a value THIS endpoint rejects
   * as schema-invalid is likely to misbehave at real save/dispatch time too
   * (same shared schema family) — see the finding this fixes.
   */
  | { kind: 'error'; message: string; isValidationError: boolean }

export interface UseCommandPreview {
  status: CommandPreviewState
  /** Re-issues the preview request for the CURRENT `req` without waiting for
   *  a field to change again — the error state's Retry affordance calls this
   *  directly (mirrors `useExecutorDefaults`' retry). */
  retry: () => void
}

/**
 * Fetches a live preview of the real command Omnipus would spawn for the
 * given `req` (cli/model/cli_path/cli_args/max_tool_iterations), debounced
 * >=400ms and re-fetching automatically whenever any of those field values
 * change. Pass `undefined` (or a `req` with no `cli`) when no CLI is
 * selected yet — resolves to `{ kind: 'idle' }` without a network call.
 */
export function useCommandPreview(req: ExecutorCommandPreviewRequest | undefined): UseCommandPreview {
  const [status, setStatus] = useState<CommandPreviewState>({ kind: 'idle' })
  const [attempt, setAttempt] = useState(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  // Derived key: only the field VALUES that affect the previewed command
  // line, normalized so `undefined`/empty-string/missing all collapse to the
  // same key. This is what the effect keys off — a caller re-rendering with
  // a brand-new `req` OBJECT (e.g. from a `useMemo`-less inline literal)
  // must NOT itself trigger a re-fetch; only an actual value change should.
  const key = req?.cli
    ? JSON.stringify({
        cli: req.cli,
        model: req.model ?? '',
        cli_path: req.cli_path ?? '',
        cli_args: req.cli_args ?? '',
        max_tool_iterations: req.max_tool_iterations ?? 0,
      })
    : null

  useEffect(() => {
    if (!req || !req.cli) {
      setStatus({ kind: 'idle' })
      return
    }

    setStatus({ kind: 'pending' })
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      const controller = new AbortController()
      abortRef.current = controller
      fetchExecutorPreview(req, { signal: controller.signal })
        .then((result) => {
          if (controller.signal.aborted) return
          setStatus({ kind: 'result', result })
        })
        .catch((err: unknown) => {
          // Network error, timeout, rate-limited (429), a genuine 4xx
          // validation rejection, or an aborted request racing this
          // handler. Never blocks Create/Save — the preview is purely
          // informational — but the real cause must never be invisible,
          // even to an engineer debugging a report, so it's logged here
          // regardless of whether the UI ends up rendering it.
          if (controller.signal.aborted) return
          console.error('[useCommandPreview] preview fetch failed', err)
          const message = isApiError(err)
            ? err.userMessage
            : err instanceof Error
              ? err.message
              : 'Could not compute a live command preview.'
          // Any 4xx other than 429 (rate-limited, a transport-shaped
          // condition the operator can't fix by editing the field) means the
          // server itself rejected this request body as invalid.
          const isValidationError = isApiError(err) && err.status >= 400 && err.status < 500 && err.status !== 429
          setStatus({ kind: 'error', message, isValidationError })
        })
    }, DEBOUNCE_MS)

    // Cancels a still-pending debounce timer OR aborts an in-flight request
    // from THIS burst — runs before the next effect invocation (key/attempt
    // changed) and on unmount, so a stale burst can never resolve into a
    // newer one's state.
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
    }
    // `req` itself is intentionally excluded — `key` is its full dependency
    // surface, and `attempt` exists purely to force a re-run on retry().
     
  }, [key, attempt])

  const retry = useCallback(() => setAttempt((n) => n + 1), [])

  return { status, retry }
}

/**
 * buildExecutorPreviewRequest builds an `ExecutorCommandPreviewRequest` from
 * the loose CLI/model/cli_path/cli_args values each caller owns in its own
 * state shape — `AgentProfile.tsx` (edit form: `executor`/top-level `model`)
 * and `Step1Identity.tsx`'s `ExecutorInputs` (create wizard: the flat
 * `WizardSubmitPayload`). Both previously duplicated identical
 * trim-or-undefined logic for `model` inline; extracted here so each call
 * site is a one-liner and the two can never silently drift apart on how a
 * blank model is represented on the wire (empty string vs `undefined` change
 * which preview case the backend computes — see the schema's doc comment on
 * `model`).
 */
export function buildExecutorPreviewRequest(
  cli: ExecutorCommandPreviewRequest['cli'],
  model: string,
  cliPath: string | undefined,
  cliArgs: string | undefined,
): ExecutorCommandPreviewRequest {
  return {
    cli,
    model: model.trim() !== '' ? model.trim() : undefined,
    cli_path: cliPath,
    cli_args: cliArgs,
  }
}
