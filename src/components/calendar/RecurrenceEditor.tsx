import * as React from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { FormError } from '@/components/ui/FormError'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { DateTriggerButton } from '@/components/ui/date-picker'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { cn } from '@/lib/utils'
import {
  browserTimeZone,
  clampInterval,
  computePresets,
  defaultRecurrenceState,
  formatDateLabel,
  formatTimeOfDay,
  isMonthlyClampDay,
  matchPreset,
  ordinalWord,
  RECURRENCE_FREQUENCY_OPTIONS,
  startOfLocalDay,
  summarizeRecurrence,
  unitLabel,
  validateRecurrenceState,
  WEEKDAY_LABEL,
  WEEKDAY_ORDER,
  WEEKDAY_SHORT_LABEL,
  type RecurrenceEditorState,
  type RecurrenceFrequency,
  type RecurrenceValue,
  type WeekdayCode,
} from '@/lib/calendar/recurrence'

export type {
  RecurrenceEditorState,
  RecurrenceEndCondition,
  RecurrenceFrequency,
  RecurrenceValue,
  CompiledRecurrence,
  MonthlyMode,
  WeekdayCode,
} from '@/lib/calendar/recurrence'
export {
  compileRecurrence,
  parseRruleString,
  summarizeRecurrence,
  computePresets,
  defaultRecurrenceState,
  validateRecurrenceState,
} from '@/lib/calendar/recurrence'

/**
 * RecurrenceEditor — the Outlook/Google-style recurrence editor.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md
 * User Story 1; FR-002/003/004/006a/007/019/021.
 *
 * A self-contained, controlled leaf: all state lives in `value` (owned by the
 * caller — typically the calendar event slide-over), all RRULE build/parse/
 * summarize logic lives in `@/lib/calendar/recurrence` (import from there
 * directly, or via the convenience re-exports above). This component renders
 * ONLY the "Repeat" section of an event form — the Title/Agent/Date fields
 * are the caller's.
 *
 * Handoff contract for the slide-over:
 *   - Load: `parseRruleString(trigger.config.rrule, trigger.config.dtstart_ms)`
 *     → `RecurrenceEditorState | null`. Wrap in `{kind:'rrule', state}` for
 *     `value`, or `{kind:'none'}` when there's no stored rule (or it's a
 *     legacy cron_expr/every_ms trigger — US-5 offers a FRESH picker, so pass
 *     `{kind:'none'}` there too, never a translated rule).
 *   - Save: when `value.kind === 'none'`, create a `once` trigger instead
 *     (this component never emits an RRULE for "Does not repeat" — FR-005).
 *     When `value.kind === 'rrule'`, call
 *     `compileRecurrence(value.state, anchor, tz)` to get the
 *     `{rrule, dtstart_ms, tz}` triple to write into `trigger.config`.
 *   - FR-019 gating: wire `onValidityChange` to disable the panel's own Save
 *     button. The zero-weekday hint is ALSO rendered inline by this
 *     component (FormError under the weekday toggles) regardless of whether
 *     the caller wires the callback.
 *   - FR-024 (edit-mode anchor invariance): this component does not decide
 *     when to re-anchor. Passing the SAME `anchor`/`value.state` back
 *     through `compileRecurrence` on an untouched save reproduces the
 *     byte-identical `rrule`/`dtstart_ms`/`tz` triple (compileRecurrence is
 *     pure — same inputs, same output). It is the CALLER's job to keep
 *     `anchor` pinned to the stored `dtstart_ms` (not "now") on a
 *     title-only edit, and to only pass a freshly-computed anchor when the
 *     operator actually changed a recurrence/time field.
 */
export interface RecurrenceEditorProps {
  /** `{kind:'none'}` for "Does not repeat", or `{kind:'rrule', state}` for a configured rule. */
  value: RecurrenceValue
  onChange: (value: RecurrenceValue) => void
  /**
   * The date+time this rule starts from — drives the date-aware presets
   * (FR-002) and the compiled `dtstart_ms`. Should stay pinned to the
   * stored anchor across a title-only edit (see FR-024 note above).
   */
  anchor: Date
  /** IANA zone for the read-only time label (FR-021). Defaults to the browser's own zone. */
  tz?: string
  disabled?: boolean
  className?: string
  /** Fires whenever validity changes (FR-019) — wire to the panel's own Save button. */
  onValidityChange?: (valid: boolean) => void
}

function EndDatePicker({
  value,
  minDate,
  onChange,
  disabled,
}: {
  value: Date
  minDate: Date
  onChange: (date: Date) => void
  disabled?: boolean
}) {
  const [open, setOpen] = React.useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <DateTriggerButton hasValue disabled={disabled} aria-label="Ends on date">
          {formatDateLabel(value)}
        </DateTriggerButton>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <Calendar
          mode="single"
          selected={value}
          defaultMonth={value}
          // FR-019 / BDD "End date earlier than start is unreachable": dates
          // before the anchor are disabled AT THE PICKER, not corrected
          // after the fact.
          disabled={{ before: startOfLocalDay(minDate) }}
          onSelect={(d) => {
            if (d) {
              onChange(d)
              setOpen(false)
            }
          }}
          autoFocus
        />
      </PopoverContent>
    </Popover>
  )
}

export function RecurrenceEditor({
  value,
  onChange,
  anchor,
  tz,
  disabled,
  className,
  onValidityChange,
}: RecurrenceEditorProps) {
  const timeZone = tz ?? browserTimeZone()
  const presets = React.useMemo(() => computePresets(anchor), [anchor])
  const derivedId = React.useMemo(() => matchPreset(value, anchor), [value, anchor])
  const state: RecurrenceEditorState = value.kind === 'rrule' ? value.state : defaultRecurrenceState(anchor)

  // "Custom…" is a presentational mode that is NOT representable in `value`
  // (both a preset-matched rule and a hand-built rule are `{kind:'rrule'}`).
  // A freshly-seeded Custom rule that happens to equal a named preset would
  // otherwise make `matchPreset` snap the dropdown straight back to that preset
  // and hide the Custom controls — the create-flow reachability bug. Track an
  // explicit sticky flag: set when the operator picks "Custom…", cleared when
  // they pick another preset OR when a new `value` arrives from the caller (an
  // edit-mode load, detected by reference identity against our own last emit).
  const emittedRef = React.useRef<RecurrenceValue | null>(null)
  const [stickyCustom, setStickyCustom] = React.useState(false)
  if (value !== emittedRef.current && stickyCustom) {
    setStickyCustom(false)
  }
  const selectedId = stickyCustom ? 'custom' : derivedId

  function emit(next: RecurrenceValue) {
    emittedRef.current = next
    onChange(next)
  }

  const validity = React.useMemo(
    () => (value.kind === 'rrule' ? validateRecurrenceState(value.state) : { valid: true, reason: null }),
    [value],
  )

  React.useEffect(() => {
    onValidityChange?.(validity.valid)
    // onValidityChange is expected to be a stable callback (or intentionally
    // omitted); only the actual validity boolean should re-trigger this.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [validity.valid])

  function updateState(patch: Partial<RecurrenceEditorState>) {
    emit({ kind: 'rrule', state: { ...state, ...patch } })
  }

  function handlePresetChange(id: string) {
    if (id === 'custom') {
      // Enter Custom mode explicitly and keep it sticky, even if the seeded
      // state still equals a named preset — otherwise the dropdown snaps back
      // to that preset and hides the Custom controls. The panel opens
      // pre-filled with the current (possibly preset-matched) state.
      setStickyCustom(true)
      emit({ kind: 'rrule', state })
      return
    }
    setStickyCustom(false)
    if (id === 'none') {
      emit({ kind: 'none' })
      return
    }
    const preset = presets.find((p) => p.id === id)
    if (preset) emit(preset.value)
  }

  function toggleWeekday(code: WeekdayCode) {
    const has = state.weekdays.includes(code)
    const next = has ? state.weekdays.filter((d) => d !== code) : [...state.weekdays, code]
    updateState({ weekdays: next })
  }

  const summary = summarizeRecurrence(value, anchor)
  const timeLabel = `${formatTimeOfDay(anchor)} (${timeZone})`

  return (
    <div className={cn('flex flex-col gap-4', className)}>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="recurrence-preset">Repeat</Label>
        <Select value={selectedId} onValueChange={handlePresetChange} disabled={disabled}>
          <SelectTrigger id="recurrence-preset" aria-label="Repeat">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {presets.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {/* FR-021: read-only tz label beside the time — no tz picker in v1. */}
        <p className="text-xs text-[var(--color-muted)]" data-testid="recurrence-time-label">
          Time: {timeLabel}
        </p>
      </div>

      {selectedId === 'custom' && (
        <div className="flex flex-col gap-4 rounded-md border border-[var(--color-border)] p-3">
          <div className="flex items-center gap-2">
            <Label htmlFor="recurrence-interval" className="shrink-0">
              Repeat every
            </Label>
            <Input
              id="recurrence-interval"
              type="number"
              min={1}
              max={99}
              disabled={disabled}
              value={state.interval}
              onChange={(e) => updateState({ interval: clampInterval(Number(e.target.value)) })}
              className="w-16"
            />
            <Select
              value={state.frequency}
              onValueChange={(f) => updateState({ frequency: f as RecurrenceFrequency })}
              disabled={disabled}
            >
              <SelectTrigger aria-label="Frequency unit" className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {RECURRENCE_FREQUENCY_OPTIONS.map((f) => (
                  <SelectItem key={f} value={f}>
                    {unitLabel(f, state.interval)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {state.frequency === 'weekly' && (
            <div className="flex flex-col gap-1.5">
              <span className="text-sm text-[var(--color-secondary)]">On these days</span>
              <div className="flex gap-1" role="group" aria-label="Days of the week">
                {WEEKDAY_ORDER.map((code) => {
                  const selected = state.weekdays.includes(code)
                  return (
                    <Button
                      key={code}
                      type="button"
                      size="sm"
                      variant={selected ? 'default' : 'outline'}
                      aria-pressed={selected}
                      aria-label={WEEKDAY_LABEL[code]}
                      disabled={disabled}
                      onClick={() => toggleWeekday(code)}
                    >
                      {WEEKDAY_SHORT_LABEL[code]}
                    </Button>
                  )
                })}
              </div>
              <FormError error={state.weekdays.length === 0 ? 'Select at least one day.' : null} />
            </div>
          )}

          {state.frequency === 'monthly' && (
            <div className="flex flex-col gap-1.5">
              <span className="text-sm text-[var(--color-secondary)]">Repeats on</span>
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  size="sm"
                  disabled={disabled}
                  variant={state.monthlyMode === 'day-of-month' ? 'default' : 'outline'}
                  onClick={() => updateState({ monthlyMode: 'day-of-month' })}
                >
                  Day {state.monthlyDayOfMonth}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  disabled={disabled}
                  variant={state.monthlyMode === 'nth-weekday' ? 'default' : 'outline'}
                  onClick={() => updateState({ monthlyMode: 'nth-weekday' })}
                >
                  The {ordinalWord(state.monthlyOrdinal)} {WEEKDAY_LABEL[state.monthlyWeekday]}
                </Button>
              </div>
              {state.monthlyMode === 'day-of-month' && isMonthlyClampDay(state.monthlyDayOfMonth) && (
                <p className="text-xs text-[var(--color-muted)]">Moved to the last day in shorter months.</p>
              )}
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <span className="text-sm text-[var(--color-secondary)]">Ends</span>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                size="sm"
                disabled={disabled}
                variant={state.end.kind === 'never' ? 'default' : 'outline'}
                onClick={() => updateState({ end: { kind: 'never' } })}
              >
                Never
              </Button>
              <Button
                type="button"
                size="sm"
                disabled={disabled}
                variant={state.end.kind === 'on-date' ? 'default' : 'outline'}
                onClick={() =>
                  updateState({
                    end: state.end.kind === 'on-date' ? state.end : { kind: 'on-date', date: anchor },
                  })
                }
              >
                On date
              </Button>
              {state.end.kind === 'on-date' && (
                <EndDatePicker
                  value={state.end.date}
                  minDate={anchor}
                  disabled={disabled}
                  onChange={(d) => updateState({ end: { kind: 'on-date', date: d } })}
                />
              )}
              <Button
                type="button"
                size="sm"
                disabled={disabled}
                variant={state.end.kind === 'after-count' ? 'default' : 'outline'}
                onClick={() =>
                  updateState({
                    end: state.end.kind === 'after-count' ? state.end : { kind: 'after-count', count: 1 },
                  })
                }
              >
                After
              </Button>
              {state.end.kind === 'after-count' && (
                <div className="flex items-center gap-1.5">
                  <Input
                    type="number"
                    min={1}
                    aria-label="Number of occurrences"
                    disabled={disabled}
                    value={state.end.count}
                    onChange={(e) =>
                      updateState({
                        end: { kind: 'after-count', count: Math.max(1, Math.trunc(Number(e.target.value) || 1)) },
                      })
                    }
                    className="w-16"
                  />
                  <span className="text-sm text-[var(--color-muted)]">occurrences</span>
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* FR-004: live plain-English summary at all times. */}
      <p className="text-sm text-[var(--color-secondary)]" data-testid="recurrence-summary">
        {summary}
      </p>
    </div>
  )
}
RecurrenceEditor.displayName = 'RecurrenceEditor'
