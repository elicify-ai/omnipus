import { useState } from 'react'
import { Check, X, CircleDashed, CaretDown, CaretRight } from '@phosphor-icons/react'
import type { AcceptanceCriterion, JudgeVerdict, EvidenceRecord } from '@/lib/api'
import { EvidenceViewer } from './EvidenceViewer'

export const DEFAULT_TASK_MAX_ATTEMPTS = 3

interface CriteriaVerdictListProps {
  criteria: AcceptanceCriterion[]
  /**
   * A goal's Definition of Done (ADR-080 D-DOD), DISTINCT from `criteria` —
   * generic standing quality gates vs. outcome-specific checks. Only a
   * goal-scoped caller passes this (a Task/Plan has no separate `dod` array
   * of its own — its `criteria` already serves as its DoD); when present,
   * renders as its own "Definition of Done" group below the criteria, since
   * the Judge scores `criteria ∪ dod` together and a verdict can land on
   * either array.
   */
  dod?: AcceptanceCriterion[]
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
 *
 * ADR-080 D-DOD: when a caller passes `dod` (a goal's Definition of Done),
 * those items render as a second, distinctly-labeled group ("Definition of
 * Done") below the criteria — same per-item rendering (status icon, judge
 * reason, evidence), because the Judge evaluates `criteria ∪ dod` as one
 * judged set and a DoD item gets its own per-criterion verdict exactly like
 * an acceptance criterion.
 */
export function CriteriaVerdictList({
  criteria,
  dod = [],
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

  function verdictFor(
    criterionId: string | undefined,
  ): JudgeVerdict['per_criterion'][number] | undefined {
    // Explicit id-absence handling (ADR-074 D5.3): verdict entries key on
    // `criterion_id` while `AcceptanceCriterion.id` is optional pre-persist.
    // A criterion without a server-set id matches NO verdict entry — return
    // undefined outright (no reason/quote lines render) instead of falling
    // through to a blank-reason lookup. The server always sets ids on
    // persist, so this only fires on unpersisted drafts.
    if (!criterionId || !latestVerdict) return undefined
    return latestVerdict.per_criterion.find((cv) => cv.criterion_id === criterionId)
  }

  function latestEvidenceFor(criterionId: string | undefined): EvidenceRecord | undefined {
    if (!criterionId) return undefined
    return evidence
      .filter((e) => e.criterion_id === criterionId)
      .sort((a, b) => b.attempt - a.attempt)[0]
  }

  if (criteria.length === 0 && dod.length === 0) return null

  return (
    <div className="flex flex-col gap-1.5">
      {typeof attemptCount === 'number' && (
        <p className="text-xs text-[var(--color-muted)]" data-testid="attempt-counter">
          attempt {attemptCount}/{effectiveMax}
        </p>
      )}
      {criteria.length > 0 && (
        <ul className="space-y-1.5">
          {criteria.map((c) => (
            <CriterionRow
              key={c.id ?? c.text}
              criterion={c}
              verdict={verdictFor(c.id)}
              evidenceRecord={latestEvidenceFor(c.id)}
              isExpanded={expandedId === c.id}
              onToggleExpand={() => setExpandedId(expandedId === c.id ? null : (c.id ?? null))}
            />
          ))}
        </ul>
      )}
      {dod.length > 0 && (
        <div className="mt-1" data-testid="criteria-verdict-dod">
          <p className="text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide">
            Definition of Done
          </p>
          <ul className="space-y-1.5">
            {dod.map((c) => (
              <CriterionRow
                key={c.id ?? c.text}
                criterion={c}
                verdict={verdictFor(c.id)}
                evidenceRecord={latestEvidenceFor(c.id)}
                isExpanded={expandedId === c.id}
                onToggleExpand={() => setExpandedId(expandedId === c.id ? null : (c.id ?? null))}
              />
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

interface CriterionRowProps {
  criterion: AcceptanceCriterion
  verdict: JudgeVerdict['per_criterion'][number] | undefined
  evidenceRecord: EvidenceRecord | undefined
  isExpanded: boolean
  onToggleExpand: () => void
}

function CriterionRow({ criterion: c, verdict, evidenceRecord: ev, isExpanded, onToggleExpand }: CriterionRowProps) {
  return (
    <li className="rounded-md bg-[var(--color-surface-2)] text-xs p-2">
      <div className="flex items-start gap-2">
        <CriterionStatusIcon status={c.status} />
        <div className="flex-1 min-w-0">
          <p className="text-[var(--color-secondary)]">{c.text}</p>
          {verdict?.reason && (
            // ADR-074 D5.3: the judge's reason IS the verdict
            // statement — criterion-text size, no longer 10px muted.
            <p
              className="text-[var(--color-muted)] whitespace-pre-wrap mt-0.5"
              data-testid="verdict-reason"
            >
              {verdict.reason}
            </p>
          )}
          {verdict?.evidence_quote && (
            // ADR-074 D7: the grounding quote, rendered as INERT
            // quoted text (plain text node — never parsed as
            // markdown/HTML: it is verbatim UNTRUSTED content).
            // Renders ONLY when non-empty — fail-closed, pre-D7 and
            // old-soul verdicts carry none and show no line.
            <p
              className="border-l-2 border-[var(--color-border)] pl-2 mt-1 text-[var(--color-muted)] italic whitespace-pre-wrap"
              data-testid="verdict-evidence-quote"
            >
              {verdict.evidence_quote}
            </p>
          )}
        </div>
        {ev && (
          <button tabIndex={0}
            type="button"
            onClick={onToggleExpand}
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
