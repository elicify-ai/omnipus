import * as React from 'react'
import { CaretDown, CaretUp } from '@phosphor-icons/react'
import { Button, type ButtonProps } from './button'
import { cn } from '@/lib/utils'

/**
 * DisclosureRow — the "tool call row header" shape: a full-width disclosure
 * toggle that expands/collapses a detail panel below it, disabled (and with
 * `aria-expanded` OMITTED, not pinned to `false`) while there is nothing to
 * disclose yet (e.g. the call is still running) or ever (no args/result/
 * error at all). C2-PREP inventory.json's own `target` column called this out
 * on 9 near-identical hand-built rows and recommended building it once:
 * GenericToolCall.tsx (the canonical shape — read before changing this file),
 * ToolCallBadge.tsx, ActivityPanel.tsx, SubagentBlock.tsx,
 * FileReadPreview.tsx, WebSearchResult.tsx, WebFetchPreview.tsx,
 * BrowserNavigate.tsx, BrowserTool.tsx, BashOutput.tsx. A tenth file,
 * FileTreeView.tsx, shares the exact same header shape but is C1-owned
 * (tree-indentation ledger group) — once that lane points its header at this
 * primitive too, the shape converges to one definition everywhere.
 *
 * Built on `Button`, never a raw `<button>` — the design-system
 * `controls/raw-button` lock flags a literal `<button>` unconditionally, and
 * `Button` already carries the `data-ds-action` hit-region stamp and the
 * repo's WebKit tabindex convention (see `./segmented-control.tsx`'s doc
 * comment for the same reasoning).
 *
 * Sibling-not-nested convention: a disclosure toggle must never contain a
 * second interactive control (a `<button>` cannot nest inside a `<button>`).
 * A row that also offers an independent action — e.g. BrowserNavigate.tsx's
 * "Watch live" launcher — renders that action as a SIBLING of `DisclosureRow`
 * inside the caller's own flex row, never as a child. `DisclosureRow` itself
 * is a single flex item (`flex-1`) sized to share that row with such
 * siblings; it does not provide a slot for them.
 */

export interface DisclosureRowProps
  extends Omit<ButtonProps, 'variant' | 'aria-expanded' | 'onClick' | 'children' | 'asChild'> {
  /** Whether the disclosed detail panel is currently open. */
  expanded: boolean
  /** Called with the next open state when the user activates the toggle. Never called while `expandable` is false. */
  onExpandedChange: (expanded: boolean) => void
  /**
   * Whether this row currently has anything to disclose. `false` disables
   * the row (native `disabled` — removed from the tab order) and OMITS
   * `aria-expanded` entirely rather than pinning it to `false`, so assistive
   * tech never announces "collapsible" on a row that cannot actually expand
   * — the deliberate convention carried over byte-for-byte from
   * GenericToolCall.tsx / ToolCallBadge.tsx.
   */
  expandable: boolean
  /** Row content — status indicator, label, status text. The caret renders after this, automatically, pushed to the row's far right. */
  children: React.ReactNode
  /** Suppress the trailing caret even when `expandable`. Default false — every audited call site shows it. */
  hideCaret?: boolean
  /** Caret icon size in px. Default 12, matching every existing call site. */
  caretSize?: number
}

const DisclosureRow = React.forwardRef<HTMLButtonElement, DisclosureRowProps>(
  (
    {
      expanded,
      onExpandedChange,
      expandable,
      children,
      hideCaret = false,
      caretSize = 12,
      className,
      disabled,
      ...props
    },
    ref,
  ) => {
    const isDisabled = Boolean(disabled) || !expandable
    return (
      <Button
        ref={ref}
        type="button"
        variant="ghost"
        size="sm"
        onClick={() => {
          if (expandable) onExpandedChange(!expanded)
        }}
        aria-expanded={expandable ? expanded : undefined}
        disabled={isDisabled}
        data-state={expandable ? (expanded ? 'open' : 'closed') : undefined}
        className={cn(
          // Button's base classes set their own text-[length:...] utility
          // (body-compact), which would otherwise override the smaller
          // utility-xs size every audited call site's ancestor wrapper sets
          // (e.g. GenericToolCall.tsx's `font-mono
          // text-[length:var(--type-utility-xs-size)]` outer div) — font-size
          // does not cascade through an element that declares its own, so
          // this row re-declares the canonical tool-row size explicitly
          // rather than silently rendering larger than every other call site.
          'h-auto min-w-0 flex-1 justify-start gap-[var(--space-2)] rounded-none px-0 py-[var(--space-1)] text-left text-[length:var(--type-utility-xs-size)] font-[var(--font-weight-regular)]',
          expandable
            ? 'cursor-pointer hover:bg-[var(--color-surface-2)]/60'
            : 'cursor-default hover:bg-transparent',
          className,
        )}
        {...props}
      >
        {children}
        {expandable && !hideCaret && (
          <span className="ml-auto shrink-0 text-[var(--color-muted)]" aria-hidden="true">
            {expanded ? <CaretUp size={caretSize} /> : <CaretDown size={caretSize} />}
          </span>
        )}
      </Button>
    )
  },
)
DisclosureRow.displayName = 'DisclosureRow'

export { DisclosureRow }
