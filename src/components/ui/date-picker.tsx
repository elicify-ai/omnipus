import * as React from 'react'
import { CalendarBlank } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

// Date-only picker — replaces native `<input type="date">` (ADR-030 §10).
//
// The trigger is a <Button> styled to be pixel-identical to <Input> (same
// height/border/background/padding/focus-ring — see DATE_TRIGGER_CLASSNAME)
// so a DatePicker sitting next to a plain <Input> in a form is visually
// indistinguishable as a "box", while still being a real button that opens
// a themed react-day-picker calendar in a Popover.
export const DATE_TRIGGER_CLASSNAME = cn(
  'flex h-11 sm:h-9 w-full items-center gap-2 rounded-md border border-[var(--color-border)]',
  'bg-[var(--color-surface-1)] px-3 py-1 text-sm text-[var(--color-secondary)] shadow-sm transition-colors',
  'justify-start text-left font-normal whitespace-nowrap',
  'hover:bg-[var(--color-surface-1)]',
  // ring-offset-0 keeps the focus ring flush against the border — <Button>'s base
  // variant sets ring-offset-2 (an offset gap meant for buttons sitting on a flat
  // background), which tailwind-merge does not strip; without the override this
  // trigger's focus ring floats off the border instead of matching <Input> pixel-for-pixel.
  ' focus-visible:border-[var(--color-accent)]',
  'disabled:cursor-not-allowed disabled:opacity-50',
)

// Shared trigger button for DatePicker + DateTimePicker — both pickers open
// their Popover from an identical icon + truncated-label <Button> styled to
// match <Input> (DATE_TRIGGER_CLASSNAME). Extracted here so the block isn't
// duplicated between the two picker files.
// Extends the native button attributes (rather than a fixed id/aria-label/
// disabled list) so that Radix's `PopoverTrigger asChild` — which clones this
// element and injects its own onClick/onPointerDown/aria-expanded/data-state
// props to wire up the popover open/close — passes straight through via
// `...rest`. A fixed prop allowlist would silently drop those injected
// handlers and the trigger would stop opening the popover.
export interface DateTriggerButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  /** Whether a value is currently selected — drives the muted placeholder styling. */
  hasValue: boolean
}

export const DateTriggerButton = React.forwardRef<HTMLButtonElement, DateTriggerButtonProps>(
  ({ className, hasValue, children, ...rest }, ref) => (
    <Button
      ref={ref}
      type="button"
      variant="outline"
      className={cn(DATE_TRIGGER_CLASSNAME, className)}
      {...rest}
    >
      <CalendarBlank size={16} className="shrink-0 opacity-70" aria-hidden="true" />
      <span className={cn('truncate', !hasValue && 'text-[var(--color-muted)]')}>
        {children}
      </span>
    </Button>
  ),
)
DateTriggerButton.displayName = 'DateTriggerButton'

export interface DatePickerProps {
  value: Date | null
  onChange: (date: Date | null) => void
  placeholder?: string
  id?: string
  'aria-label'?: string
  disabled?: boolean
  className?: string
}

function formatDateDisplay(date: Date): string {
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

const DatePicker = React.forwardRef<HTMLButtonElement, DatePickerProps>(function DatePicker(
  {
    value,
    onChange,
    placeholder = 'Pick a date',
    id,
    'aria-label': ariaLabel,
    disabled,
    className,
  },
  ref,
) {
  const [open, setOpen] = React.useState(false)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <DateTriggerButton
          ref={ref}
          id={id}
          aria-label={ariaLabel}
          disabled={disabled}
          className={className}
          hasValue={!!value}
        >
          {value ? formatDateDisplay(value) : placeholder}
        </DateTriggerButton>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <Calendar
          mode="single"
          selected={value ?? undefined}
          defaultMonth={value ?? undefined}
          onSelect={(date) => {
            onChange(date ?? null)
            setOpen(false)
          }}
          autoFocus
        />
      </PopoverContent>
    </Popover>
  )
})
DatePicker.displayName = 'DatePicker'

export { DatePicker }
