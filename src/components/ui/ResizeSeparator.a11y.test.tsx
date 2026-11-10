// ResizeSeparator.a11y.test.tsx — side-panel-shell-spec.md §12 test #10:
// the resize separator's complete assistive state (US-9 AS-1/AS-2, MIN-205).
//
// CHARACTERISATION — GREEN ON WAVE-0 CODE: src/components/ui/
// resize-separator.tsx shipped in wave 0 with this behaviour; this file pins
// it so the wave-1 shell wiring cannot regress the control. Failability
// proven in RED by a one-mutation probe (keyboard step default 16 -> 17):
// the run died on the step tests, the mutation was reverted, and
// `git status` confirmed no production change survived.
//
// Oracles: side-panel-shell-spec.md — the separator exposes name, role,
// orientation, aria-controls to the panel, valuemin/max/now, valuetext
// "N pixels wide", is focusable (Tab), adjusts by a fixed 16px step
// (ArrowLeft widens a right-side panel), Home/End to the bounds, commits a
// settled choice (drag release; keyboard 300ms after the last keypress,
// MIN-205), never commits before a settle, and double-click resets (US-2
// AS-3).

import { useState } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { ResizeSeparator } from '@/components/ui/resize-separator'

beforeAll(() => {
  // jsdom has no pointer-capture backend; stub the two Element methods the
  // control calls so a jsdom pointer drag runs the real control logic.
  if (!Element.prototype.setPointerCapture) {
    Element.prototype.setPointerCapture = function setPointerCapture() {}
    Element.prototype.releasePointerCapture = function releasePointerCapture() {}
  }
})

function Harness(props: {
  panelSide?: 'left' | 'right'
  onCommit?: (px: number) => void
}) {
  const [value, setValue] = useState(400)
  return (
    <div>
      <div id="side-panel-column" />
      <ResizeSeparator
        label="Resize Library panel"
        value={value}
        min={320}
        max={800}
        controls="side-panel-column"
        panelSide={props.panelSide ?? 'right'}
        onValueChange={(px) => setValue(px)}
        onCommit={(px) => {
          setValue(px)
          props.onCommit?.(px)
        }}
        onReset={() => setValue(400)}
      />
    </div>
  )
}

describe('ResizeSeparator assistive state (§12 #10, US-9) — characterisation, green on wave-0 code', () => {
  afterEach(() => {
    cleanup()
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('role="separator", vertical orientation, accessible name, aria-controls to the panel column, focusable', () => {
    render(<Harness />)
    const sep = screen.getByRole('separator')
    expect(sep.getAttribute('aria-orientation')).toBe('vertical')
    expect(sep.getAttribute('aria-label')).toBe('Resize Library panel')
    expect(sep.getAttribute('aria-controls')).toBe('side-panel-column')
    expect(sep.getAttribute('tabindex')).toBe('0')
    sep.focus()
    expect(document.activeElement).toBe(sep)
  })

  it('full aria value state: valuemin 320 / valuemax 800 / valuenow = current width, valuetext "400 pixels wide" (US-9 AS-1)', () => {
    render(<Harness />)
    const sep = screen.getByRole('separator')
    expect(sep.getAttribute('aria-valuemin')).toBe('320')
    expect(sep.getAttribute('aria-valuemax')).toBe('800')
    expect(sep.getAttribute('aria-valuenow')).toBe('400')
    expect(sep.getAttribute('aria-valuetext')).toBe('400 pixels wide')
  })

  it('ArrowLeft widens a right-side panel by the 16px step (US-9 AS-2); ArrowRight narrows', () => {
    render(<Harness />)
    const sep = screen.getByRole('separator')
    fireEvent.keyDown(sep, { key: 'ArrowLeft' })
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('416')
    fireEvent.keyDown(sep, { key: 'ArrowRight' })
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('400')
  })

  it('Home lands on the 320px floor, End on the 800px ceiling (US-9 AS-2); a single settle commit of the LAST value (MIN-205)', () => {
    vi.useFakeTimers()
    const commits: number[] = []
    render(<Harness onCommit={(px) => commits.push(px)} />)
    const sep = screen.getByRole('separator')
    fireEvent.keyDown(sep, { key: 'Home' })
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('320')
    fireEvent.keyDown(sep, { key: 'End' })
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('800')
    vi.advanceTimersByTime(299)
    expect(commits).toEqual([])
    vi.advanceTimersByTime(1)
    expect(commits).toEqual([800]) // one commit, of the last value only
  })

  it('no commit before a settle: keys without 300ms of quiet commit NOTHING (MIN-205 — transient previews never persist)', () => {
    vi.useFakeTimers()
    const onCommit = vi.fn()
    render(
      <ResizeSeparator
        label="x" value={400} min={320} max={800}
        onValueChange={() => {}} onCommit={onCommit}
      />,
    )
    const sep = screen.getByRole('separator')
    fireEvent.keyDown(sep, { key: 'ArrowLeft' })
    vi.advanceTimersByTime(299)
    expect(onCommit).not.toHaveBeenCalled()
    fireEvent.keyDown(sep, { key: 'ArrowLeft' }) // reschedules the settle
    vi.advanceTimersByTime(299)
    expect(onCommit).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(onCommit).toHaveBeenCalledTimes(1)
  })

  it('pointer drag follows the pointer live and commits ONLY on release (US-2 AS-1; release is the settle)', () => {
    const onValueChange = vi.fn()
    const onCommit = vi.fn()
    render(
      <ResizeSeparator
        label="x" value={400} min={320} max={800}
        onValueChange={onValueChange} onCommit={onCommit}
      />,
    )
    const sep = screen.getByRole('separator')
    fireEvent.pointerDown(sep, { button: 0, pointerId: 1, clientX: 100 })
    fireEvent.pointerMove(sep, { pointerId: 1, clientX: 60 })
    expect(onCommit).not.toHaveBeenCalled() // live preview, not settled
    expect(onValueChange).toHaveBeenLastCalledWith(440, 'drag')
    fireEvent.pointerMove(sep, { pointerId: 1, clientX: 40 })
    expect(onValueChange).toHaveBeenLastCalledWith(460, 'drag')
    fireEvent.pointerUp(sep, { pointerId: 1 })
    expect(onCommit).toHaveBeenLastCalledWith(460, 'drag')
  })

  it('drag clamps at both bounds mid-drag (SP-17): never emits below 320 or above the ceiling', () => {
    render(<Harness />)
    const sep = screen.getByRole('separator')
    fireEvent.pointerDown(sep, { button: 0, pointerId: 1, clientX: 100 })
    fireEvent.pointerMove(sep, { pointerId: 1, clientX: 2000 })
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('320')
    fireEvent.pointerMove(sep, { pointerId: 1, clientX: -2000 })
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('800')
    fireEvent.pointerUp(sep, { pointerId: 1 })
  })

  it('double-click resets (US-2 AS-3): onReset returns the separator to the default-derived width', () => {
    render(<Harness />)
    const sep = screen.getByRole('separator')
    fireEvent.keyDown(sep, { key: 'End' }) // move away from 400 first
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('800')
    fireEvent.doubleClick(sep)
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('400')
  })

  it('a LEFT-side panel mirrors the arrows (ArrowRight widens) — the geometry is side-aware (US-9 AS-2)', () => {
    render(<Harness panelSide="left" />)
    const sep = screen.getByRole('separator')
    fireEvent.keyDown(sep, { key: 'ArrowRight' })
    expect(screen.getByRole('separator').getAttribute('aria-valuenow')).toBe('416')
  })
})
