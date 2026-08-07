import { cn } from '@/lib/utils'
import type { EvidenceRecord } from '@/lib/api'

interface EvidenceViewerProps {
  evidence: EvidenceRecord
}

/**
 * Renders a single machine-check `EvidenceRecord` (ADR-049 D2, US-11 AS-4).
 * `command`/`output` arrive ALREADY redacted (backend redacts before
 * persistence, ADR-004 `RegisterSensitiveValues`) — this component never
 * re-redacts or un-redacts, it only renders what it's given, so a raw secret
 * is never in scope to leak. `truncated`/`timed_out`/`policy_denied` are
 * rendered as explicit markers rather than relying solely on the marker text
 * the backend may already have appended to `output`, so the signal is never
 * silently dropped from the UI.
 */
export function EvidenceViewer({ evidence }: EvidenceViewerProps) {
  const exitCodeUnavailable = evidence.timed_out || evidence.policy_denied
  const passed = !exitCodeUnavailable && evidence.exit_code === 0

  return (
    <div className="rounded-md bg-[var(--color-surface-1)] border border-[var(--color-border)] p-2 text-[10px] font-mono" data-testid="evidence-viewer">
      <p className="text-[var(--color-muted)] mb-1 truncate" title={evidence.command}>
        $ {evidence.command}
      </p>
      <pre className="whitespace-pre-wrap break-words text-[var(--color-secondary)] max-h-[160px] overflow-y-auto">
        {evidence.output}
      </pre>
      <div className="flex items-center gap-1.5 mt-1.5 flex-wrap">
        <span
          className={cn(
            'rounded px-1.5 py-0.5',
            passed
              ? 'bg-[var(--color-success)]/10 text-[color:var(--color-success)]'
              : 'bg-[var(--color-error)]/10 text-[color:var(--color-error)]',
          )}
        >
          exit {exitCodeUnavailable ? '—' : evidence.exit_code}
        </span>
        {evidence.timed_out && (
          <span className="rounded px-1.5 py-0.5 bg-[var(--color-error)]/10 text-[color:var(--color-error)]" data-testid="evidence-timed-out">
            timed out
          </span>
        )}
        {evidence.policy_denied && (
          <span className="rounded px-1.5 py-0.5 bg-[var(--color-error)]/10 text-[color:var(--color-error)]" data-testid="evidence-policy-denied">
            policy denied
          </span>
        )}
        {evidence.truncated && (
          <span className="rounded px-1.5 py-0.5 bg-[var(--color-warning)]/10 text-[color:var(--color-warning)]" data-testid="evidence-truncated">
            truncated
          </span>
        )}
      </div>
    </div>
  )
}
