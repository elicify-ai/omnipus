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
import { Field } from './field'

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

  // These assert the date's PARTS rather than one region's ordering. The
  // component formats with toLocaleDateString(undefined, ...) — deliberately
  // the viewer's own locale — so "Jun 22, 2026" and "22 Jun 2026" are both
  // correct output, and which one appears depends on the machine running the
  // test rather than on anything the component does. Pinning the US ordering
  // made these pass on a US-locale runner and fail on an en_SG one: a test that
  // reports on the runner's regional settings, not on the product.
  it('shows the formatted date when value is set', () => {
    render(<DatePicker value={new Date(2026, 5, 22)} onChange={vi.fn()} aria-label="Due date" />)
    const label = screen.getByRole('button', { name: 'Due date' }).textContent ?? ''
    expect(label).toMatch(/22/)
    expect(label).toMatch(/Jun/i)
    expect(label).toMatch(/2026/)
  })

  it('differentiation: two different values render two different labels', () => {
    const { unmount } = render(<DatePicker value={new Date(2026, 0, 5)} onChange={vi.fn()} aria-label="Due date" />)
    const first = screen.getByRole('button', { name: 'Due date' }).textContent ?? ''
    expect(first).toMatch(/Jan/i)
    expect(first).toMatch(/5/)
    unmount()
    render(<DatePicker value={new Date(2026, 10, 30)} onChange={vi.fn()} aria-label="Due date" />)
    const second = screen.getByRole('button', { name: 'Due date' }).textContent ?? ''
    expect(second).toMatch(/Nov/i)
    expect(second).toMatch(/30/)
    // The point of this test: two different values must not render the same.
    expect(second).not.toBe(first)
  })

  it('is disabled when disabled=true', () => {
    render(<DatePicker value={null} onChange={vi.fn()} disabled aria-label="Due date" />)
    expect(screen.getByRole('button', { name: 'Due date' })).toBeDisabled()
  })

  it('remains focusable but does not open when read-only', () => {
    render(<DatePicker value={new Date(2026, 5, 22)} onChange={vi.fn()} readOnly aria-label="Due date" />)
    const trigger = screen.getByRole('button', { name: 'Due date' })
    expect(trigger).toHaveAttribute('aria-disabled', 'true')
    expect(trigger).not.toBeDisabled()
    fireEvent.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(trigger).toHaveClass('data-[readonly=true]:!opacity-100')
  })

  it('forwards Field metadata and describes required without invalid button ARIA', () => {
    render(
      <Field label="Due date" description="Used for reminders" error="Choose a future date" required>
        <DatePicker value={null} onChange={vi.fn()} aria-label="Explicit due date" />
      </Field>,
    )
    const trigger = screen.getByRole('button', { name: 'Explicit due date' })
    expect(trigger.id).not.toBe('')
    expect(trigger).toHaveAttribute('aria-invalid', 'true')
    expect(trigger).not.toHaveAttribute('aria-required')
    expect(trigger).not.toHaveAttribute('required')
    const descriptions = (trigger.getAttribute('aria-describedby') ?? '').split(/\s+/).map((id) => document.getElementById(id)?.textContent)
    expect(descriptions).toEqual(expect.arrayContaining(['Used for reminders', 'Choose a future date', 'Required']))
  })

  it('rejects an invalid Date instead of rendering corrupted calendar state', () => {
    expect(() => render(<DatePicker value={new Date(Number.NaN)} onChange={vi.fn()} aria-label="Due date" />))
      .toThrow('value must be a valid Date or null')
  })

  it('bounds the open calendar popover to the narrow viewport', () => {
    render(<DatePicker value={null} onChange={vi.fn()} aria-label="Due date" />)
    fireEvent.click(screen.getByRole('button', { name: 'Due date' }))

    expect(screen.getByRole('dialog')).toHaveClass('max-w-[calc(100vw-var(--space-3))]')
  })

  it.each(['readOnly', 'disabled'] as const)('closes immediately when %s becomes true while open', (mode) => {
    const onChange = vi.fn()
    const value = new Date(2026, 5, 15)
    const { rerender } = render(<DatePicker value={value} onChange={onChange} aria-label="Due date" />)
    fireEvent.click(screen.getByRole('button', { name: 'Due date' }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    rerender(<DatePicker value={value} onChange={onChange} aria-label="Due date" {...{ [mode]: true }} />)
    expect(screen.queryByRole('dialog')).toBeNull()
    rerender(<DatePicker value={value} onChange={onChange} aria-label="Due date" />)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(onChange).not.toHaveBeenCalled()
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
