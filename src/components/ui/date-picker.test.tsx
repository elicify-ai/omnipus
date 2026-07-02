/**
 * date-picker.test.tsx — date-only picker replacing native `<input type="date">`
 * (ADR-030 §10).
 *
 * Radix Popover renders inline in jsdom (no special handling needed). Day
 * cells are targeted via react-day-picker's `data-day="YYYY-MM-DD"` attribute
 * (see calendar.test.tsx for the source citation).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DatePicker } from './date-picker'

function clickDay(isoDate: string) {
  const btn = document.querySelector(`[data-day="${isoDate}"] button`)
  if (!btn) throw new Error(`no day button for ${isoDate}`)
  fireEvent.click(btn)
}

describe('DatePicker — trigger render', () => {
  it('shows the placeholder when value is null', () => {
    render(<DatePicker value={null} onChange={vi.fn()} placeholder="Pick a date" aria-label="Due date" />)
    expect(screen.getByRole('button', { name: 'Due date' })).toHaveTextContent('Pick a date')
  })

  it('shows the formatted date when value is set', () => {
    render(<DatePicker value={new Date(2026, 5, 22)} onChange={vi.fn()} aria-label="Due date" />)
    const trigger = screen.getByRole('button', { name: 'Due date' })
    expect(trigger).toHaveTextContent('Jun 22, 2026')
  })

  it('differentiation: two different values render two different labels', () => {
    const { unmount } = render(<DatePicker value={new Date(2026, 0, 5)} onChange={vi.fn()} aria-label="Due date" />)
    expect(screen.getByRole('button', { name: 'Due date' })).toHaveTextContent('Jan 5, 2026')
    unmount()
    render(<DatePicker value={new Date(2026, 10, 30)} onChange={vi.fn()} aria-label="Due date" />)
    expect(screen.getByRole('button', { name: 'Due date' })).toHaveTextContent('Nov 30, 2026')
  })

  it('is disabled when disabled=true', () => {
    render(<DatePicker value={null} onChange={vi.fn()} disabled aria-label="Due date" />)
    expect(screen.getByRole('button', { name: 'Due date' })).toBeDisabled()
  })
})

describe('DatePicker — selection', () => {
  it('opens the popover, selecting a day calls onChange with that Date and closes', async () => {
    const onChange = vi.fn()
    render(<DatePicker value={new Date(2026, 5, 15)} onChange={onChange} aria-label="Due date" />)

    fireEvent.click(screen.getByRole('button', { name: 'Due date' }))
    clickDay('2026-06-20')

    expect(onChange).toHaveBeenCalledOnce()
    const [selected] = onChange.mock.calls[0] as [Date]
    expect(selected.getFullYear()).toBe(2026)
    expect(selected.getMonth()).toBe(5)
    expect(selected.getDate()).toBe(20)

    // Popover closes after a selection — content is unmounted from the DOM.
    expect(document.querySelector('[data-day="2026-06-20"]')).not.toBeInTheDocument()
  })

  it('opens on the value\'s own month when a value is already set', () => {
    render(<DatePicker value={new Date(2026, 8, 3)} onChange={vi.fn()} aria-label="Due date" />)
    fireEvent.click(screen.getByRole('button', { name: 'Due date' }))
    expect(document.querySelector('[data-day="2026-09-03"]')).toBeInTheDocument()
  })

  it('clicking the already-selected day clears the value (react-day-picker single-select deselects on reclick)', () => {
    const onChange = vi.fn()
    render(<DatePicker value={new Date(2026, 5, 15)} onChange={onChange} aria-label="Due date" />)

    fireEvent.click(screen.getByRole('button', { name: 'Due date' }))
    clickDay('2026-06-15')

    expect(onChange).toHaveBeenCalledOnce()
    expect(onChange).toHaveBeenCalledWith(null)
  })
})
