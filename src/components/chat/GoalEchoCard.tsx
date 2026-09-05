// GoalEchoCard — ADR-053 FE-8 / US-3 / design §1 (D11); criteria breakdown
// per ADR-074 D5.2 / judgment-first FR-011 (US-6).
//
// Renders the compiled-goal ECHO in the chat thread: when the engine compiles
// user intent into the goal definition + acceptance-criteria ladder (including
// literal machine-check commands), the agent echoes it back IN CHAT (no
// form/modal) and the user confirms by replying in normal chat. This card is
// that echo surface — it shows exactly what will be run, and prompts the user
// to reply to confirm or restate to amend.
//
// The criteria breakdown arrives on the goal_status frame's optional
// `criteria` field (present on the `queued` pending-confirm emission,
// ADR-074 D5.2). Rendering is plain-language-FIRST: each row leads with the
// criterion text; a technical payload (machine-check command verbatim, or a
// behavior count) renders as a quiet per-row "verifies via" chip. `[kind]`
// classification tokens are NOT user-facing content and never render.
//
// Purely presentational — driven by props. Literal commands are shown
// verbatim so the user can vet them before confirming — they run under the
// goal-bearing agent's own tool policy, never a bypass.

import { Target, ArrowBendUpRight } from '@phosphor-icons/react'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

type GoalCriterion = NonNullable<GoalStatusFrame['criteria']>[number]

export interface GoalEchoCardProps {
  /** The goal_status frame describing the compiled goal (condition + accounting + criteria breakdown). */
  frame: GoalStatusFrame
}

/**
 * Renders one criterion's technical payload as the "verifies via" chip text,
 * or null when the criterion is plain prose (no chip — the judge reads it).
 * Commands are verbatim (FR-113 substance), never paraphrased.
 */
function verifiesVia(c: GoalCriterion): string | null {
  if (c.check) {
    return `${c.check.command} (expected exit ${c.check.expected_exit_code})`
  }
  if (c.behavior) {
    const min = c.behavior.min_count ?? 1
    if (typeof c.behavior.max_count === 'number') {
      return `${c.behavior.tool} called ${min}–${c.behavior.max_count} times`
    }
    return `${c.behavior.tool} called at least ${min} time${min === 1 ? '' : 's'}`
  }
  return null
}

export function GoalEchoCard({ frame }: GoalEchoCardProps) {
  const criteria = frame.criteria ?? []
  return (
    <div
      data-testid="goal-echo-card"
      className="my-2 rounded-lg border border-[var(--color-accent)]/30 bg-[var(--color-surface-1)] px-4 py-3 text-xs"
    >
      {/* Header — compiled-goal banner */}
      <div className="flex items-center gap-2 mb-2">
        <Target size={14} weight="fill" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
        <span className="font-medium text-[var(--color-secondary)] uppercase tracking-wide text-[10px]">
          Compiled goal — reply to confirm
        </span>
      </div>

      {/* Condition (the compiled goal definition) */}
      <p className="text-[var(--color-secondary)] break-words" data-testid="goal-echo-condition">
        {frame.condition}
      </p>

      {/* Round accounting */}
      <p className="text-[var(--color-muted)] mt-1.5 tabular-nums" data-testid="goal-echo-round">
        {frame.max_rounds} rounds · {frame.cap} concurrent loop{frame.cap === 1 ? '' : 's'}
      </p>

      {/* Criteria breakdown — plain language first, per-row verifies-via chip
          for technical payloads (ADR-074 D5.2 / FR-011). */}
      {criteria.length > 0 && (
        <div className="mt-2.5" data-testid="goal-echo-criteria">
          <div className="text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide">
            Done when
          </div>
          <ol className="space-y-1.5">
            {criteria.map((c, i) => {
              const chip = verifiesVia(c)
              return (
                <li key={c.id ?? i} className="flex items-start gap-2" data-testid="goal-echo-criterion">
                  <span className="text-[var(--color-muted)] tabular-nums shrink-0">{i + 1}.</span>
                  <div className="flex-1 min-w-0">
                    <span className="text-[var(--color-secondary)] break-words">{c.text}</span>
                    {chip && (
                      <span
                        className="block mt-0.5 text-[11px] text-[var(--color-muted)]"
                        data-testid="goal-echo-verifies-via"
                      >
                        verifies via{' '}
                        <code className="font-mono text-[var(--color-accent)] bg-[var(--color-surface-2)] rounded px-1.5 py-0.5 break-all">
                          {chip}
                        </code>
                      </span>
                    )}
                  </div>
                </li>
              )
            })}
          </ol>
        </div>
      )}

      {/* Confirmation prompt — conversational, no buttons */}
      <div className="mt-2.5 flex items-center gap-1.5 text-[var(--color-muted)] italic">
        <ArrowBendUpRight size={11} aria-hidden="true" />
        <span>Reply to confirm, or restate to amend.</span>
      </div>
    </div>
  )
}
