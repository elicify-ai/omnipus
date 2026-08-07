// GoalAmendmentDiff — ADR-053 FE-8 / US-3 / design §1 (N-6).
//
// Renders a goal AMENDMENT DIFF in the chat thread: when the user re-states an
// active goal (`/goal <new intent>`), the engine diffs it against the current
// goal and surfaces the result IN CHAT (no form/modal) as added / changed /
// dropped criteria for the user to confirm or reject in their next message.
//
// Per the design (§1, N-6): "A later /goal <new intent> is not a silent
// recompile: it is diffed against the current goal and surfaced in chat as an
// amendment for the user to confirm or reject in their next message (added /
// changed / dropped criteria — no form), so the contract never shifts under the
// worker invisibly."
//
// Purely presentational — driven by a display-only diff shape. This is NOT a
// wire-format type (Constraint #8): it is an SPA-internal render projection
// computed by the caller from the landed goal frames, never a cross-boundary
// type. The `// not-wire-format` marker is explicit.

import { Plus, Swap, Minus, ArrowBendUpRight } from '@phosphor-icons/react'

// not-wire-format — SPA-internal display type for the amendment diff card.
// The backend surfaces the amendment in chat; this shape is the render-time
// projection, computed by the caller from landed goal data. Not a wire type.
export interface GoalAmendmentDiffData {
  added: string[]
  changed: { from: string; to: string }[]
  dropped: string[]
}

export interface GoalAmendmentDiffProps {
  diff: GoalAmendmentDiffData
}

export function GoalAmendmentDiff({ diff }: GoalAmendmentDiffProps) {
  const empty = diff.added.length === 0 && diff.changed.length === 0 && diff.dropped.length === 0

  return (
    <div
      data-testid="goal-amendment-diff"
      className="my-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-3 text-xs"
    >
      {/* Header — amendment banner */}
      <div className="flex items-center gap-2 mb-2">
        <Swap size={13} weight="fill" className="shrink-0 text-[color:var(--color-warning)]" aria-hidden="true" />
        <span className="font-medium text-[var(--color-secondary)] uppercase tracking-wide text-[10px]">
          Goal amendment — reply to confirm or reject
        </span>
      </div>

      {empty && (
        <p className="text-[var(--color-muted)] italic" data-testid="goal-amendment-empty">
          No changes detected — the restated goal matches the current one.
        </p>
      )}

      {/* Added criteria — green */}
      {diff.added.length > 0 && (
        <div className="mt-1.5" data-testid="goal-amendment-added">
          <div className="flex items-center gap-1.5 text-[color:var(--color-success)] mb-0.5 text-[10px] uppercase tracking-wide">
            <Plus size={11} weight="bold" aria-hidden="true" />
            Added
          </div>
          <ul className="space-y-0.5 ml-1">
            {diff.added.map((c, i) => (
              <li key={i} className="text-[var(--color-secondary)] break-words flex items-start gap-1.5">
                <Plus size={10} className="mt-0.5 shrink-0 text-[color:var(--color-success)]" aria-hidden="true" />
                <span>{c}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Changed criteria — amber, from → to */}
      {diff.changed.length > 0 && (
        <div className="mt-1.5" data-testid="goal-amendment-changed">
          <div className="flex items-center gap-1.5 text-[color:var(--color-warning)] mb-0.5 text-[10px] uppercase tracking-wide">
            <Swap size={11} aria-hidden="true" />
            Changed
          </div>
          <ul className="space-y-0.5 ml-1">
            {diff.changed.map((c, i) => (
              <li key={i} className="text-[var(--color-secondary)] break-words flex items-start gap-1.5">
                <Swap size={10} className="mt-0.5 shrink-0 text-[color:var(--color-warning)]" aria-hidden="true" />
                <span>
                  <span className="line-through text-[var(--color-muted)]">{c.from}</span>
                  <span className="mx-1 text-[var(--color-muted)]">{'→'}</span>
                  <span>{c.to}</span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Dropped criteria — red strikethrough */}
      {diff.dropped.length > 0 && (
        <div className="mt-1.5" data-testid="goal-amendment-dropped">
          <div className="flex items-center gap-1.5 text-[color:var(--color-error)] mb-0.5 text-[10px] uppercase tracking-wide">
            <Minus size={11} weight="bold" aria-hidden="true" />
            Dropped
          </div>
          <ul className="space-y-0.5 ml-1">
            {diff.dropped.map((c, i) => (
              <li key={i} className="break-words flex items-start gap-1.5">
                <Minus size={10} className="mt-0.5 shrink-0 text-[color:var(--color-error)]" aria-hidden="true" />
                <span className="line-through text-[var(--color-muted)]">{c}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Confirmation prompt — conversational, no buttons */}
      {!empty && (
        <div className="mt-2.5 flex items-center gap-1.5 text-[var(--color-muted)] italic">
          <ArrowBendUpRight size={11} aria-hidden="true" />
          <span>Reply to confirm the amendment, or restate again.</span>
        </div>
      )}
    </div>
  )
}
