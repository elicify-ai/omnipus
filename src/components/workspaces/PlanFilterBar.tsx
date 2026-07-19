import { useMemo } from 'react'
import { Plus } from '@phosphor-icons/react'
import type { Plan, Task } from '@/lib/api'
import { PLAN_FILTER_ALL, PLAN_FILTER_UNTAGGED, distinctTags } from '@/lib/planFilter'
import { planStateColor } from '@/lib/planStateColors'
import { cn } from '@/lib/utils'

interface PlanFilterBarProps {
  plans: Plan[]
  /** Full (unfiltered) task list — used only to derive the available tag chips. */
  tasks: Task[]
  activePlanId: string | null
  activeTagFilter: string | null
  onSelectPlan: (id: string | null) => void
  onSelectTag: (tag: string | null) => void
  onNewPlan: () => void
}

/**
 * Board toolbar filter bar (ADR-049, SD-C2/FR-080) — replaces the removed
 * `MilestoneFilterPills` 1:1: plan pills AND tag chips in one bar, with two
 * sentinels (`PLAN_FILTER_ALL`/`PLAN_FILTER_UNTAGGED`). Reuses
 * `MilestoneFilterPills`'s `role=group`/`aria-pressed` a11y grammar.
 */
export function PlanFilterBar({
  plans,
  tasks,
  activePlanId,
  activeTagFilter,
  onSelectPlan,
  onSelectTag,
  onNewPlan,
}: PlanFilterBarProps) {
  const tags = useMemo(() => distinctTags(tasks), [tasks])

  const activePlanLabel =
    activePlanId === PLAN_FILTER_ALL ? 'All' : (plans.find((p) => p.id === activePlanId)?.title ?? 'All')

  return (
    <div className="flex flex-col gap-1.5 flex-1 min-w-0">
      <div
        role="group"
        aria-label={`Active plan filter: ${activePlanLabel}`}
        className="flex items-center gap-1.5 overflow-x-auto flex-wrap"
      >
        <Pill label="All" active={activePlanId === PLAN_FILTER_ALL} onClick={() => onSelectPlan(PLAN_FILTER_ALL)} />
        {plans.map((p) => (
          <Pill
            key={p.id}
            label={p.title}
            active={activePlanId === p.id}
            onClick={() => onSelectPlan(p.id)}
            dotColor={planStateColor(p.state)}
          />
        ))}
        <button tabIndex={0}
          type="button"
          onClick={onNewPlan}
          className="flex items-center gap-1 rounded-full border border-dashed border-[var(--color-border)] px-3 py-1 text-xs text-[var(--color-muted)] hover:border-[var(--color-accent)] hover:text-[var(--color-accent)] transition-colors flex-shrink-0"
        >
          <Plus size={10} />
          New plan
        </button>
      </div>

      {(tags.length > 0 || activeTagFilter !== null) && (
        <div
          role="group"
          aria-label="Tag filter"
          className="flex items-center gap-1.5 overflow-x-auto flex-wrap"
        >
          <Pill label="All tags" active={activeTagFilter === null} onClick={() => onSelectTag(null)} muted />
          <Pill
            label="Untagged"
            active={activeTagFilter === PLAN_FILTER_UNTAGGED}
            onClick={() => onSelectTag(PLAN_FILTER_UNTAGGED)}
            muted
          />
          {tags.map((tag) => (
            <Pill
              key={tag}
              label={tag}
              active={activeTagFilter === tag}
              onClick={() => onSelectTag(activeTagFilter === tag ? null : tag)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function Pill({
  label,
  active,
  onClick,
  muted = false,
  dotColor,
}: {
  label: string
  active: boolean
  onClick: () => void
  muted?: boolean
  dotColor?: string
}) {
  return (
    <button tabIndex={0}
      type="button"
      onClick={onClick}
      aria-pressed={active}
      title={label}
      className={cn(
        'flex-shrink-0 flex items-center gap-1.5 max-w-[160px] rounded-full px-3 py-1 text-xs font-medium transition-colors',
        active
          ? 'bg-[var(--color-accent)] text-[var(--color-primary)]'
          : muted
            ? 'bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
            : 'bg-[var(--color-surface-2)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]/80',
      )}
    >
      {dotColor && (
        <span className="w-1.5 h-1.5 rounded-full flex-shrink-0" style={{ backgroundColor: dotColor }} />
      )}
      <span className="truncate">{label}</span>
    </button>
  )
}
