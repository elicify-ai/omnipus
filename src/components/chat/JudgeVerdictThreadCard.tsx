// JudgeVerdictThreadCard — ADR-049 D2/D4/US-13/SD-C10.
//
// Inline thread rendering of a `Message.type === 'judge_verdict'` transcript
// entry, shown ONLY under verbose chat (`shouldRenderJudgeVerdictInThread`,
// toolVisibility.ts) — the default is panel-only (ActivityPanel's judge
// row), mirroring `shouldRenderSubagentSpan`'s hide-by-default/verbose-
// reveal rule for delegation cards. Deliberately a separate, small
// presentational component from ActivityPanel's inline judge row (some
// duplication accepted): the two surfaces render different data shapes
// (persisted `JudgeVerdict` here vs the live `JudgeVerdictFrame` there) and
// have different layout constraints (a centered thread card vs a slide-out
// panel row).

import { Check, X, Scales } from '@phosphor-icons/react'
import type { JudgeVerdict } from '@/lib/api'

export interface JudgeVerdictThreadCardProps {
  verdict: JudgeVerdict
}

export function JudgeVerdictThreadCard({ verdict }: JudgeVerdictThreadCardProps) {
  return (
    <div className="flex justify-center py-2" data-testid="judge-verdict-thread-card">
      <div className="w-full max-w-md rounded-lg border border-[var(--color-accent)]/30 bg-[var(--color-surface-1)] px-3 py-2.5 text-xs">
        <div className="flex items-center gap-1.5 mb-1.5">
          <Scales size={13} weight="fill" className="text-[var(--color-accent)]" aria-hidden="true" />
          <span className="font-medium text-[var(--color-secondary)]">
            Judge verdict — {verdict.scope} round {verdict.round}
          </span>
          <span
            className={
              verdict.met
                ? 'ml-auto text-[10px] font-semibold text-[color:var(--color-success)]'
                : 'ml-auto text-[10px] font-semibold text-[color:var(--color-error)]'
            }
          >
            {verdict.met ? 'met' : 'unmet'}
          </span>
        </div>
        <ul className="space-y-1">
          {verdict.per_criterion.map((cv) => (
            <li key={cv.criterion_id} className="flex items-start gap-1.5">
              {cv.met ? (
                <Check size={11} weight="bold" className="shrink-0 mt-0.5 text-[color:var(--color-success)]" aria-hidden="true" />
              ) : (
                <X size={11} weight="bold" className="shrink-0 mt-0.5 text-[color:var(--color-error)]" aria-hidden="true" />
              )}
              <div className="min-w-0">
                <p className="font-mono text-[var(--color-secondary)] truncate">{cv.criterion_id}</p>
                {cv.reason && <p className="text-[var(--color-muted)] whitespace-pre-wrap mt-0.5">{cv.reason}</p>}
              </div>
            </li>
          ))}
        </ul>
        <p className="text-[10px] text-[var(--color-muted)] mt-1.5">
          {verdict.model} · judged by {verdict.judge_agent_id}
        </p>
      </div>
    </div>
  )
}
