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

import { useRef } from 'react'
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

// WAI-ARIA radio group pattern: exactly one radio is in the tab sequence
// (the checked one — roving tabindex); arrow keys move AND immediately
// select the adjacent option, matching how a native <input type="radio">
// group behaves.
export function AltitudeToggle({ value, onChange }: AltitudeToggleProps) {
  const optionRefs = useRef<Partial<Record<BoardAltitude, HTMLButtonElement | null>>>({})

  function moveSelection(delta: 1 | -1) {
    const currentIndex = OPTIONS.findIndex((opt) => opt.value === value)
    const next = OPTIONS[(currentIndex + delta + OPTIONS.length) % OPTIONS.length]
    onChange(next.value)
    optionRefs.current[next.value]?.focus()
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLButtonElement>) {
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        e.preventDefault()
        moveSelection(1)
        break
      case 'ArrowLeft':
      case 'ArrowUp':
        e.preventDefault()
        moveSelection(-1)
        break
      default:
        break
    }
  }

  return (
    <div
      className="flex items-center rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-0.5 gap-0.5"
      role="radiogroup"
      aria-label="Board depth"
    >
      {OPTIONS.map((opt) => {
        const checked = value === opt.value
        return (
          <button
            key={opt.value}
            ref={(el) => {
              optionRefs.current[opt.value] = el
            }}
            type="button"
            role="radio"
            aria-checked={checked}
            tabIndex={checked ? 0 : -1}
            onClick={() => onChange(opt.value)}
            onKeyDown={handleKeyDown}
            className={cn(
              'px-2.5 py-1 text-[11px] font-medium rounded-md transition-colors',
              checked
                ? 'bg-[var(--color-surface-1)] text-[var(--color-secondary)] shadow-sm'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            )}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
