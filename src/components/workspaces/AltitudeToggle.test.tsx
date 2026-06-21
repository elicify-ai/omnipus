/**
 * AltitudeToggle.test.tsx — the Board depth control (top-level vs show-all).
 *
 * Covers: both options render as radios, the active value is checked, and
 * clicking the inactive option fires onChange with that value (clicking the
 * already-active one still reports its value — the parent owns the state).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AltitudeToggle } from './AltitudeToggle'

describe('AltitudeToggle', () => {
  it('renders both options as a radio group with the active one checked', () => {
    render(<AltitudeToggle value="top-level" onChange={vi.fn()} />)
    const group = screen.getByRole('group', { name: 'Board depth' })
    expect(group).toBeInTheDocument()

    const topLevel = screen.getByRole('radio', { name: 'Top-level' })
    const showAll = screen.getByRole('radio', { name: 'Show all' })
    expect(topLevel).toHaveAttribute('aria-checked', 'true')
    expect(showAll).toHaveAttribute('aria-checked', 'false')
  })

  it('fires onChange("show-all") when the inactive option is clicked', () => {
    const onChange = vi.fn()
    render(<AltitudeToggle value="top-level" onChange={onChange} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Show all' }))
    expect(onChange).toHaveBeenCalledWith('show-all')
  })

  it('reflects "show-all" as the checked option when that is the value', () => {
    render(<AltitudeToggle value="show-all" onChange={vi.fn()} />)
    expect(screen.getByRole('radio', { name: 'Show all' })).toHaveAttribute(
      'aria-checked',
      'true',
    )
  })
})
