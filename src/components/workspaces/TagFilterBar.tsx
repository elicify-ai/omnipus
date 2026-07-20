import { useMemo } from 'react'
import type { Task } from '@/lib/api'
import { PLAN_FILTER_UNTAGGED, distinctTags } from '@/lib/planFilter'
import { cn } from '@/lib/utils'

interface TagFilterBarProps {
  /** Full (unfiltered) task list — used only to derive the available tag chips. */
  tasks: Task[]
  activeTagFilter: string | null
  onSelectTag: (tag: string | null) => void
}

/**
 * Tasks-screen toolbar tag filter (ADR-051) — plan filtering lives entirely
 * in the PlansFilterBand above the board now (plan tiles, not plan pills
 * here), so this component keeps just the tag filter. Uses `planFilter.ts`'s
 * `PLAN_FILTER_UNTAGGED` sentinel plus a `role=group`/`aria-pressed` a11y
 * grammar.
 */
export function TagFilterBar({ tasks, activeTagFilter, onSelectTag }: TagFilterBarProps) {
  const tags = useMemo(() => distinctTags(tasks), [tasks])

  if (tags.length === 0 && activeTagFilter === null) return null

  return (
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
    <button tabIndex={0}
      type="button"
      onClick={onClick}
      aria-pressed={active}
      title={label}
      className={cn(
        'flex-shrink-0 max-w-[160px] truncate rounded-full px-3 py-1 text-xs font-medium transition-colors',
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
