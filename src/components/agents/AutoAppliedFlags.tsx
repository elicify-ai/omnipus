// AutoAppliedFlags — read-only "Automatically applied by Omnipus" info block
// rendered above the (now-relabeled) "Additional CLI arguments" field in both
// the create wizard (Step1Identity → ExecutorInputs) and the edit form
// (AgentProfile). Agent System P0 fix: the CLI-arguments field previously
// showed the driver's real auto-applied flags only as HTML `placeholder`
// ghost-text (e.g. "--verbose --output json") that vanished on focus and
// never matched what Omnipus actually sent — this component shows the REAL
// per-CLI flags as plain, non-editable text so operators are never misled.
//
// Deliberately interactive-inert (no input/checkbox/button touching the
// list) — several of these flags are safety-locked server-side
// (`pkg/agent/runner/argsafety.go::filterDangerousCLIArgs` silently strips a
// conflicting operator override), so presenting them as editable here would
// be actively misleading.

import { useExecutorDefaults, selectExecutorDefaults } from '@/hooks/useExecutorDefaults'
import type { CliValidateRequest } from '@/lib/api'

export interface AutoAppliedFlagsProps {
  /** The currently-selected executor CLI. `undefined` renders nothing (no
   *  CLI chosen yet). */
  cli: CliValidateRequest['cli'] | undefined
  testId?: string
}

export function AutoAppliedFlags({ cli, testId }: AutoAppliedFlagsProps) {
  const { status, data } = useExecutorDefaults(cli !== undefined)

  if (!cli) return null

  if (status === 'idle' || status === 'loading') {
    return (
      <div
        data-testid={testId}
        data-defaults-status="loading"
        className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
        aria-busy="true"
      >
        <div className="h-3 w-48 animate-pulse rounded bg-[var(--color-surface-1)]" />
      </div>
    )
  }

  // Fail-soft (per useExecutorDefaults' contract): a fetch error omits the
  // block entirely rather than blocking the rest of the form. The hook
  // already console.warn'd the underlying error.
  if (status === 'error' || !data) {
    return null
  }

  const entry = selectExecutorDefaults(data, cli)
  if (!entry) return null

  return (
    <div
      data-testid={testId}
      data-defaults-status="ready"
      role="group"
      aria-label="Flags automatically applied by Omnipus"
      className="space-y-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2.5"
    >
      <p className="text-[11px] font-medium text-[var(--color-secondary)]">
        Automatically applied by Omnipus
      </p>
      {entry.auto_applied_flags.length === 0 ? (
        <p className="text-[11px] text-[var(--color-muted)]">
          No flags are auto-applied for {cli}.
        </p>
      ) : (
        <div className="flex flex-wrap gap-1.5" data-testid={testId ? `${testId}-list` : undefined}>
          {entry.auto_applied_flags.map((flag) => (
            <code
              key={flag}
              className="rounded border border-[var(--color-border)] bg-[var(--color-surface-1)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--color-secondary)]"
            >
              {flag}
            </code>
          ))}
        </div>
      )}
      {entry.notes && (
        <p className="text-[11px] text-[var(--color-muted)] leading-snug">{entry.notes}</p>
      )}
      <p className="text-[10px] text-[var(--color-muted)]/70">
        Read-only — enforced server-side and cannot be overridden below.
      </p>
    </div>
  )
}

export default AutoAppliedFlags
