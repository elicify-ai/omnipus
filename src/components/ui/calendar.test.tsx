/**
 * calendar.test.tsx — Sovereign Deep themed wrapper around react-day-picker v9.
 *
 * Traces to: ADR-030 §10 (native date/time inputs → shadcn-style pickers).
 * Strategy: DayPicker renders inline (no portal), so it mounts directly in
 * jsdom. Days are queried by the `data-day="YYYY-MM-DD"` attribute
 * react-day-picker puts on each grid cell (verified against the installed
 * v9.14.0 source — see DayPicker.js's `data-day: day.isoDate`).
 */

import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'
import { Calendar } from './calendar'

function dayCell(isoDate: string): HTMLElement | null {
  return document.querySelector(`[data-day="${isoDate}"]`)
}

function dayButton(isoDate: string): HTMLElement {
  const cell = dayCell(isoDate)
  const btn = cell?.querySelector('button')
  if (!btn) throw new Error(`no day button for ${isoDate}`)
  return btn as HTMLElement
}

describe('Calendar — render', () => {
  it('renders a month grid with the weekday header and 7 columns', () => {
    const { container } = render(<Calendar mode="single" defaultMonth={new Date(2026, 5, 1)} />)
    // 7 weekday header cells (Sun..Sat by default locale). jsdom's implicit-ARIA
    // role computation does not map `th[scope=col]` to "columnheader" inside a
    // `<table role="grid">` (react-day-picker's MonthGrid), so query the DOM
    // directly rather than via role.
    expect(container.querySelectorAll('th')).toHaveLength(7)
    // June 2026 has 30 days — each should have a grid cell.
    for (let d = 1; d <= 30; d++) {
      const iso = `2026-06-${d.toString().padStart(2, '0')}`
      expect(dayCell(iso)).toBeTruthy()
    }
  })

  it('marks the selected day with data-selected and no other day', () => {
    render(<Calendar mode="single" selected={new Date(2026, 5, 15)} defaultMonth={new Date(2026, 5, 1)} />)
    expect(dayCell('2026-06-15')?.getAttribute('data-selected')).toBe('true')
    expect(dayCell('2026-06-14')?.getAttribute('data-selected')).toBeNull()
    expect(dayCell('2026-06-16')?.getAttribute('data-selected')).toBeNull()
  })

  it('renders no selected day when selected is undefined', () => {
    render(<Calendar mode="single" defaultMonth={new Date(2026, 5, 1)} />)
    for (let d = 1; d <= 30; d++) {
      const iso = `2026-06-${d.toString().padStart(2, '0')}`
      expect(dayCell(iso)?.getAttribute('data-selected')).toBeNull()
    }
  })
})

describe('Calendar — selection', () => {
  it('clicking a day calls onSelect with that Date', () => {
    const onSelect = vi.fn()
    render(<Calendar mode="single" defaultMonth={new Date(2026, 5, 1)} onSelect={onSelect} />)
    dayButton('2026-06-20').click()
    expect(onSelect).toHaveBeenCalledOnce()
    const [selected] = onSelect.mock.calls[0]
    expect(selected.getFullYear()).toBe(2026)
    expect(selected.getMonth()).toBe(5)
    expect(selected.getDate()).toBe(20)
  })

  it('differentiation: clicking two different days produces two different onSelect payloads', () => {
    const onSelect = vi.fn()
    render(<Calendar mode="single" defaultMonth={new Date(2026, 5, 1)} onSelect={onSelect} />)
    dayButton('2026-06-05').click()
    dayButton('2026-06-25').click()
    expect(onSelect).toHaveBeenCalledTimes(2)
    expect((onSelect.mock.calls[0][0] as Date).getDate()).toBe(5)
    expect((onSelect.mock.calls[1][0] as Date).getDate()).toBe(25)
  })

  it('disabled days are not interactive and do not fire onSelect', () => {
    const onSelect = vi.fn()
    render(
      <Calendar
        mode="single"
        defaultMonth={new Date(2026, 5, 1)}
        onSelect={onSelect}
        disabled={{ before: new Date(2026, 5, 10) }}
      />,
    )
    expect(dayCell('2026-06-05')?.getAttribute('data-disabled')).toBe('true')
    const btn = dayButton('2026-06-05') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    btn.click()
    expect(onSelect).not.toHaveBeenCalled()
  })
})

describe('Calendar — outside days', () => {
  it('marks days from the adjacent month as outside', () => {
    render(<Calendar mode="single" defaultMonth={new Date(2026, 5, 1)} showOutsideDays />)
    // May 31 2026 is a Sunday and June 1 2026 is a Monday — May 31 is the
    // outside leading day shown to fill the first week's row.
    expect(dayCell('2026-05-31')?.getAttribute('data-outside')).toBe('true')
  })
})
