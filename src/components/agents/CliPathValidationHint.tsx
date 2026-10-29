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
      <p data-testid={testId} data-cli-status="pending" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        <Spinner size={11} className="animate-spin" />
        Checking…
      </p>
    )
  }

  if (validation.kind === 'error') {
    return (
      <p data-testid={testId} data-cli-status="error" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
        <WarningCircle size={11} weight="fill" />
        Couldn't verify — you can still save. Will retry on the next edit.
      </p>
    )
  }

  if (validation.kind === 'forbidden') {
    // T11 slice A: the server refused the check (403). Not retryable by
    // editing; saving stays possible (non-blocking, FR-019).
    return (
      <p data-testid={testId} data-cli-status="forbidden" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
        <WarningCircle size={11} weight="fill" />
        CLI check unavailable: it runs only for a signed-in person, not in development-bypass mode or with the command-line token ({validation.message}). You can still save.
      </p>
    )
  }

  if (validation.kind === 'result') {
    const { reason, version } = validation.result
    switch (reason) {
      case 'ok':
        return (
          <p data-testid={testId} data-cli-status="ok" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-success)]">
            <CheckCircle size={11} weight="fill" />
            CLI runs{version ? ` · v${version}` : ''}
          </p>
        )
      case 'unauthenticated':
        return (
          <p data-testid={testId} data-cli-status="unauthenticated" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
            <WarningCircle size={11} weight="fill" />
            Installed — not logged in. You can still save.
          </p>
        )
      case 'missing-binary':
        return (
          <p data-testid={testId} data-cli-status="missing-binary" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)]">
            <XCircle size={11} weight="fill" />
            No CLI found at this path.
          </p>
        )
      case 'handshake-failed':
        return (
          <p data-testid={testId} data-cli-status="handshake-failed" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)]">
            <XCircle size={11} weight="fill" />
            Did not run, or returned no version.
          </p>
        )
      case 'path-not-allowed':
        // T11 spec §3.2 / C-A2: the server's program-file check refused this
        // path before anything ran. Its detail names the failed check, so it
        // is shown as-is.
        return (
          <p data-testid={testId} data-cli-status="path-not-allowed" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)]">
            <XCircle size={11} weight="fill" />
            {validation.result.detail || 'This program path is not allowed.'}
          </p>
        )
      default:
        // unknown-cli, or any future/stale reason value the SPA bundle
        // doesn't recognize (F-17 stale-bundle safety) — non-blocking.
        return (
          <p data-testid={testId} data-cli-status="unknown" className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
            <WarningCircle size={11} weight="fill" />
            Couldn't verify — you can still save.
          </p>
        )
    }
  }

  // idle — fall back to the static detection hint (US-1 AC-1/AC-3).
  if (detectHint.kind === 'detected') {
    return (
      <p data-testid={testId} data-cli-status="detected" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        Detected ({detectHint.source === 'well-known' ? 'well-known' : 'PATH'})
      </p>
    )
  }
  if (detectHint.kind === 'not-found') {
    return (
      <p data-testid={testId} data-cli-status="not-found" className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
        Not found — enter manually.
      </p>
    )
  }
  return null
}
