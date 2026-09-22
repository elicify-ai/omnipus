import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from './sheet'

describe('Sheet modal semantics', () => {
  it('declares the default trapped sheet as modal', () => {
    render(
      <Sheet open>
        <SheetContent>
          <SheetTitle>Details</SheetTitle>
          <SheetDescription>Sheet content</SheetDescription>
        </SheetContent>
      </Sheet>,
    )

    expect(screen.getByRole('dialog', { name: 'Details' })).toHaveAttribute('aria-modal', 'true')
  })

  it('does not claim modality when the public root opts out', () => {
    render(
      <Sheet open modal={false}>
        <SheetContent>
          <SheetTitle>Details</SheetTitle>
          <SheetDescription>Nonmodal sheet content</SheetDescription>
        </SheetContent>
      </Sheet>,
    )

    expect(screen.getByRole('dialog', { name: 'Details' })).not.toHaveAttribute('aria-modal')
  })

  it('tracks modal changes without replacing the public root API', () => {
    const content = <SheetContent><SheetTitle>Details</SheetTitle><SheetDescription>Sheet content</SheetDescription></SheetContent>
    const { rerender } = render(<Sheet open>{content}</Sheet>)
    expect(screen.getByRole('dialog', { name: 'Details' })).toHaveAttribute('aria-modal', 'true')

    rerender(<Sheet open modal={false}>{content}</Sheet>)
    expect(screen.getByRole('dialog', { name: 'Details' })).not.toHaveAttribute('aria-modal')

    rerender(<Sheet open modal>{content}</Sheet>)
    expect(screen.getByRole('dialog', { name: 'Details' })).toHaveAttribute('aria-modal', 'true')
  })

  it('preserves an explicit content aria-modal override', () => {
    render(
      <Sheet open>
        <SheetContent aria-modal="false">
          <SheetTitle>Details</SheetTitle>
          <SheetDescription>Caller-defined semantics</SheetDescription>
        </SheetContent>
      </Sheet>,
    )

    expect(screen.getByRole('dialog', { name: 'Details' })).toHaveAttribute('aria-modal', 'false')
  })
})
