import * as React from 'react'
import { Button, type ButtonProps } from './button'
import { cn } from '@/lib/utils'

/**
 * RadioGroup — the WAI-ARIA APG "radio group" pattern: `role="radiogroup"`
 * wrapping `role="radio"` children, roving tabindex (exactly one option —
 * the checked one — is a Tab stop) and arrow-key navigation that moves focus
 * AND immediately selects the adjacent option, matching how a native
 * `<input type="radio">` group behaves. Home/End jump to the first/last
 * enabled option. Arrows wrap past the ends by default; `loop={false}` stops
 * them at the first and last option instead (a group where passing through
 * an option on the way round would save it, e.g. the Skills & Tools trust
 * block, ruling 2026-11-26).
 *
 * Four real call sites drove this API, in two shapes:
 *   - AltitudeToggle.tsx and WorkspaceTasksTab.tsx's `ViewSwitcher` already
 *     hand-roll exactly this model (roving tabindex; ArrowRight/ArrowDown to
 *     the next option, ArrowLeft/ArrowUp to the previous, both wrapping) —
 *     this component generalizes that implementation so both can be deleted
 *     in favor of it.
 *   - PromptGuardSection.tsx (and the retired SkillTrustSection.tsx) render full-width card
 *     rows (`role="radio"`, `aria-checked`) with every option at a fixed
 *     `tabIndex={0}` and NO arrow-key handling at all — a pre-existing
 *     accessibility gap. Using this component there is a real accessibility
 *     fix (roving tabindex + arrow keys + Home/End land for the first time),
 *     not just a visual-only swap.
 *
 * Built on the `Button` primitive, never a raw `<button>` — no
 * `@radix-ui/react-radio-group` package is installed in this repo, and the
 * design-system `controls/raw-button` lock flags a literal `<button>` (or any
 * JSX tag statically aliased to the string `"button"`) unconditionally, with
 * no directory exemption. Routing every option through `Button` (already a
 * catalogued `primitive`, `design-system/catalog.json`) keeps this file clean
 * under that scanner without needing a registered exception.
 */

interface RadioGroupContextValue {
  value: string
  onValueChange: (value: string) => void
  disabled?: boolean
  /** Whether arrow keys wrap past the first and last option. */
  loop: boolean
  /** The value that should carry the group's one Tab stop (WAI-ARIA APG
   *  roving tabindex): normally the checked value, but when `value` matches
   *  no rendered item — a group whose initial/external value hasn't landed
   *  on an option yet — the checked-item fallback leaves the WHOLE group
   *  unreachable by keyboard (every item at tabIndex -1). Falls back to the
   *  first enabled item's value so the group is always reachable. */
  activeTabValue: string | undefined
}

const RadioGroupContext = React.createContext<RadioGroupContextValue | null>(null)

function useRadioGroupContext(component: string): RadioGroupContextValue {
  const ctx = React.useContext(RadioGroupContext)
  if (!ctx) throw new Error(`${component} must be rendered inside a <RadioGroup>`)
  return ctx
}

// Same discriminated-union accessible-name contract as IconButton
// (./icon-button.tsx) and SegmentedControl (./segmented-control.tsx) — every
// audited call site names its radiogroup via aria-label ("Board depth",
// "Task view", "Prompt injection defense level", "Skill trust level").
type AccessibleGroupName =
  | { 'aria-label': string; 'aria-labelledby'?: never }
  | { 'aria-label'?: never; 'aria-labelledby': string }

export type RadioGroupProps = Omit<
  React.HTMLAttributes<HTMLDivElement>,
  'aria-label' | 'aria-labelledby' | 'onChange'
> &
  AccessibleGroupName & {
    /** The checked item's `value`. Controlled — there is no uncontrolled mode. */
    value: string
    onValueChange: (value: string) => void
    /** Disables every item unless a given `RadioGroupItem` sets its own `disabled`. */
    disabled?: boolean
    /**
     * Layout only — arrow-key navigation always honors both axes
     * (ArrowRight/ArrowDown move forward, ArrowLeft/ArrowUp move back),
     * matching AltitudeToggle's existing behavior regardless of orientation.
     * Sets `aria-orientation` for assistive technology. Default: `'horizontal'`.
     */
    orientation?: 'horizontal' | 'vertical'
    /**
     * Whether ArrowRight/ArrowDown on the last option and ArrowLeft/ArrowUp on
     * the first wrap round. With `false` those keys do nothing there: no focus
     * move and no `onValueChange`. Home and End are unchanged. Default: `true`.
     */
    loop?: boolean
  }

const RadioGroup = React.forwardRef<HTMLDivElement, RadioGroupProps>(
  ({ value, onValueChange, disabled, orientation = 'horizontal', loop = true, className, children, ...props }, ref) => {
    // Computed from the rendered items themselves (their own `value`/
    // `disabled` props), the same source the keyboard navigation already
    // reads via the DOM (`RADIO_SELECTOR`) — see the interface doc above.
    const activeTabValue = React.useMemo(() => {
      let matched = false
      let firstEnabled: string | undefined
      React.Children.forEach(children, (child) => {
        if (!React.isValidElement(child)) return
        const itemProps = child.props as { value?: string; disabled?: boolean }
        if (itemProps.value === undefined) return
        if (itemProps.value === value) matched = true
        if (firstEnabled === undefined && !(itemProps.disabled ?? disabled)) {
          firstEnabled = itemProps.value
        }
      })
      return matched ? value : firstEnabled
    }, [children, value, disabled])
    const contextValue = React.useMemo(
      () => ({ value, onValueChange, disabled, loop, activeTabValue }),
      [value, onValueChange, disabled, loop, activeTabValue],
    )
    return (
      <RadioGroupContext.Provider value={contextValue}>
        <div
          ref={ref}
          role="radiogroup"
          aria-orientation={orientation}
          className={cn(
            orientation === 'vertical'
              ? 'flex flex-col gap-[var(--space-2)]'
              : 'flex items-center gap-[var(--space-0-5)]',
            className,
          )}
          {...props}
        >
          {children}
        </div>
      </RadioGroupContext.Provider>
    )
  },
)
RadioGroup.displayName = 'RadioGroup'

// Only enabled radios participate in roving-tabindex navigation — a disabled
// option is skipped exactly like a disabled native <input type="radio">.
const RADIO_SELECTOR = '[role="radio"]:not([disabled]):not([aria-disabled="true"])'

export interface RadioGroupItemProps
  extends Omit<ButtonProps, 'value' | 'variant' | 'role' | 'aria-checked' | 'tabIndex'> {
  value: string
}

const RadioGroupItem = React.forwardRef<HTMLButtonElement, RadioGroupItemProps>(
  ({ value, className, disabled, onClick, onKeyDown, children, ...props }, ref) => {
    const ctx = useRadioGroupContext('RadioGroupItem')
    const checked = ctx.value === value
    const isDisabled = disabled ?? ctx.disabled

    function selectAndFocus(target: HTMLButtonElement) {
      const nextValue = target.dataset.radioValue
      target.focus()
      if (nextValue !== undefined) ctx.onValueChange(nextValue)
    }

    function handleKeyDown(event: React.KeyboardEvent<HTMLButtonElement>) {
      onKeyDown?.(event)
      if (event.defaultPrevented) return
      const group = event.currentTarget.closest('[role="radiogroup"]')
      if (!group) return
      const options = Array.from(group.querySelectorAll<HTMLButtonElement>(RADIO_SELECTOR))
      const currentIndex = options.indexOf(event.currentTarget)
      if (currentIndex === -1 || options.length === 0) return

      const step = (offset: 1 | -1) => {
        event.preventDefault()
        const next = currentIndex + offset
        // Without loop an arrow at either end stays put: no focus move, no selection.
        if (!ctx.loop && (next < 0 || next >= options.length)) return
        selectAndFocus(options[(next + options.length) % options.length])
      }

      switch (event.key) {
        case 'ArrowRight':
        case 'ArrowDown':
          step(1)
          break
        case 'ArrowLeft':
        case 'ArrowUp':
          step(-1)
          break
        case 'Home':
          event.preventDefault()
          selectAndFocus(options[0])
          break
        case 'End':
          event.preventDefault()
          selectAndFocus(options[options.length - 1])
          break
        default:
          break
      }
    }

    return (
      <Button
        ref={ref}
        type="button"
        variant="ghost"
        role="radio"
        aria-checked={checked}
        tabIndex={value === ctx.activeTabValue ? 0 : -1}
        disabled={isDisabled}
        data-radio-value={value}
        data-state={checked ? 'checked' : 'unchecked'}
        onClick={(event) => {
          onClick?.(event)
          if (!event.defaultPrevented) ctx.onValueChange(value)
        }}
        onKeyDown={handleKeyDown}
        className={cn(
          'h-auto w-full justify-start rounded-md border border-[var(--color-border)] p-[var(--space-2-5)] text-left font-medium',
          checked
            ? 'border-[var(--color-accent)]/60 bg-[var(--color-accent)]/8 text-[var(--color-secondary)] hover:bg-[var(--color-accent)]/8'
            : 'bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:border-[var(--color-accent)]/50 hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
          className,
        )}
        {...props}
      >
        {children}
      </Button>
    )
  },
)
RadioGroupItem.displayName = 'RadioGroupItem'

export { RadioGroup, RadioGroupItem }
