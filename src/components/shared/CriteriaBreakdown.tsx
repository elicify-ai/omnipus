/**
 * CriteriaBreakdown — shared presentational criteria list (ADR-074 D5.4).
 *
 * Renders an itemized Definition-of-Done breakdown: plain-language text
 * first, with a mono "verifies via:" chip beneath any criterion that carries
 * a technical payload. Used by the Create Task / Create Plan flows
 * (AcceptanceCriteriaEditor) to present agent-drafted criteria for
 * confirmation, and by the goal confirmation card (GoalEchoCard, D5.2) —
 * the same component on every surface, so a criterion reads identically
 * everywhere it is shown.
 *
 * Deliberately coupled by INTERFACE, not imports: `CriteriaBreakdownItem` is
 * a minimal structural subset of the generated `AcceptanceCriterion` wire
 * shape, so both `AcceptanceCriterion[]` (task criteria / plan dod) and the
 * goal-frame criteria assign to it without this file importing either
 * consumer's types. No `[kind]` classification label is user-facing
 * (judgment-first spec §4 prohibition).
 */

/** Structural subset of `AcceptanceCriterion` this component reads. */
export interface CriteriaBreakdownItem {
  id?: string
  text: string
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
            <p className="text-[var(--color-secondary)]">{c.text}</p>
            {verifiesVia && (
              <p className="inline-flex max-w-full items-baseline gap-1 rounded bg-[var(--color-surface-1)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-muted)]">
                <span className="shrink-0">verifies via:</span>
                <span className="truncate">{verifiesVia}</span>
              </p>
            )}
          </li>
        )
      })}
    </ul>
  )
}
