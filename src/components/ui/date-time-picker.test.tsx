/**
 * date-time-picker.test.tsx — date + time picker replacing native
 * `<input type="datetime-local">` (ADR-030 §10).
 *
 * Radix Popover renders inline in jsdom. The hour/minute <Select>s use Radix
 * Select, which needs `hasPointerCapture` / `scrollIntoView` / `ResizeObserver`
 * polyfills to open in jsdom (same gap noted in
 * model-selector.test.tsx) — verified working
 * here with `fireEvent.click` (trigger) + `fireEvent.pointerDown` +
 * `fireEvent.click` (item).
 */

import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { DateTimePicker } from './date-time-picker'

beforeAll(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
})

function clickDay(isoDate: string) {
  const btn = document.querySelector(`[data-day="${isoDate}"] button`)
  if (!btn) throw new Error(`no day button for ${isoDate}`)
  fireEvent.click(btn)
}

function selectOption(comboboxName: string, optionName: string) {
  fireEvent.click(screen.getByRole('combobox', { name: comboboxName }))
  const option = screen.getByRole('option', { name: optionName })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

describe('DateTimePicker — trigger render', () => {
  it('shows the placeholder when value is null', () => {
    render(
      <DateTimePicker value={null} onChange={vi.fn()} placeholder="Pick a date and time" aria-label="Trigger at" />,
    )
    expect(screen.getByRole('button', { name: 'Trigger at' })).toHaveTextContent('Pick a date and time')
  })

  it('shows the formatted date + time when value is set', () => {
    render(<DateTimePicker value={new Date(2026, 5, 22, 14, 30)} onChange={vi.fn()} aria-label="Trigger at" />)
    const trigger = screen.getByRole('button', { name: 'Trigger at' })
    expect(trigger).toHaveTextContent('Jun 22, 2026')
    expect(trigger).toHaveTextContent('02:30 PM')
  })

  it('is disabled when disabled=true', () => {
    render(<DateTimePicker value={null} onChange={vi.fn()} disabled aria-label="Trigger at" />)
    expect(screen.getByRole('button', { name: 'Trigger at' })).toBeDisabled()
  })
})

describe('DateTimePicker — day selection', () => {
  it('picking a day preserves the existing time-of-day', () => {
    const onChange = vi.fn()
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />)

    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    clickDay('2026-06-20')

    expect(onChange).toHaveBeenCalledOnce()
    const [next] = onChange.mock.calls[0] as [Date]
    expect(next.getFullYear()).toBe(2026)
    expect(next.getMonth()).toBe(5)
    expect(next.getDate()).toBe(20)
    expect(next.getHours()).toBe(9)
    expect(next.getMinutes()).toBe(30)
  })

  it('popover stays open after picking a day (so hour/minute can still be adjusted)', () => {
    const onChange = vi.fn()
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    clickDay('2026-06-20')
    expect(screen.getByRole('combobox', { name: 'Hour' })).toBeInTheDocument()
  })

  it('clicking the already-selected day clears the value (react-day-picker single-select deselects on reclick)', () => {
    const onChange = vi.fn()
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />)

    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    clickDay('2026-06-15')

    expect(onChange).toHaveBeenCalledOnce()
    expect(onChange).toHaveBeenCalledWith(null)
  })

  it('picking a day with no prior value defaults the time to a deterministic midnight, not "now"', () => {
    // Fix: handleDaySelect used to fall back to `now.getHours()/getMinutes()`
    // for a fresh (value=null) day-only pick — nondeterministic and surprising
    // for a due date. It must default to 00:00 regardless of the wall-clock
    // time the pick happens to occur at.
    vi.useFakeTimers()
    try {
      // "Now" is deliberately NOT midnight — proves the picked time doesn't leak in.
      vi.setSystemTime(new Date(2026, 5, 15, 14, 45))

      const onChange = vi.fn()
      render(<DateTimePicker value={null} onChange={onChange} aria-label="Trigger at" />)
      fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
      clickDay('2026-06-15')

      expect(onChange).toHaveBeenCalledOnce()
      const [next] = onChange.mock.calls[0] as [Date]
      expect(next.getFullYear()).toBe(2026)
      expect(next.getMonth()).toBe(5)
      expect(next.getDate()).toBe(15)
      expect(next.getHours()).toBe(0)
      expect(next.getMinutes()).toBe(0)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('DateTimePicker — hour/minute selects', () => {
  it('changing the hour updates the time and keeps the date', () => {
    const onChange = vi.fn()
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))

    selectOption('Hour', '14')

    expect(onChange).toHaveBeenCalledOnce()
    const [next] = onChange.mock.calls[0] as [Date]
    expect(next.getHours()).toBe(14)
    expect(next.getMinutes()).toBe(30)
    expect(next.getDate()).toBe(15)
  })

  it('changing the minute updates the time and keeps the date/hour', () => {
    const onChange = vi.fn()
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))

    selectOption('Minute', '45')

    expect(onChange).toHaveBeenCalledOnce()
    const [next] = onChange.mock.calls[0] as [Date]
    expect(next.getMinutes()).toBe(45)
    expect(next.getHours()).toBe(9)
    expect(next.getDate()).toBe(15)
  })

  it('selecting hour 00 sets getHours() to exactly 0 (midnight is not falsy-skipped)', () => {
    const onChange = vi.fn()
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))

    selectOption('Hour', '00')

    expect(onChange).toHaveBeenCalledOnce()
    const [next] = onChange.mock.calls[0] as [Date]
    expect(next.getHours()).toBe(0)
    expect(next.getMinutes()).toBe(30)
    expect(next.getDate()).toBe(15)
  })

  it('offers an out-of-step existing minute (e.g. :37) as a selectable option instead of dropping it', () => {
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 37)} onChange={vi.fn()} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    fireEvent.click(screen.getByRole('combobox', { name: 'Minute' }))
    expect(screen.getByRole('option', { name: '37' })).toBeInTheDocument()
  })

  it('differentiation: two different minute changes produce two different payloads', () => {
    const onChange = vi.fn()
    const { unmount } = render(
      <DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    selectOption('Minute', '15')
    expect((onChange.mock.calls[0][0] as Date).getMinutes()).toBe(15)
    unmount()

    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={onChange} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    selectOption('Minute', '50')
    expect((onChange.mock.calls[1][0] as Date).getMinutes()).toBe(50)
  })
})

describe('DateTimePicker — Done button', () => {
  it('is disabled until a value exists, and closes the popover once enabled', () => {
    render(<DateTimePicker value={null} onChange={vi.fn()} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    const popover = screen.getByRole('dialog')
    expect(within(popover).getByRole('button', { name: 'Done' })).toBeDisabled()
  })

  it('clicking Done closes the popover once a value is set', () => {
    render(<DateTimePicker value={new Date(2026, 5, 15, 9, 30)} onChange={vi.fn()} aria-label="Trigger at" />)
    fireEvent.click(screen.getByRole('button', { name: 'Trigger at' }))
    fireEvent.click(screen.getByRole('button', { name: 'Done' }))
    expect(document.querySelector('[data-day="2026-06-15"]')).not.toBeInTheDocument()
  })
})
