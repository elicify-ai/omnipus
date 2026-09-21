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

import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
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

// WAI-ARIA radio group pattern, via the shared `RadioGroup`/`RadioGroupItem`
// primitive (src/components/ui/radio-group.tsx) — built specifically to
// replace this component's own former hand-rolled roving-tabindex
// implementation (see that file's doc comment). Behavior is unchanged: one
// radio is in the tab sequence (the checked one), arrow keys move AND
// immediately select the adjacent option; RadioGroup additionally supports
// Home/End (jump to first/last), a WAI-ARIA APG addition this component
// never had, not a removed capability.
export function AltitudeToggle({ value, onChange }: AltitudeToggleProps) {
  return (
    <RadioGroup
      value={value}
      onValueChange={(next) => onChange(next as BoardAltitude)}
      aria-label="Board depth"
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-[var(--space-0-5)] gap-[var(--space-0-5)]"
    >
      {OPTIONS.map((opt) => {
        const checked = value === opt.value
        return (
          <RadioGroupItem
            key={opt.value}
            value={opt.value}
            className={cn(
              'h-auto w-auto justify-center border-0 bg-transparent px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium rounded-md transition-colors',
              checked
                ? 'bg-[var(--color-surface-1)] text-[var(--color-secondary)] shadow-sm hover:bg-[var(--color-surface-1)] hover:text-[var(--color-secondary)]'
                : 'text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]',
            )}
          >
            {opt.label}
          </RadioGroupItem>
        )
      })}
    </RadioGroup>
  )
}
