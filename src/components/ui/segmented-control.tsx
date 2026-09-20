import * as React from 'react'
import { Button, type ButtonProps } from './button'
import { cn } from '@/lib/utils'

/**
 * SegmentedControl — a `role="group"` cluster of independently-tabbable
 * toggle buttons (`aria-pressed`), for a single-select choice presented as a
 * flat button row: view-mode switches, preset pickers, period selectors.
 *
 * This is the WAI-ARIA "group of toggle buttons" pattern, NOT a radio group:
 * every button keeps its own normal Tab stop (no roving tabindex, no
 * arrow-key navigation) — see `./radio-group.tsx` for the roving-tabindex
 * exclusive-choice pattern used by e.g. AltitudeToggle. Every one of the 11
 * audited call sites for this shape (LibraryPdfPreview.tsx,
 * LibraryTextPreview.tsx, AuthMethodControl.tsx, ProviderDetailPanel.tsx,
 * RiskySettingControl.tsx, SsrfEditor.tsx, ToolPolicyEditor.tsx,
 * McpServerModal.tsx, Step1Identity.tsx, CalendarToolbar.tsx, UsageScreen.tsx)
 * already renders exactly this shape by hand: `role="group"` + per-button
 * `aria-pressed` + normal tab order — CalendarToolbar.tsx even carries an
 * explicit code comment explaining why `role="tablist"`/`role="tab"` would be
 * the WRONG pattern here (no roving tabindex or aria-controls is wired), so
 * this component preserves that distinction rather than "fixing" it.
 *
 * Built on the `Button` primitive, never a raw `<button>` — no
 * `@radix-ui/react-toggle-group` package is installed in this repo, and the
 * design-system `controls/raw-button` lock flags a literal `<button>` (or any
 * JSX tag statically aliased to the string `"button"`) unconditionally, with
 * no directory exemption. Routing every segment through `Button` (already a
 * catalogued `primitive`, `design-system/catalog.json`) keeps this file clean
 * under that scanner without needing a registered exception.
 */

interface SegmentedControlContextValue {
  value: string
  onValueChange: (value: string) => void
  disabled?: boolean
}

const SegmentedControlContext = React.createContext<SegmentedControlContextValue | null>(null)

function useSegmentedControlContext(component: string): SegmentedControlContextValue {
  const ctx = React.useContext(SegmentedControlContext)
  if (!ctx) throw new Error(`${component} must be rendered inside a <SegmentedControl>`)
  return ctx
}

// Same discriminated-union accessible-name contract as IconButton
// (./icon-button.tsx) — a `role="group"` cluster needs an accessible name
// exactly as much as an icon-only button does, and this shape statically
// forbids supplying both or neither.
type AccessibleGroupName =
  | { 'aria-label': string; 'aria-labelledby'?: never }
  | { 'aria-label'?: never; 'aria-labelledby': string }

export type SegmentedControlProps = Omit<React.HTMLAttributes<HTMLDivElement>, 'aria-label' | 'aria-labelledby'> &
  AccessibleGroupName & {
    /** The selected item's `value`. Controlled — there is no uncontrolled mode. */
    value: string
    onValueChange: (value: string) => void
    /** Disables every item unless a given `SegmentedControlItem` sets its own `disabled`. */
    disabled?: boolean
  }

const SegmentedControl = React.forwardRef<HTMLDivElement, SegmentedControlProps>(
  ({ value, onValueChange, disabled, className, children, ...props }, ref) => {
    const contextValue = React.useMemo(
      () => ({ value, onValueChange, disabled }),
      [value, onValueChange, disabled],
    )
    return (
      <SegmentedControlContext.Provider value={contextValue}>
        <div
          ref={ref}
          role="group"
          className={cn(
            'inline-flex items-center gap-[var(--space-0-5)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] p-[var(--space-0-5)]',
            className,
          )}
          {...props}
        >
          {children}
        </div>
      </SegmentedControlContext.Provider>
    )
  },
)
SegmentedControl.displayName = 'SegmentedControl'

export interface SegmentedControlItemProps
  extends Omit<ButtonProps, 'value' | 'variant' | 'aria-pressed' | 'role'> {
  value: string
}

const SegmentedControlItem = React.forwardRef<HTMLButtonElement, SegmentedControlItemProps>(
  ({ value, className, disabled, size = 'sm', onClick, children, ...props }, ref) => {
    const ctx = useSegmentedControlContext('SegmentedControlItem')
    const pressed = ctx.value === value
    const isDisabled = disabled ?? ctx.disabled
    return (
      <Button
        ref={ref}
        type="button"
        variant="ghost"
        size={size}
        aria-pressed={pressed}
        disabled={isDisabled}
        data-state={pressed ? 'on' : 'off'}
        onClick={(event) => {
          onClick?.(event)
          if (!event.defaultPrevented) ctx.onValueChange(value)
        }}
        className={cn(
          'h-7 min-w-0 rounded px-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-medium',
          pressed
            ? 'bg-[var(--color-surface-3)] text-[var(--color-accent)] shadow-sm hover:bg-[var(--color-surface-3)] hover:text-[var(--color-accent)]'
            : 'bg-transparent text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]',
          className,
        )}
        {...props}
      >
        {children}
      </Button>
    )
  },
)
SegmentedControlItem.displayName = 'SegmentedControlItem'

export { SegmentedControl, SegmentedControlItem }
