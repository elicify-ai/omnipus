import { DayPicker } from 'react-day-picker'
import type { DayPickerProps } from 'react-day-picker'
import { CaretLeft, CaretRight } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

// Calendar — Sovereign Deep themed wrapper around react-day-picker v9.
//
// Fully Tailwind-themed (no react-day-picker/style.css import, no companion
// CSS file): react-day-picker merges any classNames we pass over its own
// `rdp-*` defaults (unstyled without the stock stylesheet), so every visual
// rule lives here as utility classes. Selection/today/outside/disabled state
// is exposed by react-day-picker as `data-*` attributes on the day <td>
// (`data-selected`, `data-today`, `data-outside`, `data-disabled`) — the day
// <td> is marked `group` so the inner day <button> can react to those via
// Tailwind's `group-data-[...]` variant instead of a second stylesheet.
export type CalendarProps = DayPickerProps

const DAY_BASE =
  'inline-flex h-9 w-9 [@media(pointer:coarse)]:h-[44px] [@media(pointer:coarse)]:w-[44px] items-center justify-center rounded-md p-0 text-sm font-normal font-inter ' +
  'text-[var(--color-secondary)] transition-colors motion-reduce:transition-none ' +
  'hover:bg-[var(--color-surface-2)] ' +
  ' ' +
  'disabled:pointer-events-none disabled:opacity-40 aria-disabled:pointer-events-none aria-disabled:opacity-40 ' +
  'group-data-[today=true]:font-semibold group-data-[today=true]:text-[var(--color-accent)] group-data-[today=true]:forced-colors:text-[CanvasText] ' +
  'group-data-[outside=true]:text-[var(--color-muted)] ' +
  'group-data-[disabled=true]:text-[var(--color-muted)] group-data-[disabled=true]:opacity-40 ' +
  'group-data-[selected=true]:bg-[var(--color-accent)] group-data-[selected=true]:text-[var(--color-primary)] ' +
  'group-data-[selected=true]:forced-colors:border-2 group-data-[selected=true]:forced-colors:border-[Highlight] group-data-[selected=true]:forced-colors:bg-[Canvas] group-data-[selected=true]:forced-colors:text-[CanvasText] ' +
  'group-data-[selected=true]:font-semibold group-data-[selected=true]:hover:bg-[var(--color-accent-hover)] ' +
  'group-data-[selected=true]:hover:text-[var(--color-primary)]'

const NAV_BUTTON =
  'inline-flex h-7 w-7 items-center justify-center rounded-md border border-[var(--color-border)] ' +
  'bg-transparent p-0 text-[var(--color-secondary)] opacity-80 transition-colors ' +
  'hover:opacity-100 hover:bg-[var(--color-surface-2)] ' +
  ' ' +
  'aria-disabled:pointer-events-none aria-disabled:opacity-30'

function Calendar({ className, classNames, showOutsideDays = true, onDayFocus, ...props }: CalendarProps) {
  return (
    <DayPicker
      showOutsideDays={showOutsideDays}
      data-calendar-viewport="true"
      className={cn(
        'box-border max-w-full overflow-x-auto overscroll-x-contain p-3 font-inter [@media(pointer:coarse)]:p-0',
        className,
      )}
      classNames={{
        months: 'relative flex w-max min-w-full flex-col gap-4 [@media(pointer:coarse)]:min-w-[calc(var(--target-touch-minimum)*7)]',
        month: 'space-y-1',
        nav: 'absolute inset-x-1 top-1 flex items-center justify-between z-10',
        button_previous: NAV_BUTTON,
        button_next: NAV_BUTTON,
        chevron: 'h-3.5 w-3.5',
        month_caption: 'flex h-9 items-center justify-center',
        caption_label: 'text-sm font-medium font-outfit text-[var(--color-secondary)]',
        month_grid: 'w-full border-collapse mt-1',
        weekdays: 'flex',
        weekday: 'w-9 [@media(pointer:coarse)]:w-[44px] text-center text-xs font-normal font-inter text-[var(--color-muted)]',
        weeks: '',
        week: 'flex w-full mt-1',
        day: 'group relative h-9 w-9 [@media(pointer:coarse)]:h-[44px] [@media(pointer:coarse)]:w-[44px] p-0 text-center text-sm focus-within:relative focus-within:z-20',
        day_button: DAY_BASE,
        ...classNames,
      }}
      components={{
        Chevron: ({ orientation, className: chevronClassName }) =>
          orientation === 'right' ? (
            <CaretRight weight="bold" className={chevronClassName} />
          ) : (
            <CaretLeft weight="bold" className={chevronClassName} />
          ),
      }}
      onDayFocus={(date, modifiers, event) => {
        const day = event.currentTarget as HTMLElement
        const viewport = day.closest<HTMLElement>('[data-calendar-viewport]')
        if (viewport) {
          const viewportRect = viewport.getBoundingClientRect()
          const dayRect = day.getBoundingClientRect()
          if (dayRect.left < viewportRect.left) viewport.scrollLeft += dayRect.left - viewportRect.left
          else if (dayRect.right > viewportRect.right) viewport.scrollLeft += dayRect.right - viewportRect.right
        }
        onDayFocus?.(date, modifiers, event)
      }}
      {...props}
    />
  )
}
Calendar.displayName = 'Calendar'

export { Calendar }
