import * as React from 'react'
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { DisclosureRow } from './disclosure-row'

// ListGroup — a collapsible group header plus its body (W2-1 contract §2.2,
// R35). Built on DisclosureRow so the toggle is a real Button with
// aria-expanded/aria-controls and the coarse-pointer growth DisclosureRow
// already carries. Groups start collapsed (R35); a collapsed body keeps its
// id (aria-controls must resolve) but carries `hidden` and does not mount
// its children. Actions render as a SIBLING of the toggle, never inside it
// (DisclosureRow's sibling-not-nested rule: no button inside a button).
export interface ListGroupProps {
  label: string
  count?: number
  /** Controlled open state; pair with onExpandedChange. */
  expanded?: boolean
  onExpandedChange?: (expanded: boolean) => void
  /** Initial open state when uncontrolled. R35: groups start collapsed. */
  defaultExpanded?: boolean
  actions?: React.ReactNode
  children: React.ReactNode
  id?: string
  className?: string
  'data-testid'?: string
}

export function ListGroup({
  label,
  count,
  expanded: expandedProp,
  onExpandedChange,
  defaultExpanded = false,
  actions,
  children,
  id,
  className,
  'data-testid': testId,
}: ListGroupProps) {
  const generatedId = React.useId()
  const baseId = id ?? generatedId
  const bodyId = `${baseId}-body`
  const [uncontrolledExpanded, setUncontrolledExpanded] = React.useState(defaultExpanded)
  const controlled = expandedProp !== undefined
  const expanded = controlled ? expandedProp : uncontrolledExpanded

  function handleExpandedChange(next: boolean) {
    if (!controlled) setUncontrolledExpanded(next)
    onExpandedChange?.(next)
  }

  const Caret = expanded ? CaretDown : CaretRight
  return (
    <section data-slot="list-group" data-testid={testId} className={className}>
      <div className="flex items-center gap-[var(--space-2)]">
        <DisclosureRow
          expanded={expanded}
          onExpandedChange={handleExpandedChange}
          expandable
          hideCaret
          aria-controls={bodyId}
        >
          <Caret aria-hidden="true" className="size-[var(--icon-size-compact)] shrink-0 text-[var(--color-muted)]" />
          <span className="text-[length:var(--type-label-size)] font-semibold uppercase tracking-[var(--type-label-letter-spacing)] text-[var(--color-secondary)]">
            {label}
          </span>
          {/* An empty group still renders (hiding it is the caller's choice)
              but shows no "0": the name stays the bare label, as the RED
              test pins for count === 0. */}
          {count !== undefined && count > 0 ? (
            <>
              {/* Literal whitespace: flex gap is visual only, and the accessible
                  name must read "Telegram 3", not "Telegram3". */}
              {' '}
              <span className="text-[length:var(--type-label-size)] font-[var(--font-weight-regular)] normal-case tracking-[var(--font-letter-spacing-normal)] text-[var(--color-muted)]">{count}</span>
            </>
          ) : null}
        </DisclosureRow>
        {actions != null ? <div className="flex shrink-0 items-center gap-[var(--space-1)]">{actions}</div> : null}
      </div>
      <div id={bodyId} hidden={!expanded}>
        {expanded ? children : null}
      </div>
    </section>
  )
}
