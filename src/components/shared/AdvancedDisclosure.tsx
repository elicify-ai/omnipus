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
 * Consumed by: ToolPolicyEditor (#318) and any future surface that wants a
 * safe-to-skip advanced section.
 */

import { useState } from 'react'
import { DisclosureRow } from '@/components/ui/disclosure-row'

export interface AdvancedDisclosureProps {
  /** Button label; defaults to "Advanced". */
  title?: string
  /** One-line safe-defaults hint shown in the collapsed header (optional). */
  summary?: string
  /** Whether the disclosure starts open. Defaults to false. */
  defaultOpen?: boolean
  /** Optional class override for the title text itself (e.g. a caller that wants a section-heading weight/size instead of the default label styling). */
  titleClassName?: string
  children: React.ReactNode
}

export function AdvancedDisclosure({
  title = 'Advanced',
  summary,
  defaultOpen = false,
  titleClassName,
  children,
}: AdvancedDisclosureProps) {
  const [open, setOpen] = useState(defaultOpen)

  return (
    <div
      className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
      data-testid="advanced-disclosure"
    >
      <DisclosureRow
        expanded={open}
        onExpandedChange={setOpen}
        expandable
        caretSize={13}
        data-testid="advanced-disclosure-trigger"
        className="w-full rounded-none px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
      >
        <span className="flex flex-col items-start gap-[var(--space-0-5)] text-left">
          <span className={titleClassName}>{title}</span>
          {summary && !open && (
            <span className="text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] text-[var(--color-muted)]">{summary}</span>
          )}
        </span>
      </DisclosureRow>

      {open && (
        <div
          className="px-[var(--space-2-5)] pb-[var(--space-2-5)] border-t border-[var(--color-border)] pt-[var(--space-2-5)]"
          data-testid="advanced-disclosure-content"
        >
          {children}
        </div>
      )}
    </div>
  )
}
