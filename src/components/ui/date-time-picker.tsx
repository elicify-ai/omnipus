import * as React from 'react'
import { CalendarBlank, Clock } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { DATE_TRIGGER_CLASSNAME } from '@/components/ui/date-picker'
import { cn } from '@/lib/utils'

// Date + time picker — replaces native `<input type="datetime-local">`
// (ADR-030 §10). Same Input-matched trigger as DatePicker; the popover adds
// two shadcn <Select> dropdowns (hour 00–23, minute 00–59 by `minuteStep`)
// below the calendar instead of a native `<input type="time">`.
export interface DateTimePickerProps {
  value: Date | null
  onChange: (date: Date | null) => void
  placeholder?: string
  id?: string
  'aria-label'?: string
  disabled?: boolean
  className?: string
  /** Minute increment offered in the minute select. Defaults to 5. */
  minuteStep?: number
}

function pad2(n: number): string {
  return n.toString().padStart(2, '0')
}

const HOURS: string[] = Array.from({ length: 24 }, (_, h) => pad2(h))

function minuteOptions(step: number, current: number | null): string[] {
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

function DateTimePicker({
  value,
  onChange,
  placeholder = 'Pick a date and time',
  id,
  'aria-label': ariaLabel,
  disabled,
  className,
  minuteStep = 5,
}: DateTimePickerProps) {
  const [open, setOpen] = React.useState(false)
  const minutes = React.useMemo(
    () => minuteOptions(minuteStep, value ? value.getMinutes() : null),
    [minuteStep, value],
  )

  function handleDaySelect(day: Date | undefined) {
    if (!day) {
      onChange(null)
      return
    }
    const now = new Date()
    const hours = value ? value.getHours() : now.getHours()
    const mins = value ? value.getMinutes() : now.getMinutes()
    onChange(new Date(day.getFullYear(), day.getMonth(), day.getDate(), hours, mins, 0, 0))
  }

  function handleHourChange(hourStr: string) {
    const hour = parseInt(hourStr, 10)
    const base = value ?? new Date()
    onChange(new Date(base.getFullYear(), base.getMonth(), base.getDate(), hour, base.getMinutes(), 0, 0))
  }

  function handleMinuteChange(minuteStr: string) {
    const minute = parseInt(minuteStr, 10)
    const base = value ?? new Date()
    onChange(new Date(base.getFullYear(), base.getMonth(), base.getDate(), base.getHours(), minute, 0, 0))
  }

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
            {value ? formatDateTimeDisplay(value) : placeholder}
          </span>
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <Calendar
          mode="single"
          selected={value ?? undefined}
          defaultMonth={value ?? undefined}
          onSelect={handleDaySelect}
          autoFocus
        />
        <div className="flex items-center gap-2 border-t border-[var(--color-border)] p-3">
          <Clock size={14} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
          <Select value={value ? pad2(value.getHours()) : undefined} onValueChange={handleHourChange}>
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
          <Select value={value ? pad2(value.getMinutes()) : undefined} onValueChange={handleMinuteChange}>
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
          <Button type="button" size="sm" onClick={() => setOpen(false)} disabled={!value}>
            Done
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
DateTimePicker.displayName = 'DateTimePicker'

export { DateTimePicker }
