import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Switch } from './switch'

describe('Switch — form contract', () => {
  it('preserves identity and controlled checked state', () => {
    const onCheckedChange = vi.fn()
    const { rerender } = render(<Switch aria-label="Notifications" id="notifications" name="notifications" checked={false} onCheckedChange={onCheckedChange} required />)
    const control = screen.getByRole('switch', { name: 'Notifications' })
    expect(control).not.toBeChecked(); expect(control).toHaveAttribute('id', 'notifications')
    fireEvent.click(control); expect(onCheckedChange).toHaveBeenCalledWith(true)
    rerender(<Switch aria-label="Notifications" checked onCheckedChange={onCheckedChange} />)
    expect(control).toBeChecked()
  })

  it('does not change or invoke its callback while disabled', () => {
    const onCheckedChange = vi.fn()
    render(<Switch aria-label="Notifications" checked={false} disabled onCheckedChange={onCheckedChange} />)
    const control = screen.getByRole('switch', { name: 'Notifications' })
    fireEvent.click(control)
    fireEvent.keyDown(control, { key: ' ' })
    expect(control).not.toBeChecked()
    expect(onCheckedChange).not.toHaveBeenCalled()
  })

  it('uses distinct system-color boundary, track, and thumb cues in both states', () => {
    const { rerender } = render(<Switch aria-label="Notifications" checked={false} />)
    const control = screen.getByRole('switch', { name: 'Notifications' })
    const thumb = control.firstElementChild
    expect(control).toHaveClass(
      'forced-colors:border-[CanvasText]',
      'forced-colors:data-[state=unchecked]:bg-[Canvas]',
      'forced-colors:data-[state=checked]:bg-[Highlight]',
      'forced-colors:[forced-color-adjust:none]',
    )
    expect(thumb).toHaveClass(
      'forced-colors:data-[state=unchecked]:bg-[CanvasText]',
      'forced-colors:data-[state=checked]:bg-[HighlightText]',
    )
    expect(control).toHaveAttribute('data-state', 'unchecked')
    rerender(<Switch aria-label="Notifications" checked />)
    expect(control).toHaveAttribute('data-state', 'checked')
  })
})
