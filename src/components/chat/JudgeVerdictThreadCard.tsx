// JudgeVerdictThreadCard — ADR-049 D2/D4/US-13/SD-C10.
//
// Inline thread rendering of a `Message.type === 'judge_verdict'` transcript
// entry, shown ONLY under verbose chat (`shouldRenderJudgeVerdictInThread`,
// toolVisibility.ts) — the default is panel-only (ActivityPanel's judge
// row).
//
// toolui-analysis item 5 (founder-approved 2026-09-26): the card starts
// COLLAPSED to a one-line header — "Judge verdict · <scope> round N ·
// met/unmet" — with the criteria list and the model/judge footer inside the
// DisclosureRow accordion. Still a separate, small presentational component
// from ActivityPanel's inline judge row (some duplication accepted): the two
// surfaces render different data shapes (persisted `JudgeVerdict` here vs
// the live `JudgeVerdictFrame` there) and have different layout constraints
// (a centered thread card vs a slide-out panel row).

import { useState } from 'react'
import { Check, X, Scales } from '@phosphor-icons/react'
import type { JudgeVerdict } from '@/lib/api'
import { Card } from '@/components/ui/card'
import { DisclosureRow } from '@/components/ui/disclosure-row'

export interface JudgeVerdictThreadCardProps {
  verdict: JudgeVerdict
}

export function JudgeVerdictThreadCard({ verdict }: JudgeVerdictThreadCardProps) {
  // Collapsed by default (toolui-analysis item 5): the header line alone
  // carries "Judge verdict · <scope> round N · met/unmet"; the criteria list
  // and model/judge footer expand on demand. Verbose-only surface either way
  // (toolVisibility.ts) — this component's own visibility gate lives in
  // ChatScreen, unchanged.
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="flex justify-center py-[var(--space-2)]" data-testid="judge-verdict-thread-card">
      <Card className="w-full max-w-md border-[var(--color-accent)]/30 px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)]">
        {/* One-line header — the whole row is one DisclosureRow toggle (there
            is no sibling action on this card), caret inside the button,
            aria-expanded/keyboard toggling inherited from Button. The
            mid-dot + met/unmet sit at the row's right edge so the header
            reads "Judge verdict · task round 2 · unmet". */}
        <DisclosureRow
          expanded={expanded}
          onExpandedChange={setExpanded}
          expandable
          data-testid="judge-verdict-toggle"
        >
          <Scales size={13} weight="fill" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
          <span className="font-medium text-[var(--color-secondary)]">
            Judge verdict · {verdict.scope} round {verdict.round}
          </span>
          <span className="ml-auto flex shrink-0 items-center gap-[var(--space-1)]">
            <span className="text-[var(--color-muted)]">·</span>
            <span
              className={
                verdict.met
                  ? 'font-semibold text-[color:var(--color-success)]'
                  : 'font-semibold text-[color:var(--color-error)]'
              }
            >
              {verdict.met ? 'met' : 'unmet'}
            </span>
          </span>
        </DisclosureRow>

        {/* Accordion body — criteria list + model/judge footer, the content
            that used to sit directly under the header. */}
        {expanded && (
          <div className="mt-[var(--space-1)]">
            <ul className="space-y-[var(--space-1)]">
              {verdict.per_criterion.map((cv) => (
                <li key={cv.criterion_id} className="flex items-start gap-[var(--space-1)]">
                  {cv.met ? (
                    <Check size={11} weight="bold" className="mt-[var(--space-0-5)] shrink-0 text-[color:var(--color-success)]" aria-hidden="true" />
                  ) : (
                    <X size={11} weight="bold" className="mt-[var(--space-0-5)] shrink-0 text-[color:var(--color-error)]" aria-hidden="true" />
                  )}
                  <div className="min-w-0">
                    <p className="truncate font-mono text-[var(--color-secondary)]">{cv.criterion_id}</p>
                    {cv.reason && <p className="mt-[var(--space-0-5)] whitespace-pre-wrap text-[var(--color-muted)]">{cv.reason}</p>}
                  </div>
                </li>
              ))}
            </ul>
            <p className="mt-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
              {verdict.model} · judged by {verdict.judge_agent_id}
            </p>
          </div>
        )}
      </Card>
    </div>
  )
}
