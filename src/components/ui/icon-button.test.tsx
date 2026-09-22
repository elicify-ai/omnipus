import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { IconButton, type IconButtonProps } from './icon-button'

const labelledByProps: IconButtonProps = { 'aria-labelledby': 'external-label', children: '×' }
const ariaLabelProps: IconButtonProps = { 'aria-label': 'Close', children: '×' }
// @ts-expect-error IconButton requires exactly one accessible-name mechanism.
const missingNameProps: IconButtonProps = { children: '×' }
// @ts-expect-error Supplying both naming mechanisms makes ownership ambiguous.
const duplicateNameProps: IconButtonProps = { 'aria-label': 'Close', 'aria-labelledby': 'external-label', children: '×' }

void [labelledByProps, ariaLabelProps, missingNameProps, duplicateNameProps]

describe('IconButton accessible action contract', () => {
  it('uses its required label as the accessible name and defaults to type=button', () => {
    render(<IconButton aria-label="Close"><span aria-hidden>×</span></IconButton>)
    expect(screen.getByRole('button', { name: 'Close' })).toHaveAttribute('type', 'button')
  })

  it.each(['', '   '])('rejects an empty accessible label %j', (label) => {
    expect(() => render(<IconButton aria-label={label}>×</IconButton>)).toThrowError('IconButton accessible name must contain visible text')
  })

  it('throws a named, clear error — never a raw TypeError — when both accessible-name props are missing', () => {
    // @ts-expect-error IconButton requires aria-label or aria-labelledby.
    const props: IconButtonProps = { children: '×' }
    let thrown: unknown
    try {
      render(<IconButton {...props} />)
    } catch (error) {
      thrown = error
    }
    expect(thrown).toBeInstanceOf(Error)
    expect((thrown as Error).name).toBe('IconButtonAccessibleNameError')
    expect((thrown as Error).message).toBe('IconButton requires an aria-label or aria-labelledby prop')
  })

  it('supports an external aria-labelledby accessible name', () => {
    render(<><span id="close-label">Close panel</span><IconButton aria-labelledby="close-label">×</IconButton></>)
    expect(screen.getByRole('button', { name: 'Close panel' })).toBeVisible()
  })

  it('suppresses activation while pending and exposes busy state', () => {
    let activations = 0
    render(<IconButton aria-label="Save" actionState="pending" onClick={() => { activations += 1 }}>S</IconButton>)
    const button = screen.getByRole('button', { name: 'Save' })
    fireEvent.click(button)
    expect(activations).toBe(0)
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('aria-busy', 'true')
  })

  it("merges a caller className through tailwind-merge so it wins over the size default, not clsx's plain concatenation", () => {
    render(<IconButton aria-label="Zoom in" className="h-4 w-4">+</IconButton>)
    const button = screen.getByRole('button', { name: 'Zoom in' })
    expect(button).toHaveClass('h-4', 'w-4')
    expect(button.className).not.toContain('h-9')
    expect(button.className).not.toContain('w-9')
  })
})
