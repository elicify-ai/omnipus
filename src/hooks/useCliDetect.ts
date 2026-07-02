// useCliDetect — shared "probe the host for installed external CLIs" state
// for the external-executor-cli-path-detection feature (ADR-030).
//
// Both the create wizard (Step1Identity → ExecutorInputs, always enabled)
// and the edit form (AgentProfile, enabled only when the agent being edited
// is a subagent_3p) need the same fetch-once + unmount-cancel + fail-soft
// detect() call, so it lives here rather than being duplicated per
// component (Simplify F1).

import { useEffect, useState } from 'react'
import { fetchCliDetect } from '@/lib/api'
import type { CliDetect } from '@/lib/api'

/**
 * Probes `/system/cli-detect` once per `enabled` transition to `true` and
 * returns the result, or `null` before it resolves (or when disabled).
 *
 * Detection is a convenience only — the caller uses the result to prefill
 * the cli_path field, never to gate anything. A network/auth error is
 * swallowed (fail-soft: no prefill, no crash — FR-019 spirit: detection is
 * never a blocker) but logged via `console.warn` so a real regression isn't
 * silent, matching the pattern already established in `AgentListScreen.tsx`.
 */
export function useCliDetect(enabled = true): CliDetect | null {
  const [cliDetect, setCliDetect] = useState<CliDetect | null>(null)

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    fetchCliDetect()
      .then((d) => {
        if (!cancelled) setCliDetect(d)
      })
      .catch((err) => {
        console.warn('CLI detection failed (prefill skipped):', err)
      })
    return () => {
      cancelled = true
    }
  }, [enabled])

  return cliDetect
}
