/**
 * Tooltip on Radix — W2-1 contract §2.9 (R45, #1273, #828).
 *
 * RED on 69aa07d9a: this file tests the NEW interface (`TooltipProvider`,
 * `Tooltip`, `TooltipTrigger`, `TooltipContent`; no `content`/`label`/
 * `side`/`interactive` props). Those exports do not exist in today's
 * hand-built wrapper — the import fails for the right reason (the contract's
 * §4 RED item 3: "fails on the old wrapper because the exports do not
 * exist").
 *
 * The existing `tooltip.test.tsx` and all 8 callers' tests are NOT touched
 * (brief: callers' tests stay as they are; the developer rewrites the old
 * wrapper's own test file with the implementation per §2.9 "Own tests").
 *
 * Q2 (founder question): these tests encode RECOMMENDED option A — Radix's
 * behaviour, no tap-to-open. Touch behaviour itself is not asserted here:
 * jsdom cannot emulate a touch pointer type authentically.
 *
 * Drive notes (from @radix-ui/react-tooltip@1.2.14 dist, read in this task):
 * the trigger opens on `onPointerMove` (ignored for `pointerType: 'touch'`),
 * the open goes through `setTimeout(delayDuration)` (Radix's built-in
 * default is 700ms — a self-provider passing `delayDuration={0}` must open
 * within ~1ms), `aria-describedby` carries the content id while open, and
 * Escape is handled by DismissableLayer on `document`.
 */
import { act, fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { Button } from './button'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

beforeAll(() => {
  // Same jsdom shims the repo's other Radix suites use (select.test.tsx).
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runOnlyPendingTimers()
  vi.useRealTimers()
})

function renderTooltip(content = 'Explanation text') {
  return render(
    <Tooltip>
      <TooltipTrigger asChild>
        <Button type="button" data-testid="trigger">Auto → Ask</Button>
      </TooltipTrigger>
      <TooltipContent>{content}</TooltipContent>
    </Tooltip>,
  )
}

// One pointer move with a mouse pointer type + ~1ms of clock: Radix's open
// path. delayDuration={0} (the contract's provider default) opens here; the
// 700ms Radix default would not — so this doubles as the "one delay, zero"
// proof for #1273.
function hover(trigger: HTMLElement) {
  fireEvent.pointerMove(trigger, { pointerType: 'mouse', pointerId: 1 })
  act(() => vi.advanceTimersByTime(1))
}

describe('Tooltip (Radix) — interface and self-provider (§2.9)', () => {
  it('opens without any app-root provider: Tooltip wraps its own TooltipProvider', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    hover(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent('Explanation text')
  })

  it('the bubble carries data-slot="tooltip-content"', () => {
    renderTooltip()
    hover(screen.getByTestId('trigger'))
    expect(screen.getByRole('tooltip')).toHaveAttribute('data-slot', 'tooltip-content')
  })
})

describe('Tooltip (Radix) — open/close behaviour (§2.9)', () => {
  it('opens on a mouse pointer move and associates the trigger via aria-describedby while open', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    expect(trigger).not.toHaveAttribute('aria-describedby')
    hover(trigger)
    const bubble = screen.getByRole('tooltip')
    expect(bubble).toHaveTextContent('Explanation text')
    expect(trigger).toHaveAttribute('aria-describedby', bubble.id)
  })

  // Hoverable content (WCAG 1.4.13; contract §2.9): the bubble must survive
  // the pointer travelling from the trigger onto it, so Radix does NOT close
  // on a bare pointer leave — leaving the trigger creates a grace area, and
  // the bubble closes only when a LATER pointer move lands outside both the
  // trigger and the bubble (Radix 1.2.14 dist: the document-level
  // pointermove handler drops the grace area and calls onClose).
  it('closes when the pointer moves away outside the trigger and bubble; a bare pointer leave toward the bubble does not close it', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    hover(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    // Leaving the trigger alone creates the grace area; the bubble stays.
    fireEvent.pointerLeave(trigger, { pointerType: 'mouse', pointerId: 1 })
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    // A pointer move outside both trigger and bubble closes it.
    fireEvent.pointerMove(document.body, { pointerType: 'mouse', pointerId: 1, clientX: 300, clientY: 300 })
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    expect(trigger).not.toHaveAttribute('aria-describedby')
  })

  it('closes on trigger pointer-down', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    hover(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    fireEvent.pointerDown(trigger, { pointerType: 'mouse', pointerId: 1 })
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('opens on keyboard focus alone (no pointer event needed)', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    act(() => trigger.focus())
    expect(screen.getByRole('tooltip')).toHaveTextContent('Explanation text')
  })

  it('Escape dismisses the bubble, keeps focus on the trigger, and clears aria-describedby', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    act(() => trigger.focus())
    const bubble = screen.getByRole('tooltip')
    // Same dispatch the repo's Radix alert-dialog tests use: keyDown on the
    // layer's own element, which bubbles to DismissableLayer's document listener.
    fireEvent.keyDown(bubble, { key: 'Escape', code: 'Escape' })
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    expect(document.activeElement).toBe(trigger)
    expect(trigger).not.toHaveAttribute('aria-describedby')
  })

  it('focus never moves to the bubble: the trigger stays the active element while open', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    act(() => trigger.focus())
    hover(trigger)
    expect(document.activeElement).toBe(trigger)
  })

  // Q2 (recommended option A): Radix ignores touch pointers — a tap should
  // NOT open the bubble. jsdom can dispatch pointerType: 'touch', so the
  // negative is asserted directly against Radix's own guard.
  it('a touch pointer move does not open the tooltip [Q2 recommended option A]', () => {
    renderTooltip()
    const trigger = screen.getByTestId('trigger')
    fireEvent.pointerMove(trigger, { pointerType: 'touch', pointerId: 1 })
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })
})

describe('Tooltip (Radix) — content presentation (§2.9 Content)', () => {
  it('carries the contract classes: token surface, radius, border, caption text, floating elevation, balance', () => {
    renderTooltip()
    hover(screen.getByTestId('trigger'))
    const bubble = screen.getByRole('tooltip')
    expect(bubble).toHaveClass(
      'z-50',
      'w-fit',
      'rounded-[var(--radius-medium)]',
      'border',
      'border-[var(--color-border)]',
      'bg-[var(--color-surface-3)]',
      'text-[length:var(--type-caption-size)]',
      'text-[var(--color-secondary)]',
      'shadow-[var(--elevation-floating)]',
      'text-balance',
    )
    expect(bubble).toHaveClass('px-[var(--space-2)]')
    expect(bubble).toHaveClass('py-[var(--space-1)]')
    // The old fixed width is gone (R45: the bubble fits its text).
    expect(bubble.className).not.toContain('w-64')
  })

  it('keeps the forced-colours system repaint from the old bubble', () => {
    renderTooltip()
    hover(screen.getByTestId('trigger'))
    expect(screen.getByRole('tooltip')).toHaveClass(
      'forced-colors:border-[CanvasText]',
      'forced-colors:bg-[Canvas]',
      'forced-colors:text-[CanvasText]',
      'forced-colors:[forced-color-adjust:none]',
    )
  })

  it('animates in but opts out under prefers-reduced-motion (class proxy; computed animation-name is the reduced-motion browser kind)', () => {
    renderTooltip()
    hover(screen.getByTestId('trigger'))
    const bubble = screen.getByRole('tooltip')
    expect(bubble.className).toContain('motion-reduce:animate-none')
  })

  it('keeps the bubble hoverable by default (no disableHoverableContent)', () => {
    renderTooltip()
    hover(screen.getByTestId('trigger'))
    // Radix's default (disableHoverableContent=false) keeps pointer events
    // on the bubble — the WCAG 1.4.13 behaviour the contract names. Asserted
    // negatively: the content must not carry pointer-events-none.
    expect(screen.getByRole('tooltip').className).not.toContain('pointer-events-none')
  })
})
