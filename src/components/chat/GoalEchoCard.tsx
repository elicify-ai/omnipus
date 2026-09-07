// GoalEchoCard — ADR-053 FE-8 / US-3 / design §1 (D11); criteria breakdown
// per ADR-074 D5.2 / judgment-first FR-011 (US-6). Confirm/Cancel/Amend
// buttons per ADR-078 D1. Restated statement + judgment badges + Definition
// of Done block per ADR-080 D-STATEMENT/D-TYPES/D-DOD.
//
// Renders the compiled-goal ECHO in the chat thread: when the engine compiles
// user intent into the goal definition + acceptance-criteria ladder (including
// literal machine-check commands), the agent echoes it back IN CHAT (no
// form/modal) and the user confirms. ADR-078 adds a click-to-confirm
// affordance alongside the original reply-to-confirm path (a bare chat
// message is still recognized by the backend's `IsGoalConfirm`), because a
// natural-language reply like "yeah let's do it" is not — the buttons remove
// the need to guess the exact confirm token.
//
// ADR-080 D-STATEMENT: the `queued` frame carries an additive-optional
// `definition` — the request restated as ONE clear sentence, distinct from
// `condition` (the compiled marker/condition text) — rendered as a lead line
// above the condition, when present (absent on legacy/ambiguous frames).
//
// The criteria breakdown arrives on the goal_status frame's optional
// `criteria` field (present on the `queued` pending-confirm emission,
// ADR-074 D5.2). Rendering is plain-language-FIRST: each row leads with the
// criterion text; a technical payload (machine-check command verbatim, or a
// behavior count) renders as a quiet per-row "verifies via:" chip. Row and
// chip rendering — including the chip's formatting and the ADR-080 judgment
// badge — is delegated entirely to the shared CriteriaBreakdown component
// (D5.4), so the same criterion reads identically here and in the Create
// Task / Create Plan flows. `[kind]` classification tokens are NOT
// user-facing content and never render.
//
// ADR-080 D-DOD: the `queued` frame's optional `dod` array is the goal's
// Definition of Done — generic standing quality gates, DISTINCT from the
// outcome-specific `criteria` — rendered as its own labeled block below the
// criteria. Every DoD item is judgment-tagged like a criterion; an item whose
// `provenance === 'inferred'` (the compiler's bounded layer-4 guess, never
// silently activated) is flagged "inferred — confirm or drop" by the shared
// CriteriaBreakdown renderer so the setter can approve or drop it before
// confirming.
//
// Purely presentational — driven by props. Literal commands are shown
// verbatim so the user can vet them before confirming — they run under the
// goal-bearing agent's own tool policy, never a bypass. The three action
// callbacks (`onConfirm`/`onCancel`/`onAmend`) are optional so the card still
// renders standalone (e.g. in tests) without a wired container; the button
// row itself only ever shows while `frame.state === 'queued'` (ADR-078 D1 —
// a stale card past that point renders no buttons at all, which subsumes the
// ADR's "disable once no longer queued" risk note).

import { Target, ArrowBendUpRight, Check, PencilSimple, X } from '@phosphor-icons/react'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { CriteriaBreakdown } from '@/components/shared/CriteriaBreakdown'

export interface GoalEchoCardProps {
  /** The goal_status frame describing the compiled goal (condition + accounting + criteria breakdown). */
  frame: GoalStatusFrame
  /** Activates the pending goal — sends the bare chat message `confirm`. */
  onConfirm?: () => void
  /** Clears the pending goal — sends `/goal clear`. */
  onCancel?: () => void
  /** Pre-fills the composer with `/goal ` so the user restates the goal. Sends nothing. */
  onAmend?: () => void
}

export function GoalEchoCard({ frame, onConfirm, onCancel, onAmend }: GoalEchoCardProps) {
  const criteria = frame.criteria ?? []
  const dod = frame.dod ?? []
  const isPending = frame.state === 'queued'
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

      {/* Restated goal statement (ADR-080 D-STATEMENT) — one clear sentence,
          the request restated close to the setter's own words, rendered as
          the LEAD line above the compiled condition. Additive-optional: not
          present on legacy/ambiguous frames. */}
      {frame.definition && (
        <p
          className="text-[var(--color-secondary)] break-words font-medium"
          data-testid="goal-echo-statement"
        >
          {frame.definition}
        </p>
      )}

      {/* Condition (the compiled goal definition) */}
      <p className="text-[var(--color-secondary)] break-words" data-testid="goal-echo-condition">
        {frame.condition}
      </p>

      {/* Round accounting */}
      <p className="text-[var(--color-muted)] mt-1.5 tabular-nums" data-testid="goal-echo-round">
        {frame.max_rounds} rounds · {frame.cap} concurrent loop{frame.cap === 1 ? '' : 's'}
      </p>

      {/* Criteria breakdown — plain language first, per-row verifies-via chip
          for technical payloads (ADR-074 D5.2 / FR-011), a small judgment
          badge (boolean/quantitative/artifact, ADR-080 D-TYPES) on every
          row. Rendered by the shared CriteriaBreakdown (D5.4) so criteria
          read identically on every confirmation surface. */}
      {criteria.length > 0 && (
        <div className="mt-2.5" data-testid="goal-echo-criteria">
          <div className="text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide">
            Done when
          </div>
          <CriteriaBreakdown criteria={criteria} />
        </div>
      )}

      {/* Definition of Done (ADR-080 D-DOD) — a DISTINCT block, generic
          standing quality gates rather than outcome-specific checks. Every
          item carries a judgment badge like a criterion; an item derived by
          bounded inference (`provenance === 'inferred'`) is flagged
          "inferred — confirm or drop" by the shared CriteriaBreakdown
          renderer, so a layer-4 gate is never silently activated. */}
      {dod.length > 0 && (
        <div className="mt-2.5 border-t border-[var(--color-border)] pt-2.5" data-testid="goal-echo-dod">
          <div className="text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide">
            Definition of Done
          </div>
          <CriteriaBreakdown criteria={dod} />
        </div>
      )}

      {/* Confirm / Cancel / Amend — ADR-078 D1. Rendered only while the card
          is pending confirmation (`queued`); a card left mounted past
          activation/clear renders no buttons at all. */}
      {isPending && (
        <div className="mt-2.5 flex items-center gap-2" data-testid="goal-echo-actions">
          <button
            type="button"
            tabIndex={0}
            onClick={onConfirm}
            data-testid="goal-echo-confirm"
            className="inline-flex items-center gap-1 rounded-md border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-2.5 py-1 text-[11px] font-medium text-[var(--color-accent)] transition-colors hover:bg-[var(--color-accent)]/20"
          >
            <Check size={12} weight="bold" aria-hidden="true" />
            Confirm
          </button>
          <button
            type="button"
            tabIndex={0}
            onClick={onAmend}
            data-testid="goal-echo-amend"
            className="inline-flex items-center gap-1 rounded-md border border-transparent px-2.5 py-1 text-[11px] text-[var(--color-secondary)]/80 transition-colors hover:border-[var(--color-secondary)]/20 hover:text-[var(--color-secondary)]"
          >
            <PencilSimple size={12} aria-hidden="true" />
            Amend
          </button>
          <button
            type="button"
            tabIndex={0}
            onClick={onCancel}
            data-testid="goal-echo-cancel"
            className="inline-flex items-center gap-1 rounded-md border border-transparent px-2.5 py-1 text-[11px] text-[var(--color-muted)] transition-colors hover:text-[var(--color-secondary)]"
          >
            <X size={12} aria-hidden="true" />
            Cancel
          </button>
        </div>
      )}

      {/* Secondary hint — a channel user with no card can still confirm by
          typing (backend `IsGoalConfirm`), so the prose stays below the
          buttons. */}
      <div className="mt-2 flex items-center gap-1.5 text-[var(--color-muted)] italic">
        <ArrowBendUpRight size={11} aria-hidden="true" />
        <span>Reply to confirm, or restate to amend.</span>
      </div>
    </div>
  )
}
