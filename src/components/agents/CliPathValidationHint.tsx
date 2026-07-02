// CliPathValidationHint — inline status line rendered under the executor
// `cli_path` input in both the create wizard (Step1Identity) and the edit
// form (AgentProfile). Mirrors the tone/icon language already established by
// `ExecutorSelector.tsx`'s `RunnerTestResult` (CheckCircle/WarningCircle/
// XCircle, emerald/amber/error tones) so the two CLI-health surfaces read as
// one system.
//
// Priority: once a blur-triggered validate() has produced a state (pending /
// result / error), that takes over from the static detection hint (US-1's
// "Detected (PATH)" / "not found — enter manually" — shown only before the
// first validate() call, per the BDD "Prefill from a CLI found on PATH"
// scenario).

import { CheckCircle, Spinner, WarningCircle, XCircle } from '@phosphor-icons/react'
import type { CliValidationState } from '@/hooks/useCliPathValidation'
import type { CliDetectHint } from '@/lib/cliDetect'

interface CliPathValidationHintProps {
  validation: CliValidationState
  detectHint: CliDetectHint
  testId?: string
}

export function CliPathValidationHint({ validation, detectHint, testId }: CliPathValidationHintProps) {
  if (validation.kind === 'pending') {
    return (
      <p data-testid={testId} data-cli-status="pending" className="flex items-center gap-1 text-[11px] text-[var(--color-muted)]">
        <Spinner size={11} className="animate-spin" />
        Checking…
      </p>
    )
  }

  if (validation.kind === 'error') {
    return (
      <p data-testid={testId} data-cli-status="error" className="flex items-center gap-1 text-[11px] text-amber-400">
        <WarningCircle size={11} weight="fill" />
        Couldn't verify — you can still save. Will retry on the next edit.
      </p>
    )
  }

  if (validation.kind === 'result') {
    const { reason, version } = validation.result
    switch (reason) {
      case 'ok':
        return (
          <p data-testid={testId} data-cli-status="ok" className="flex items-center gap-1 text-[11px] text-emerald-400">
            <CheckCircle size={11} weight="fill" />
            CLI runs{version ? ` · v${version}` : ''}
          </p>
        )
      case 'unauthenticated':
        return (
          <p data-testid={testId} data-cli-status="unauthenticated" className="flex items-center gap-1 text-[11px] text-amber-400">
            <WarningCircle size={11} weight="fill" />
            Installed — not logged in. You can still save.
          </p>
        )
      case 'missing-binary':
        return (
          <p data-testid={testId} data-cli-status="missing-binary" className="flex items-center gap-1 text-[11px] text-[var(--color-error)]">
            <XCircle size={11} weight="fill" />
            No CLI found at this path.
          </p>
        )
      case 'handshake-failed':
        return (
          <p data-testid={testId} data-cli-status="handshake-failed" className="flex items-center gap-1 text-[11px] text-[var(--color-error)]">
            <XCircle size={11} weight="fill" />
            Did not run, or returned no version.
          </p>
        )
      default:
        // unknown-cli, or any future/stale reason value the SPA bundle
        // doesn't recognize (F-17 stale-bundle safety) — non-blocking.
        return (
          <p data-testid={testId} data-cli-status="unknown" className="flex items-center gap-1 text-[11px] text-amber-400">
            <WarningCircle size={11} weight="fill" />
            Couldn't verify — you can still save.
          </p>
        )
    }
  }

  // idle — fall back to the static detection hint (US-1 AC-1/AC-3).
  if (detectHint.kind === 'detected') {
    return (
      <p data-testid={testId} data-cli-status="detected" className="text-[11px] text-[var(--color-muted)]">
        Detected ({detectHint.source === 'well-known' ? 'well-known' : 'PATH'})
      </p>
    )
  }
  if (detectHint.kind === 'not-found') {
    return (
      <p data-testid={testId} data-cli-status="not-found" className="text-[11px] text-amber-400">
        Not found — enter manually.
      </p>
    )
  }
  return null
}
