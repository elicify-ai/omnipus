import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Tooltip } from './tooltip'
import { Button } from './button'

describe('Tooltip — reveal/dismiss', () => {
  it('is hidden until hovered, and hides again on mouse leave', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()

    fireEvent.mouseEnter(screen.getByTestId('trigger'))
    expect(screen.getByRole('tooltip')).toHaveTextContent('Explanation text')

    fireEvent.mouseLeave(screen.getByTestId('trigger'))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('reveals on keyboard focus and hides on blur', () => {
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

  it('toggles on click, for touch (no hover) reachability', () => {
    render(
      <Tooltip content="Explanation text" data-testid="trigger">
        <span>Auto → Ask</span>
      </Tooltip>,
    )
    const trigger = screen.getByTestId('trigger')
    fireEvent.click(trigger)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()
    fireEvent.click(trigger)
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
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
