// GoalEchoCard — ADR-053 FE-8 / US-3 / design §1 (D11); criteria breakdown
// per ADR-074 D5.2 / judgment-first FR-011 (US-6). Confirm/Cancel/Amend
// buttons per ADR-078 D1. Restated statement + judgment icons + Definition
// of Done accordion per ADR-080 D-STATEMENT/D-TYPES/D-DOD.
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
// Redesign (operator report 2026-09-07: a live goal with 16 criteria + 4 DoD
// items overflowed the viewport, taking the Confirm/Amend/Cancel buttons off
// screen with no way to reach them). Two changes fix that at the source
// rather than patching around it:
//   1. The card adopts AskUserQuestionCard's flat, hairline-delimited zone
//      style (no boxy rounded/bordered/tinted wrapper) — visual parity with
//      the sibling clarify-question surface, and less chrome per row.
//   2. Both the criteria ("Done when") and Definition of Done lists render
//      behind a collapsed-by-default accordion (see `GoalAccordionSection`
//      below) instead of always-expanded, so card height no longer scales
//      with criteria count. The buttons stay in the flat, un-collapsible
//      footer, so they are always reachable regardless of how long either
//      list gets. The DoD header additionally surfaces an "N inferred —
//      review" hint (still readable collapsed) so an inferred item is never
//      hidden from view, only from initial vertical space — the user still
//      has to expand and see it before confirming.
//
// The criteria breakdown arrives on the goal_status frame's optional
// `criteria` field (present on the `queued` pending-confirm emission,
// ADR-074 D5.2). Rendering is plain-language-FIRST: each row leads with the
// criterion text; a technical payload (machine-check command verbatim, or a
// behavior count) renders as a quiet per-row "verifies via:" chip. Row and
// chip rendering — including the chip's formatting and the ADR-080 judgment
// icon — is delegated entirely to the shared CriteriaBreakdown component
// (D5.4), so the same criterion reads identically here and in the Create
// Task / Create Plan flows. `[kind]` classification tokens are NOT
// user-facing content and never render.
//
// ADR-080 D-DOD: the `queued` frame's optional `dod` array is the goal's
// Definition of Done — generic standing quality gates, DISTINCT from the
// outcome-specific `criteria` — rendered as its own labeled accordion below
// the criteria one. Every DoD item is judgment-tagged like a criterion; an
// item whose `provenance === 'inferred'` (the compiler's bounded layer-4
// guess, never silently activated) is flagged both on the collapsed DoD
// header (so the user knows to expand) and, once expanded, per-row by the
// shared CriteriaBreakdown renderer, so the setter can approve or drop it
// before confirming.
//
// Purely presentational — driven by props. Literal commands are shown
// verbatim so the user can vet them before confirming — they run under the
// goal-bearing agent's own tool policy, never a bypass. The three action
// callbacks (`onConfirm`/`onCancel`/`onAmend`) are optional so the card still
// renders standalone (e.g. in tests) without a wired container; the button
// row itself only ever shows while `frame.state === 'queued'` (ADR-078 D1 —
// a stale card past that point renders no buttons at all, which subsumes the
// ADR's "disable once no longer queued" risk note).

import { useState } from 'react'
import {
  Target,
  ArrowBendUpRight,
  Check,
  PencilSimple,
  X,
  CaretRight,
  CaretDown,
} from '@phosphor-icons/react'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { CriteriaBreakdown, type CriteriaBreakdownItem } from '@/components/shared/CriteriaBreakdown'

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

/**
 * A single collapsed-by-default accordion section — the header is always a
 * one-line, keyboard-accessible `<button>` (`aria-expanded`, native focus/
 * Enter/Space activation, no custom key handling needed); the list only
 * mounts once expanded, so a 16-criteria goal costs the same collapsed
 * height as a 1-criterion one. No open/close transition is applied to the
 * content itself (instant show/hide) — only the chevron glyph swaps, so
 * there is no motion to gate behind `prefers-reduced-motion` for the content
 * itself; the swap is an instant icon substitution, not an animation.
 */
function GoalAccordionSection({
  testId,
  label,
  hint,
  children,
}: {
  testId: string
  label: string
  /** Short trailing hint rendered in the accent color, e.g. "2 inferred — review". */
  hint?: string
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="mt-2.5" data-testid={testId}>
      <button
        type="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        data-testid={`${testId}-trigger`}
        className="flex w-full items-center gap-1.5 text-left text-[10px] uppercase tracking-wide text-[var(--color-muted)] transition-colors hover:text-[var(--color-secondary)]"
      >
        {open ? (
          <CaretDown size={10} className="shrink-0" aria-hidden="true" />
        ) : (
          <CaretRight size={10} className="shrink-0" aria-hidden="true" />
        )}
        <span>{label}</span>
        {hint && (
          <span
            className="normal-case tracking-normal text-[var(--color-accent)]"
            data-testid={`${testId}-hint`}
          >
            — {hint}
          </span>
        )}
      </button>
      {open && (
        <div className="mt-1.5" data-testid={`${testId}-content`}>
          {children}
        </div>
      )}
    </div>
  )
}

export function GoalEchoCard({ frame, onConfirm, onCancel, onAmend }: GoalEchoCardProps) {
  const criteria = frame.criteria ?? []
  const dod: CriteriaBreakdownItem[] = frame.dod ?? []
  const isPending = frame.state === 'queued'
  const inferredDodCount = dod.filter((d) => d.provenance === 'inferred').length

  return (
    <div
      data-testid="goal-echo-card"
      className="my-2 border-y border-[var(--color-border)] py-2.5 px-1 text-xs"
    >
      {/* Header — compiled-goal banner, flat zone style matching AskUserQuestionCard. */}
      <div className="flex items-center gap-2 mb-2">
        <Target size={12} weight="fill" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
        <span className="font-mono text-[10px] uppercase tracking-widest text-[var(--color-muted)]">
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

      {/* Criteria breakdown — collapsed-by-default accordion (a long ladder
          of criteria must never push the Confirm/Cancel/Amend row off
          screen, see the redesign note above). Plain language first, a
          per-row verifies-via chip for technical payloads (ADR-074 D5.2 /
          FR-011), a small judgment icon (boolean/quantitative/artifact,
          ADR-080 D-TYPES) on every row once expanded. Rendered by the shared
          CriteriaBreakdown (D5.4) so criteria read identically on every
          confirmation surface. */}
      {criteria.length > 0 && (
        <GoalAccordionSection
          testId="goal-echo-criteria"
          label={`Done when · ${criteria.length} criteri${criteria.length === 1 ? 'on' : 'a'}`}
        >
          <CriteriaBreakdown criteria={criteria} />
        </GoalAccordionSection>
      )}

      {/* Definition of Done (ADR-080 D-DOD) — a DISTINCT accordion, generic
          standing quality gates rather than outcome-specific checks. Every
          item carries a judgment icon like a criterion; an item derived by
          bounded inference (`provenance === 'inferred'`) is flagged on the
          COLLAPSED header itself ("N inferred — review") so it is never
          hidden from the user's attention, and again per-row once expanded
          by the shared CriteriaBreakdown renderer — a layer-4 gate is never
          silently activated. */}
      {dod.length > 0 && (
        <GoalAccordionSection
          testId="goal-echo-dod"
          label={`Definition of Done · ${dod.length} item${dod.length === 1 ? '' : 's'}`}
          hint={inferredDodCount > 0 ? `${inferredDodCount} inferred — review` : undefined}
        >
          <CriteriaBreakdown criteria={dod} />
        </GoalAccordionSection>
      )}

      {/* Confirm / Cancel / Amend — ADR-078 D1. Rendered only while the card
          is pending confirmation (`queued`); a card left mounted past
          activation/clear renders no buttons at all. This row is OUTSIDE
          both accordions, so it never scrolls out of reach behind a long
          criteria/DoD list — collapsing either accordion is the fix for
          that, not scrolling further. */}
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
