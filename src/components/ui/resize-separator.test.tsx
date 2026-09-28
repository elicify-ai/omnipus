// resize-separator.test.tsx — the shared resize separator's unit contract
// (four-part contract, part 4): separator semantics with the complete
// US-9 AS-1 assistive state, the US-9 AS-2 keyboard step and Home/End, and
// the US-2 AS-3 double-click reset callback.

import { fireEvent, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ResizeSeparator } from './resize-separator'

function mount(overrides?: Partial<Parameters<typeof ResizeSeparator>[0]>) {
  const onValueChange = vi.fn()
  const props = {
    label: 'Resize Library panel',
    value: 640,
    min: 320,
    max: 720,
    onValueChange,
    ...overrides,
  }
  const utils = render(<ResizeSeparator {...props} />)
  return { ...utils, onValueChange, separator: utils.getByRole('separator') }
}

describe('ResizeSeparator semantics', () => {
  it('exposes the complete US-9 AS-1 assistive state', () => {
    const { separator } = mount({ label: 'Resize Tasks panel', min: 320, max: 800 })
    expect(separator).toHaveAttribute('aria-orientation', 'vertical')
    expect(separator).toHaveAttribute('aria-label', 'Resize Tasks panel')
    expect(separator).toHaveAttribute('aria-valuemin', '320')
    expect(separator).toHaveAttribute('aria-valuemax', '800')
    expect(separator).toHaveAttribute('aria-valuenow', '640')
    expect(separator).toHaveAttribute('aria-valuetext', '640 pixels wide')
    expect(separator).toHaveAttribute('tabindex', '0')
  })

  it('wires aria-controls to the panel element', () => {
    const { separator } = mount({ controls: 'side-panel-library' })
    expect(separator).toHaveAttribute('aria-controls', 'side-panel-library')
  })
})

describe('ResizeSeparator keyboard', () => {
  it('steps 16px per ArrowLeft (widening a right-side panel)', () => {
    const { separator, onValueChange } = mount()
    fireEvent.keyDown(separator, { key: 'ArrowLeft' })
    expect(onValueChange).toHaveBeenCalledWith(656, 'keyboard')
  })

  it('clamps at the bounds', () => {
    const { separator, onValueChange } = mount({ value: 710 })
    fireEvent.keyDown(separator, { key: 'ArrowLeft' })
    expect(onValueChange).toHaveBeenCalledWith(720, 'keyboard')
  })

  it('Home and End jump to the bounds', () => {
    const { separator, onValueChange } = mount()
    fireEvent.keyDown(separator, { key: 'Home' })
    expect(onValueChange).toHaveBeenLastCalledWith(320, 'min')
    fireEvent.keyDown(separator, { key: 'End' })
    expect(onValueChange).toHaveBeenLastCalledWith(720, 'max')
  })

  it('commits 300ms after the last keypress (MIN-205)', () => {
    vi.useFakeTimers()
    try {
      const onCommit = vi.fn()
      const { separator } = mount({ onCommit })
      fireEvent.keyDown(separator, { key: 'ArrowLeft' })
      fireEvent.keyDown(separator, { key: 'ArrowLeft' })
      vi.advanceTimersByTime(299)
      expect(onCommit).not.toHaveBeenCalled()
      vi.advanceTimersByTime(1)
      expect(onCommit).toHaveBeenCalledWith(672, 'keyboard')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('ResizeSeparator drag', () => {
  it('follows the pointer live and commits on release (US-2 AS-1)', () => {
    const onCommit = vi.fn()
    const { separator, onValueChange } = mount({ onCommit })
    separator.setPointerCapture = vi.fn()
    separator.releasePointerCapture = vi.fn()
    const box = separator.getBoundingClientRect()
    fireEvent.pointerDown(separator, { button: 0, pointerId: 7, clientX: box.left + 100, clientY: box.top })
    fireEvent.pointerMove(separator, { pointerId: 7, clientX: box.left + 60, clientY: box.top })
    expect(onValueChange).toHaveBeenCalledWith(680, 'drag')
    fireEvent.pointerUp(separator, { pointerId: 7, clientX: box.left + 60, clientY: box.top })
    expect(onCommit).toHaveBeenCalledWith(680, 'drag')
  })

  it('double-click resets via onReset (US-2 AS-3)', () => {
    const onReset = vi.fn()
    const { separator } = mount({ onReset })
    fireEvent.dblClick(separator)
    expect(onReset).toHaveBeenCalledTimes(1)
  })
})
