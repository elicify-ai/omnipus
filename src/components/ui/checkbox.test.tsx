import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Checkbox } from './checkbox'

describe('Checkbox — form contract', () => {
  it('preserves identity and controlled checked state', () => {
    const onCheckedChange = vi.fn()
    const { rerender } = render(<Checkbox aria-label="Archive" id="archive" name="archive" checked={false} onCheckedChange={onCheckedChange} required />)
    const checkbox = screen.getByRole('checkbox', { name: 'Archive' })
    expect(checkbox).not.toBeChecked(); expect(checkbox).toHaveAttribute('id', 'archive')
    fireEvent.click(checkbox); expect(onCheckedChange).toHaveBeenCalledWith(true)
    rerender(<Checkbox aria-label="Archive" checked onCheckedChange={onCheckedChange} />)
    expect(checkbox).toBeChecked()
  })

  it('does not change or invoke its callback while disabled', () => {
    const onCheckedChange = vi.fn()
    render(<Checkbox aria-label="Archive" checked={false} disabled onCheckedChange={onCheckedChange} />)
    const checkbox = screen.getByRole('checkbox', { name: 'Archive' })
    fireEvent.click(checkbox)
    fireEvent.keyDown(checkbox, { key: ' ' })
    expect(checkbox).not.toBeChecked()
    expect(onCheckedChange).not.toHaveBeenCalled()
  })

  it('hides its decorative checkmark from assistive technology', () => {
    const { container } = render(<Checkbox aria-label="Archive" checked />)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('maps the checked indicator to a readable forced-colors pair', () => {
    render(<Checkbox aria-label="Archive" checked />)
    expect(screen.getByRole('checkbox')).toHaveClass(
      'data-[state=checked]:forced-colors:border-[HighlightText]',
      'data-[state=checked]:forced-colors:bg-[Highlight]',
      'data-[state=checked]:forced-colors:text-[HighlightText]',
    )
  })
})
