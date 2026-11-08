/**
 * Tooltip — the old wrapper's own suite, rewritten for the Radix interface
 * (W2-1 contract §2.9 "Own tests": "Rewrite tooltip.test.tsx … for the new
 * interface. Remove the old tap-to-open and `interactive` cases, since they
 * test deleted API").
 *
 * The new interface's main contract lives in tooltip.radix.test.tsx (the
 * test author's RED pack). This file keeps only the old cases whose
 * behaviour still holds under Radix and is not already covered there:
 *   - keyboard focus reveals, blur hides at once (old case 2);
 *   - Tab is not trapped while the bubble is open (old case 11).
 *
 * Old cases removed, with the reason (recorded for the PR):
 *   - hover reveal / mouse-leave grace period, pointer-to-bubble hover,
 *     bubble accepts pointer events, Escape keeps focus, aria-describedby
 *     only while open, forced-colours repaint — covered by
 *     tooltip.radix.test.tsx on the new interface;
 *   - coarse-pointer pointer-events-none, a tap opens, a second tap does not
 *     toggle closed — tap-to-open is deleted (founder Q2 = A, Radix does
 *     not open on touch);
 *   - wrapper tabIndex / `label` prop / `interactive` mode (3 cases) — the
 *     wrapper span and those props are deleted API; the trigger is now the
 *     caller's own control via `TooltipTrigger asChild`.
 */
import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Button } from './button'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runOnlyPendingTimers()
  vi.useRealTimers()
})

function renderWithNext() {
  return render(
    <div>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button type="button" data-testid="trigger">Auto → Ask</Button>
        </TooltipTrigger>
        <TooltipContent>Explanation text</TooltipContent>
      </Tooltip>
      <Button type="button">Next control</Button>
    </div>,
  )
}

describe('Tooltip — keyboard reveal/dismiss', () => {
  it('reveals on keyboard focus and hides IMMEDIATELY on blur (no grace period for keyboard)', () => {
    renderWithNext()
    const trigger = screen.getByTestId('trigger')
    act(() => trigger.focus())
    expect(screen.getByRole('tooltip')).toHaveTextContent('Explanation text')
    act(() => trigger.blur())
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('does not trap Tab: the next element in the DOM remains reachable while the bubble is open', () => {
    renderWithNext()
    const trigger = screen.getByTestId('trigger')
    act(() => trigger.focus())
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    // A Tooltip never intercepts Tab (unlike a modal's focus scope): the
    // next element in document order stays a normal, un-trapped tab stop.
    const next = screen.getByRole('button', { name: 'Next control' })
    expect(next).not.toHaveAttribute('aria-hidden')
    expect(next.tabIndex).not.toBe(-1)
    act(() => next.focus())
    expect(document.activeElement).toBe(next)
  })
})
