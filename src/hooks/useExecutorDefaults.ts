// useExecutorDefaults — shared "fetch the read-only auto-applied CLI flags
// reference list" state, for the Agent System P0 ghost-text fix (see
// AutoAppliedFlags.tsx for the presentational side).
//
// `GET /agents/executor-defaults` is static reference data — one entry per
// supported CLI (claude-code / codex / opencode), not filterable
// server-side — so this hook fetches the whole list ONCE per mount and
// callers select the entry matching the currently-chosen CLI client-side
// (`selectExecutorDefaults`). This mirrors `useCliDetect`'s fetch-once +
// unmount-cancel + fail-soft shape (Simplify F1 precedent): both the create
// wizard (Step1Identity → ExecutorInputs, always enabled) and the edit form
// (AgentProfile) need the same call, so it lives here rather than being
// duplicated per component.

import { useCallback, useEffect, useState } from 'react'
import { fetchExecutorDefaults } from '@/lib/api'
import type { CliValidateRequest, ExecutorDefaults } from '@/lib/api'

export type ExecutorDefaultsListState =
  | { status: 'idle'; data: null }
  | { status: 'loading'; data: null }
  | { status: 'success'; data: ExecutorDefaults[] }
  | { status: 'error'; data: null }

export interface ExecutorDefaultsResult {
  status: ExecutorDefaultsListState['status']
  data: ExecutorDefaults[] | null
  /** Re-triggers the fetch without waiting for `enabled` to toggle — the
   *  retry affordance on `AutoAppliedFlags`' degraded (error) state calls
   *  this directly. */
  retry: () => void
}

/**
 * Fetches the full auto-applied-flags reference list once per `enabled`
 * transition to `true` (or on-demand via `retry`). A network/auth error
 * resolves to `{ status: 'error' }` — callers MUST treat this as
 * non-blocking (never block the rest of the form) but MUST NOT silently
 * omit all UI either: `AutoAppliedFlags` renders a minimal degraded state
 * with a retry affordance rather than disappearing. The error is also
 * logged via `console.warn` so a real regression isn't silent, matching
 * `useCliDetect`'s pattern.
 */
export function useExecutorDefaults(enabled = true): ExecutorDefaultsResult {
  const [state, setState] = useState<ExecutorDefaultsListState>({ status: 'idle', data: null })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    setState({ status: 'loading', data: null })
    fetchExecutorDefaults()
      .then((d) => {
        if (!cancelled) setState({ status: 'success', data: d })
      })
      .catch((err) => {
        if (cancelled) return
        console.warn('Executor defaults fetch failed (auto-applied-flags block shows a retry affordance):', err)
        setState({ status: 'error', data: null })
      })
    return () => {
      cancelled = true
    }
    // `attempt` is intentionally in the dependency array purely to force a
    // re-run on retry — it carries no other meaning.
  }, [enabled, attempt])

  const retry = useCallback(() => setAttempt((n) => n + 1), [])

  return { ...state, retry }
}

/** Picks the entry matching `cli` out of a resolved defaults list, or
 *  `undefined` when the list hasn't resolved yet or no CLI is selected. */
export function selectExecutorDefaults(
  list: ExecutorDefaults[] | null,
  cli: CliValidateRequest['cli'] | undefined,
): ExecutorDefaults | undefined {
  if (!list || !cli) return undefined
  return list.find((entry) => entry.cli === cli)
}
