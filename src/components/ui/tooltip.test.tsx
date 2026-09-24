import { fireEvent, render, screen, act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Tooltip } from './tooltip'
import { Button } from './button'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runOnlyPendingTimers()
  vi.useRealTimers()
})

describe('Tooltip — reveal/dismiss', () => {
  it('is hidden until hovered, and hides again (after the close grace period) on mouse leave', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()

    fireEvent.mouseEnter(screen.getByTestId('trigger'))
    expect(screen.getByRole('tooltip')).toHaveTextContent('Explanation text')

    fireEvent.mouseLeave(screen.getByTestId('trigger'))
    // Not closed synchronously — WCAG 1.4.13 hoverable content needs a
    // grace period so the pointer can travel from the trigger onto the
    // bubble without it disappearing first (see CLOSE_GRACE_MS).
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    act(() => vi.advanceTimersByTime(200))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('reveals on keyboard focus and hides IMMEDIATELY on blur (no grace period for keyboard)', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    const trigger = screen.getByTestId('trigger')
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    fireEvent.blur(trigger)
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  // WCAG 1.4.13 (Content on Hover or Focus) — "hoverable": the pointer must
  // be able to move off the trigger and onto the tooltip content without
  // the content disappearing. Before this fix the bubble was
  // pointer-events-none (couldn't receive its own hover at all) and the
  // trigger's mouseleave closed — and un-rendered — the bubble immediately,
  // so there was never a bubble left to hover onto.
  it('stays open when the pointer moves from the trigger onto the bubble itself, and can then be left', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    fireEvent.mouseEnter(screen.getByTestId('trigger'))
    const bubble = screen.getByRole('tooltip')

    fireEvent.mouseLeave(screen.getByTestId('trigger'))
    // Pointer arrives on the bubble within the grace period.
    act(() => vi.advanceTimersByTime(30))
    fireEvent.mouseEnter(bubble)
    // The scheduled close must have been cancelled — advancing well past
    // the grace period must NOT close it now.
    act(() => vi.advanceTimersByTime(500))
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    // Leaving the bubble itself still closes it (after its own grace period).
    fireEvent.mouseLeave(bubble)
    act(() => vi.advanceTimersByTime(200))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('the bubble accepts pointer events (not pointer-events-none) so it can be hovered at all', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    fireEvent.mouseEnter(screen.getByTestId('trigger'))
    expect(screen.getByRole('tooltip')).not.toHaveClass('pointer-events-none')
  })

  // A coarse (touch) pointer has no hover for WCAG 1.4.13 to protect — but the
  // bubble (z-50, opened above the trigger) sits inside the trigger's own
  // touch-target-minimum hit region, and the later sibling wins an overlap: left
  // pointer-events:auto on touch, the open bubble would steal taps meant for the
  // trigger's expanded 44px region. `pointer-coarse:` is a distinct class token
  // from `pointer-events-none`, so this does not weaken the mouse-hoverable proof above.
  it('the bubble drops pointer events on a coarse pointer, so it cannot steal a tap meant for the trigger', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    fireEvent.mouseEnter(screen.getByTestId('trigger'))
    expect(screen.getByRole('tooltip')).toHaveClass('pointer-coarse:pointer-events-none')
  })

  // A real tap synthesizes mouseenter + focus + click for the same gesture.
  // The bug: a click handler that TOGGLED closed what hover/focus had just
  // opened, in the same gesture — so the bubble could "never open on
  // touch". openNow only ever opens; it must never close on a second tap
  // either (that would reproduce the same failure for a deliberate re-tap).
  it('a full touch gesture (hover+focus then click, as a real tap fires) ends OPEN, not closed', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    const trigger = screen.getByTestId('trigger')
    fireEvent.mouseEnter(trigger)
    fireEvent.focus(trigger)
    fireEvent.click(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
  })

  it('a second tap on the trigger does not toggle it closed', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    const trigger = screen.getByTestId('trigger')
    fireEvent.click(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    fireEvent.click(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
  })
})

describe('Tooltip — accessibility contract', () => {
  it('is keyboard reachable: the trigger accepts focus via tabIndex', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    expect(screen.getByTestId('trigger')).toHaveAttribute('tabIndex', '0')
  })

  it('associates the trigger with the bubble via aria-describedby only while open', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    const trigger = screen.getByTestId('trigger')
    expect(trigger).not.toHaveAttribute('aria-describedby')

    fireEvent.focus(trigger)
    const bubble = screen.getByRole('tooltip')
    expect(trigger).toHaveAttribute('aria-describedby', bubble.id)
    expect(bubble.id).toBeTruthy()
  })

  it('Escape dismisses the bubble WITHOUT moving focus off the trigger (not a focus trap)', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    const trigger = screen.getByTestId('trigger')
    trigger.focus()
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    // The operator's keyboard position must not change — only the
    // supplementary bubble goes away.
    expect(document.activeElement).toBe(trigger)
  })

  it('does not trap Tab: the next element in the DOM remains reachable while the bubble is open', () => {
    render(
      <div>
        <Tooltip content="Explanation text" data-testid="trigger">
          <span>Auto → Ask</span>
        </Tooltip>
        <Button type="button">Next control</Button>
      </div>,
    )
    const trigger = screen.getByTestId('trigger')
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    // A Tooltip never intercepts Tab (unlike a modal's focus-scope) — the
    // next element in document order stays a normal, un-trapped tab stop.
    const next = screen.getByRole('button', { name: 'Next control' })
    expect(next).not.toHaveAttribute('aria-hidden')
    expect(next.tabIndex).not.toBe(-1)
  })

  it('label sets the trigger’s own accessible name for icon-only triggers', () => {
    render(
      <Tooltip content="Explanation text" label="Mode status" data-testid="trigger">
        <span aria-hidden="true">i</span>
      </Tooltip>,
    )
    expect(screen.getByLabelText('Mode status')).toBe(screen.getByTestId('trigger'))
  })
})
