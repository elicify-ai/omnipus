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
import { Card } from '@/components/ui/card'

export interface JudgeVerdictThreadCardProps {
  verdict: JudgeVerdict
}

export function JudgeVerdictThreadCard({ verdict }: JudgeVerdictThreadCardProps) {
  return (
    <div className="flex justify-center py-[var(--space-2)]" data-testid="judge-verdict-thread-card">
      <Card className="w-full max-w-md border-[var(--color-accent)]/30 px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)]">
        <div className="flex items-center gap-[var(--space-1)] mb-[var(--space-1)]">
          <Scales size={13} weight="fill" className="text-[var(--color-accent)]" aria-hidden="true" />
          <span className="font-medium text-[var(--color-secondary)]">
            Judge verdict — {verdict.scope} round {verdict.round}
          </span>
          <span
            className={
              verdict.met
                ? 'ml-auto text-[length:var(--type-caption-size)] font-semibold text-[color:var(--color-success)]'
                : 'ml-auto text-[length:var(--type-caption-size)] font-semibold text-[color:var(--color-error)]'
            }
          >
            {verdict.met ? 'met' : 'unmet'}
          </span>
        </div>
        <ul className="space-y-[var(--space-1)]">
          {verdict.per_criterion.map((cv) => (
            <li key={cv.criterion_id} className="flex items-start gap-[var(--space-1)]">
              {cv.met ? (
                <Check size={11} weight="bold" className="shrink-0 mt-[var(--space-0-5)] text-[color:var(--color-success)]" aria-hidden="true" />
              ) : (
                <X size={11} weight="bold" className="shrink-0 mt-[var(--space-0-5)] text-[color:var(--color-error)]" aria-hidden="true" />
              )}
              <div className="min-w-0">
                <p className="font-mono text-[var(--color-secondary)] truncate">{cv.criterion_id}</p>
                {cv.reason && <p className="text-[var(--color-muted)] whitespace-pre-wrap mt-[var(--space-0-5)]">{cv.reason}</p>}
              </div>
            </li>
          ))}
        </ul>
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] mt-[var(--space-1)]">
          {verdict.model} · judged by {verdict.judge_agent_id}
        </p>
      </Card>
    </div>
  )
}
