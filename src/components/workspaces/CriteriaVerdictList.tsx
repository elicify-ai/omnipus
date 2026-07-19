import { useState } from 'react'
import { Check, X, CircleDashed, CaretDown, CaretRight } from '@phosphor-icons/react'
import type { AcceptanceCriterion, JudgeVerdict, EvidenceRecord } from '@/lib/api'
import { EvidenceViewer } from './EvidenceViewer'

export const DEFAULT_TASK_MAX_ATTEMPTS = 3

interface CriteriaVerdictListProps {
  criteria: AcceptanceCriterion[]
  /** All judge verdicts for this task (any `scope: 'task'` round) — the latest round's `per_criterion` reasons are shown. */
  verdicts?: JudgeVerdict[]
  /** Evidence records for this task — the latest attempt's record per criterion is shown, expandable. */
  evidence?: EvidenceRecord[]
  /** `Task.attempt_count` (contract C17) — the current run's attempt index. */
  attemptCount?: number
  /** `Task.max_attempts`, or the inherited PlanningConfig default (3) when absent. */
  maxAttempts?: number | null
}

/**
 * Per-attempt acceptance-criteria verdict list (ADR-049 FR-088, US-11 AS-3/4,
 * SD-C12). Shows each criterion's met/unmet/pending status + the judge's
 * latest reason, an "attempt N/M" counter, and an expandable evidence viewer
 * per criterion when a machine-check recorded evidence.
 */
export function CriteriaVerdictList({
  criteria,
  verdicts = [],
  evidence = [],
  attemptCount,
  maxAttempts,
}: CriteriaVerdictListProps) {
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const effectiveMax = maxAttempts ?? DEFAULT_TASK_MAX_ATTEMPTS

  const latestVerdict = verdicts
    .filter((v) => v.scope === 'task')
    .reduce<JudgeVerdict | null>((latest, v) => (!latest || v.round > latest.round ? v : latest), null)

  function reasonFor(criterionId: string | undefined): string | undefined {
    if (!criterionId || !latestVerdict) return undefined
    return latestVerdict.per_criterion.find((cv) => cv.criterion_id === criterionId)?.reason
  }

  function latestEvidenceFor(criterionId: string | undefined): EvidenceRecord | undefined {
    if (!criterionId) return undefined
    return evidence
      .filter((e) => e.criterion_id === criterionId)
      .sort((a, b) => b.attempt - a.attempt)[0]
  }

  if (criteria.length === 0) return null

  return (
    <div className="flex flex-col gap-1.5">
      {typeof attemptCount === 'number' && (
        <p className="text-xs text-[var(--color-muted)]" data-testid="attempt-counter">
          attempt {attemptCount}/{effectiveMax}
        </p>
      )}
      <ul className="space-y-1.5">
        {criteria.map((c) => {
          const reason = reasonFor(c.id)
          const ev = latestEvidenceFor(c.id)
          const isExpanded = expandedId === c.id
          return (
            <li key={c.id ?? c.text} className="rounded-md bg-[var(--color-surface-2)] text-xs p-2">
              <div className="flex items-start gap-2">
                <CriterionStatusIcon status={c.status} />
                <div className="flex-1 min-w-0">
                  <p className="text-[var(--color-secondary)]">{c.text}</p>
                  {reason && (
                    <p className="text-[10px] text-[var(--color-muted)] whitespace-pre-wrap mt-0.5">{reason}</p>
                  )}
                </div>
                {ev && (
                  <button tabIndex={0}
                    type="button"
                    onClick={() => setExpandedId(isExpanded ? null : (c.id ?? null))}
                    aria-expanded={isExpanded}
                    aria-label={`${isExpanded ? 'Collapse' : 'Expand'} evidence for ${c.text}`}
                    className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                  >
                    {isExpanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
                  </button>
                )}
              </div>
              {isExpanded && ev && (
                <div className="mt-2">
                  <EvidenceViewer evidence={ev} />
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

function CriterionStatusIcon({ status }: { status: AcceptanceCriterion['status'] }) {
  if (status === 'met') {
    return <Check size={13} weight="bold" className="shrink-0 mt-0.5 text-[color:var(--color-success)]" />
  }
  if (status === 'unmet') {
    return <X size={13} weight="bold" className="shrink-0 mt-0.5 text-[color:var(--color-error)]" />
  }
  return <CircleDashed size={13} className="shrink-0 mt-0.5 text-[var(--color-muted)]" />
}
