/**
 * CriteriaBreakdown — shared presentational criteria list (ADR-074 D5.4;
 * judgment badge + inferred-provenance flag per ADR-080 D-TYPES/D-DOD).
 *
 * Renders an itemized Definition-of-Done breakdown: plain-language text
 * first, with a mono "verifies via:" chip beneath any criterion that carries
 * a technical payload. Used by the Create Task / Create Plan flows
 * (AcceptanceCriteriaEditor) to present agent-drafted criteria for
 * confirmation, and by the goal confirmation card (GoalEchoCard, D5.2) —
 * the same component on every surface, so a criterion reads identically
 * everywhere it is shown.
 *
 * ADR-080 D-TYPES: when a criterion carries a `judgment` (boolean /
 * quantitative / artifact — "what SHAPE of claim is this"), a small badge
 * renders alongside the text. ADR-080 D-DOD: when a criterion carries
 * `provenance === 'inferred'` (a DoD item the compiler proposed rather than
 * one the setter/workspace stated), a second flag reads "inferred — confirm
 * or drop" — the minimal structured surface for "SHOWN, never silently
 * invented." Both fields are optional and absent on ordinary task/plan
 * criteria, so this is purely additive for those callers.
 *
 * Deliberately coupled by INTERFACE, not imports: `CriteriaBreakdownItem` is
 * a minimal structural subset of the generated `AcceptanceCriterion` wire
 * shape, so both `AcceptanceCriterion[]` (task criteria / plan dod) and the
 * goal-frame criteria/dod assign to it without this file importing either
 * consumer's types. No `[kind]` classification label is user-facing
 * (judgment-first spec §4 prohibition).
 */

/** Structural subset of `AcceptanceCriterion` this component reads. */
export interface CriteriaBreakdownItem {
  id?: string
  text: string
  /** ADR-080 D-TYPES — what SHAPE of claim this is, not by what mechanism it is verified. */
  judgment?: 'boolean' | 'quantitative' | 'artifact'
  /** ADR-080 D-DOD — meaningful only on DoD items; `'inferred'` flags a compiler-proposed gate for approve/drop. */
  provenance?: 'stated' | 'workspace' | 'floor' | 'inferred'
  check?: { command: string; expected_exit_code: number }
  behavior?: {
    tool: string
    min_count?: number
    max_count?: number
    scope?: 'attempt' | 'task_session'
  }
}

/**
 * Formats the technical payload of a criterion for the "verifies via:" chip,
 * or returns null when the criterion is plain prose (no chip).
 *
 * - check:    `go test ./... -> exit 0`
 * - behavior: `search_web x3-5` — `x3` when min == max (x0 = never call),
 *             `x3+` when unbounded; ` per attempt` appended for the
 *             `attempt` scope (the `task_session` default stays unlabeled).
 */
export function formatVerifiesVia(c: CriteriaBreakdownItem): string | null {
  if (c.check) {
    return `${c.check.command} -> exit ${c.check.expected_exit_code}`
  }
  if (c.behavior) {
    const min = c.behavior.min_count ?? 1
    const max = c.behavior.max_count
    const range = max === undefined ? `x${min}+` : max === min ? `x${min}` : `x${min}-${max}`
    const scope = c.behavior.scope === 'attempt' ? ' per attempt' : ''
    return `${c.behavior.tool} ${range}${scope}`
  }
  return null
}

interface CriteriaBreakdownProps {
  criteria: CriteriaBreakdownItem[]
  /** Rendered instead of the list when there are zero criteria (optional). */
  emptyText?: string
}

export function CriteriaBreakdown({ criteria, emptyText }: CriteriaBreakdownProps) {
  if (criteria.length === 0) {
    if (!emptyText) return null
    return <p className="text-xs text-[var(--color-muted)]">{emptyText}</p>
  }

  return (
    <ul className="space-y-1.5" data-testid="criteria-breakdown">
      {criteria.map((c, idx) => {
        const verifiesVia = formatVerifiesVia(c)
        return (
          <li
            key={c.id ?? idx}
            className="px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs space-y-0.5"
          >
            <div className="flex items-start gap-1.5">
              {c.judgment && (
                <span
                  data-testid="criterion-judgment-badge"
                  className="mt-[1px] shrink-0 rounded border border-[var(--color-border)] px-1 py-[1px] text-[9px] uppercase tracking-wide text-[var(--color-muted)]"
                >
                  {c.judgment}
                </span>
              )}
              <p className="text-[var(--color-secondary)] flex-1 min-w-0">{c.text}</p>
            </div>
            {verifiesVia && (
              <p className="inline-flex max-w-full items-baseline gap-1 rounded bg-[var(--color-surface-1)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-muted)]">
                <span className="shrink-0">verifies via:</span>
                <span className="truncate">{verifiesVia}</span>
              </p>
            )}
            {c.provenance === 'inferred' && (
              <p
                data-testid="criterion-inferred-flag"
                className="text-[10px] italic text-[var(--color-accent)]"
              >
                inferred — confirm or drop
              </p>
            )}
          </li>
        )
      })}
    </ul>
  )
}
