/**
 * AdvancedDisclosure — reusable collapsible "Advanced" section.
 *
 * The collapsed header shows a configurable title and an optional safe-defaults
 * summary line. Children are NOT mounted until the disclosure is expanded
 * (conditional render, not CSS visibility) so they don't affect tab order or
 * ARIA tree when hidden.
 *
 * Modelled on the CreateAgentModal.tsx:278-307 collapsed pattern but extracted
 * into a single reusable primitive (spec §2, #316).
 *
 * Consumed by: ToolPolicyEditor (#318), ScheduleFormSheet (US-A1), and any
 * future surface that wants a safe-to-skip advanced section.
 */

import { useState } from 'react'
import { CaretDown, CaretUp } from '@phosphor-icons/react'

export interface AdvancedDisclosureProps {
  /** Button label; defaults to "Advanced". */
  title?: string
  /** One-line safe-defaults hint shown in the collapsed header (optional). */
  summary?: string
  /** Whether the disclosure starts open. Defaults to false. */
  defaultOpen?: boolean
  children: React.ReactNode
}

export function AdvancedDisclosure({
  title = 'Advanced',
  summary,
  defaultOpen = false,
  children,
}: AdvancedDisclosureProps) {
  const [open, setOpen] = useState(defaultOpen)

  return (
    <div
      className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
      data-testid="advanced-disclosure"
    >
      <button tabIndex={0}
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
        data-testid="advanced-disclosure-trigger"
      >
        <span className="flex flex-col items-start gap-0.5 text-left">
          <span>{title}</span>
          {summary && !open && (
            <span className="text-[11px] font-normal text-[var(--color-muted)]">{summary}</span>
          )}
        </span>
        {open ? <CaretUp size={13} /> : <CaretDown size={13} />}
      </button>

      {open && (
        <div
          className="px-3 pb-3 border-t border-[var(--color-border)] pt-3"
          data-testid="advanced-disclosure-content"
        >
          {children}
        </div>
      )}
    </div>
  )
}
