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
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:border-[var(--color-accent)]',
  'disabled:cursor-not-allowed disabled:opacity-50',
)

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

function DatePicker({
  value,
  onChange,
  placeholder = 'Pick a date',
  id,
  'aria-label': ariaLabel,
  disabled,
  className,
}: DatePickerProps) {
  const [open, setOpen] = React.useState(false)

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          id={id}
          aria-label={ariaLabel}
          variant="outline"
          disabled={disabled}
          className={cn(DATE_TRIGGER_CLASSNAME, className)}
        >
          <CalendarBlank size={16} className="shrink-0 opacity-70" aria-hidden="true" />
          <span className={cn('truncate', !value && 'text-[var(--color-muted)]')}>
            {value ? formatDateDisplay(value) : placeholder}
          </span>
        </Button>
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
}
DatePicker.displayName = 'DatePicker'

export { DatePicker }
