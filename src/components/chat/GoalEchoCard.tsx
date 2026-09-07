// GoalEchoCard — ADR-081 D5/D9 (work-first goal flow): re-keyed from
// "pending confirmation" to a REGISTERED RECORD VIEW. Criteria breakdown
// per ADR-074 D5.2 / judgment-first FR-011 (US-6). Restated statement +
// judgment icons + Definition of Done accordion per ADR-080
// D-STATEMENT/D-TYPES/D-DOD.
//
// Renders the goal's structured record IN CHAT (no form/modal, no
// approval controls): once the working agent registers or updates the
// record via `set_goal` (or a marker-path activation/restate), the engine
// emits the `goal_status` frame in state `active` with `criteria`/`dod`/
// `definition` populated (ADR-081 D5 — the SAME optional fields the old
// `queued`-state pending-confirm emission once populated; this component
// keeps its name and its rendering, only its keying and its footer change).
// The listing is presented as the agent's WORKING ASSUMPTIONS, not a
// proposal awaiting approval — steering (an ordinary chat message that
// changes direction) is the only control; there is no confirm/amend/cancel
// ritual anywhere (ADR-081 D9 deletes it in full, greenfield, no dormant
// branches).
//
// `definition` (the restated one-sentence statement) may be ABSENT: a
// marker-only goal legitimately stores an empty statement (the existing
// Prompt/Intent fallback, ADR-081 round-2 B-3) — the card renders
// gracefully without the statement block in that case, exactly as it
// already did for "legacy/ambiguous" frames pre-ADR-081.
//
// The criteria breakdown arrives on the goal_status frame's optional
// `criteria` field (ADR-074 D5.2). Rendering is plain-language-FIRST: each
// row leads with the criterion text; a technical payload (machine-check
// command verbatim, or a behavior count) renders as a quiet per-row
// "verifies via:" chip. Row and chip rendering — including the chip's
// formatting and the ADR-080 judgment icon — is delegated entirely to the
// shared CriteriaBreakdown component (D5.4), so the same criterion reads
// identically here and in the Create Task / Create Plan flows. `[kind]`
// classification tokens are NOT user-facing content and never render.
//
// ADR-080 D-DOD: the frame's optional `dod` array is the goal's Definition
// of Done — generic standing quality gates, DISTINCT from the
// outcome-specific `criteria` — rendered as its own labeled accordion below
// the criteria one. Every DoD item is judgment-tagged like a criterion; an
// item whose `provenance === 'inferred'` (the compiler's bounded layer-4
// guess, never silently activated) is flagged both on the collapsed DoD
// header (so the reader knows to expand) and, once expanded, per-row by the
// shared CriteriaBreakdown renderer — inferred items are always visible to
// the reader, not silently accepted.
//
// Redesign preserved verbatim from the pre-ADR-081 branch (operator report
// 2026-09-07: a live goal with 16 criteria + 4 DoD items overflowed the
// viewport): the card uses AskUserQuestionCard's flat, hairline-delimited
// zone style (no boxy rounded/bordered/tinted wrapper), and both the
// criteria ("Done when") and Definition of Done lists render behind a
// collapsed-by-default accordion (see `GoalAccordionSection` below) instead
// of always-expanded, so card height no longer scales with criteria count.
//
// Purely presentational — driven by props. Literal commands are shown
// verbatim so the reader can see exactly what will run — it runs under the
// goal-bearing agent's own tool policy, never a bypass. There is no button
// row and no action callbacks: the card renders standalone (e.g. in tests)
// with no wired container, same as before, but now unconditionally — there
// is no pending/active distinction left to gate on.

import { useState } from 'react'
import { Target, CaretRight, CaretDown } from '@phosphor-icons/react'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { CriteriaBreakdown, type CriteriaBreakdownItem } from '@/components/shared/CriteriaBreakdown'

export interface GoalEchoCardProps {
  /** The goal_status frame describing the active goal's record (condition + accounting + criteria breakdown). */
  frame: GoalStatusFrame
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

export function GoalEchoCard({ frame }: GoalEchoCardProps) {
  const criteria = frame.criteria ?? []
  const dod: CriteriaBreakdownItem[] = frame.dod ?? []
  const inferredDodCount = dod.filter((d) => d.provenance === 'inferred').length

  return (
    <div
      data-testid="goal-echo-card"
      className="my-2 border-y border-[var(--color-border)] py-2.5 px-1 text-xs"
    >
      {/* Header — record banner, flat zone style matching AskUserQuestionCard.
          No "reply to confirm" language (ADR-081 D9 deletes the confirm
          ritual): this is the agent's working assumptions, not a proposal. */}
      <div className="flex items-center gap-2 mb-2">
        <Target size={12} weight="fill" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
        <span className="font-mono text-[10px] uppercase tracking-widest text-[var(--color-muted)]">
          Goal — working assumptions
        </span>
      </div>

      {/* Restated goal statement (ADR-080 D-STATEMENT) — one clear sentence,
          the request restated close to the setter's own words, rendered as
          the LEAD line above the compiled condition. Additive-optional: not
          present on marker-path/legacy/ambiguous frames (ADR-081 round-2
          B-3) — rendered gracefully absent, no placeholder. */}
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

      {/* Criteria breakdown — collapsed-by-default accordion. Plain language
          first, a per-row verifies-via chip for technical payloads
          (ADR-074 D5.2 / FR-011), a small judgment icon (boolean/
          quantitative/artifact, ADR-080 D-TYPES) on every row once
          expanded. Rendered by the shared CriteriaBreakdown (D5.4) so
          criteria read identically on every surface. */}
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
          hidden from the reader's attention, and again per-row once
          expanded by the shared CriteriaBreakdown renderer — a layer-4
          gate is never silently activated. */}
      {dod.length > 0 && (
        <GoalAccordionSection
          testId="goal-echo-dod"
          label={`Definition of Done · ${dod.length} item${dod.length === 1 ? '' : 's'}`}
          hint={inferredDodCount > 0 ? `${inferredDodCount} inferred — review` : undefined}
        >
          <CriteriaBreakdown criteria={dod} />
        </GoalAccordionSection>
      )}
    </div>
  )
}
