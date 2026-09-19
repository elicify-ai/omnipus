import * as React from 'react'
import { Clock } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { DateTriggerButton } from '@/components/ui/date-picker'

// Date + time picker — replaces native `<input type="datetime-local">`
// (ADR-030 §10). Same Input-matched trigger as DatePicker (shared via
// DateTriggerButton); the popover adds two shadcn <Select> dropdowns (hour
// 00–23, minute 00–59 by `minuteStep`) below the calendar instead of a
// native `<input type="time">`.
export interface DateTimePickerProps {
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
  /** Minute increment offered in the minute select. Defaults to 5. */
  minuteStep?: number
}

function pad2(n: number): string {
  return n.toString().padStart(2, '0')
}

const HOURS: string[] = Array.from({ length: 24 }, (_, h) => pad2(h))

function minuteOptions(step: number, current: number | null): string[] {
  if (!Number.isFinite(step) || !Number.isInteger(step) || step < 1 || step > 60) {
    throw new RangeError('minuteStep must be a finite integer from 1 through 60')
  }
  const out: number[] = []
  for (let m = 0; m < 60; m += step) out.push(m)
  // Preserve an existing minute that doesn't land on the step grid (e.g. a
  // value produced before this picker existed, or by another client) instead
  // of silently rounding it away.
  if (current != null && !out.includes(current)) {
    out.push(current)
    out.sort((a, b) => a - b)
  }
  return out.map(pad2)
}

function formatDateTimeDisplay(date: Date): string {
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

const DateTimePicker = React.forwardRef<HTMLButtonElement, DateTimePickerProps>(function DateTimePicker(
  {
    value,
    onChange,
    placeholder = 'Pick a date and time',
    id,
    'aria-label': ariaLabel,
    'aria-describedby': ariaDescribedBy,
    'aria-invalid': ariaInvalid,
    required = false,
    disabled,
    readOnly = false,
    className,
    minuteStep = 5,
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
  const minutes = React.useMemo(
    () => minuteOptions(minuteStep, value ? value.getMinutes() : null),
    [minuteStep, value],
  )

  function handleDaySelect(day: Date | undefined) {
    if (blockedRef.current) return
    if (!day) {
      onChange(null)
      return
    }
    // A day-only pick with no prior value defaults the time-of-day to
    // midnight (00:00) — deterministic, not "now". A due date or a one-time
    // trigger picked by day alone should mean "the start of that day", not
    // whatever wall-clock time happened to be showing when the user clicked.
    const hours = value ? value.getHours() : 0
    const mins = value ? value.getMinutes() : 0
    onChange(new Date(day.getFullYear(), day.getMonth(), day.getDate(), hours, mins, 0, 0))
  }

  function handleHourChange(hourStr: string) {
    if (blockedRef.current) return
    const hour = parseInt(hourStr, 10)
    const base = value ?? new Date()
    onChange(new Date(base.getFullYear(), base.getMonth(), base.getDate(), hour, base.getMinutes(), 0, 0))
  }

  function handleMinuteChange(minuteStr: string) {
    if (blockedRef.current) return
    const minute = parseInt(minuteStr, 10)
    const base = value ?? new Date()
    onChange(new Date(base.getFullYear(), base.getMonth(), base.getDate(), base.getHours(), minute, 0, 0))
  }

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
          {value ? formatDateTimeDisplay(value) : placeholder}
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
          onSelect={handleDaySelect}
          disabled={blocked}
          autoFocus
        />
        <div className="flex items-center gap-2 border-t border-[var(--color-border)] p-3">
          <Clock size={14} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
          <Select value={value ? pad2(value.getHours()) : undefined} onValueChange={handleHourChange} disabled={blocked}>
            <SelectTrigger aria-label="Hour" className="h-8 w-[4.5rem] text-xs">
              <SelectValue placeholder="HH" />
            </SelectTrigger>
            <SelectContent>
              {HOURS.map((h) => (
                <SelectItem key={h} value={h} className="text-xs">
                  {h}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-[var(--color-muted)]">:</span>
          <Select value={value ? pad2(value.getMinutes()) : undefined} onValueChange={handleMinuteChange} disabled={blocked}>
            <SelectTrigger aria-label="Minute" className="h-8 w-[4.5rem] text-xs">
              <SelectValue placeholder="MM" />
            </SelectTrigger>
            <SelectContent>
              {minutes.map((m) => (
                <SelectItem key={m} value={m} className="text-xs">
                  {m}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex-1" />
          <Button type="button" size="sm" onClick={() => setOpen(false)} disabled={!value || blocked}>
            Done
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
})
DateTimePicker.displayName = 'DateTimePicker'

export { DateTimePicker }
