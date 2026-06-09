import { Plus } from '@phosphor-icons/react'
import type { MilestoneWithProgress } from '@/lib/api'
import { cn } from '@/lib/utils'

// Special sentinel values for non-milestone filters
export const MILESTONE_FILTER_ALL = null
export const MILESTONE_FILTER_UNSCHEDULED = '__unscheduled__'

interface MilestoneFilterPillsProps {
  milestones: MilestoneWithProgress[]
  /** null = All, '__unscheduled__' = unscheduled tasks, milestone.id = filtered */
  activeMilestoneId: string | null
  onSelect: (id: string | null) => void
  onNewMilestone: () => void
}

export function MilestoneFilterPills({
  milestones,
  activeMilestoneId,
  onSelect,
  onNewMilestone,
}: MilestoneFilterPillsProps) {
  const activeLabel =
    activeMilestoneId === MILESTONE_FILTER_ALL
      ? 'All'
      : activeMilestoneId === MILESTONE_FILTER_UNSCHEDULED
        ? 'Unscheduled'
        : (milestones.find((m) => m.id === activeMilestoneId)?.name ?? 'All')

  return (
    <div
      role="group"
      aria-label={`Active milestone filter: ${activeLabel}`}
      className="flex items-center gap-1.5 px-4 py-2 overflow-x-auto flex-wrap"
    >
      {/* All */}
      <Pill
        label="All"
        active={activeMilestoneId === MILESTONE_FILTER_ALL}
        onClick={() => onSelect(MILESTONE_FILTER_ALL)}
      />

      {/* Per-milestone pills */}
      {milestones.map((m) => (
        <Pill
          key={m.id}
          label={m.name}
          active={activeMilestoneId === m.id}
          onClick={() => onSelect(m.id)}
        />
      ))}

      {/* Unscheduled — only shown when at least one milestone exists */}
      {milestones.length > 0 && (
        <Pill
          label="Unscheduled"
          active={activeMilestoneId === MILESTONE_FILTER_UNSCHEDULED}
          onClick={() => onSelect(MILESTONE_FILTER_UNSCHEDULED)}
          muted
        />
      )}

      {/* New milestone button */}
      <button
        type="button"
        onClick={onNewMilestone}
        className="flex items-center gap-1 rounded-full border border-dashed border-[var(--color-border)] px-3 py-1 text-xs text-[var(--color-muted)] hover:border-[var(--color-accent)] hover:text-[var(--color-accent)] transition-colors flex-shrink-0"
      >
        <Plus size={10} />
        New milestone
      </button>
    </div>
  )
}

function Pill({
  label,
  active,
  onClick,
  muted = false,
}: {
  label: string
  active: boolean
  onClick: () => void
  muted?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex-shrink-0 rounded-full px-3 py-1 text-xs font-medium transition-colors',
        active
          ? 'bg-[var(--color-accent)] text-[var(--color-primary)]'
          : muted
            ? 'bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
            : 'bg-[var(--color-surface-2)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]/80',
      )}
    >
      {label}
    </button>
  )
}
