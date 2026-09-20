import * as React from 'react'
import { CalendarBlank } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

// Date-only picker — replaces native `<input type="date">` (ADR-030 §10).
//
// The trigger preserves the established date-control geometry. It shares the
// Input height, border, background, padding, and typography while retaining
// the existing subtle shadow and the shared Button focus treatment.
export const DATE_TRIGGER_CLASSNAME = cn(
  'flex h-11 sm:h-9 w-full items-center gap-2 rounded-md border border-[var(--color-border)]',
  'bg-[var(--color-surface-1)] px-3 py-1 text-sm text-[var(--color-secondary)] shadow-sm transition-colors',
  'justify-start text-left font-[var(--font-weight-regular)] whitespace-nowrap',
  'hover:bg-[var(--color-surface-1)]',
  'disabled:cursor-not-allowed disabled:opacity-50',
  'data-[readonly=true]:cursor-default data-[readonly=true]:!opacity-100',
)

// Shared trigger button for DatePicker + DateTimePicker — both pickers open
// their Popover from the established icon + truncated-label Button treatment
// (DATE_TRIGGER_CLASSNAME). Extracted here so the block isn't
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
      <span className={cn('truncate', !hasValue ? 'text-[var(--color-muted)]' : undefined)}>
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
  'aria-describedby'?: string
  'aria-invalid'?: React.AriaAttributes['aria-invalid']
  required?: boolean
  disabled?: boolean
  readOnly?: boolean
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
    'aria-describedby': ariaDescribedBy,
    'aria-invalid': ariaInvalid,
    required = false,
    disabled,
    readOnly = false,
    className,
  },
  ref,
) {
  const [open, setOpen] = React.useState(false)
  const generatedRequiredId = React.useId()
  const requiredDescriptionId = `${id ?? generatedRequiredId}-required`
  const describedBy = [ariaDescribedBy, required ? requiredDescriptionId : undefined].filter(Boolean).join(' ') || undefined
  const blocked = Boolean(disabled || readOnly)
  const blockedRef = React.useRef(blocked)
  blockedRef.current = blocked
  if (value !== null && !Number.isFinite(value.getTime())) {
    throw new RangeError('value must be a valid Date or null')
  }
  React.useEffect(() => {
    if (blocked) setOpen(false)
  }, [blocked])
  const effectiveOpen = open && !blocked

  return (
    <Popover open={effectiveOpen} onOpenChange={(nextOpen) => {
      if (blockedRef.current) setOpen(false)
      else setOpen(nextOpen)
    }}>
      <PopoverTrigger asChild>
        <DateTriggerButton
          ref={ref}
          id={id}
          aria-label={ariaLabel}
          aria-describedby={describedBy}
          aria-invalid={ariaInvalid}
          disabled={disabled}
          aria-disabled={readOnly || undefined}
          data-readonly={readOnly || undefined}
          className={className}
          hasValue={!!value}
        >
          {value ? formatDateDisplay(value) : placeholder}
        </DateTriggerButton>
      </PopoverTrigger>
      {required && <span id={requiredDescriptionId} className="sr-only">Required</span>}
      <PopoverContent
        aria-label={`${ariaLabel ?? placeholder} options`}
        className="w-auto max-w-[calc(100vw-var(--space-3))] p-0"
        align="start"
      >
        <Calendar
          mode="single"
          selected={value ?? undefined}
          defaultMonth={value ?? undefined}
          onSelect={(date) => {
            if (blockedRef.current) return
            onChange(date ?? null)
            setOpen(false)
          }}
          disabled={blocked}
          autoFocus
        />
      </PopoverContent>
    </Popover>
  )
})
DatePicker.displayName = 'DatePicker'

export { DatePicker }
