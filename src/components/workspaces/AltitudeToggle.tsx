/**
 * AltitudeToggle — board depth/altitude control.
 *
 * Switches the Board between two modes:
 *   'top-level' (default) — only root tasks; children nested-collapsed.
 *   'show-all'            — children expanded inline under their parent card.
 *
 * Persists the choice per-session in the workspacesStore (Zustand).
 * Placement: board toolbar, right side — next to the New Task button.
 */

import { cn } from '@/lib/utils'
import type { BoardAltitude } from '@/store/workspacesStore'

interface AltitudeToggleProps {
  value: BoardAltitude
  onChange: (next: BoardAltitude) => void
}

const OPTIONS: { value: BoardAltitude; label: string }[] = [
  { value: 'top-level', label: 'Top-level' },
  { value: 'show-all',  label: 'Show all' },
]

export function AltitudeToggle({ value, onChange }: AltitudeToggleProps) {
  return (
    <div
      className="flex items-center rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-0.5 gap-0.5"
      role="group"
      aria-label="Board depth"
    >
      {OPTIONS.map((opt) => (
        <button
          key={opt.value}
          type="button"
          role="radio"
          aria-checked={value === opt.value}
          onClick={() => onChange(opt.value)}
          className={cn(
            'px-2.5 py-1 text-[11px] font-medium rounded-md transition-colors',
            value === opt.value
              ? 'bg-[var(--color-surface-1)] text-[var(--color-secondary)] shadow-sm'
              : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
          )}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}
