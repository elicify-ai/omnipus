import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from './alert-dialog'

// AlertDialog is the design-system confirmation surface that replaces
// window.confirm. Unlike Dialog it must NOT dismiss on overlay/outside click —
// a destructive confirm has to be deliberate. The consumer owns close-on-confirm.

function Harness({
  open,
  onOpenChange,
  onAction,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  onAction: () => void
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete thing</AlertDialogTitle>
          <AlertDialogDescription>This cannot be undone.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onAction}>Delete</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

describe('AlertDialog', () => {
  it('renders with role="alertdialog"', () => {
    render(<Harness open onOpenChange={() => {}} onAction={() => {}} />)
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  })

  it('does NOT close on overlay/outside click', async () => {
    const onOpenChange = vi.fn()
    render(<Harness open onOpenChange={onOpenChange} onAction={() => {}} />)
    const dialog = screen.getByRole('alertdialog')
    // Simulate an outside pointer interaction (Radix dismiss path). The content
    // preventDefaults onPointerDownOutside / onInteractOutside, so no close.
    fireEvent.pointerDown(document.body)
    fireEvent.pointerUp(document.body)
    fireEvent.click(document.body)
    expect(dialog).toBeInTheDocument()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('closes on Escape', async () => {
    const onOpenChange = vi.fn()
    render(<Harness open onOpenChange={onOpenChange} onAction={() => {}} />)
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape', code: 'Escape' })
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('closes when Cancel is clicked', async () => {
    const onOpenChange = vi.fn()
    render(<Harness open onOpenChange={onOpenChange} onAction={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('does NOT auto-close when Action is clicked (consumer controls closing)', () => {
    const onOpenChange = vi.fn()
    const onAction = vi.fn()
    render(<Harness open onOpenChange={onOpenChange} onAction={onAction} />)
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    // The action handler fires...
    expect(onAction).toHaveBeenCalledTimes(1)
    // ...but the dialog does not close itself: onOpenChange(false) is the
    // consumer's job, not the Action button's.
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  })

  // Regression guard: `flex-col-reverse` on the footer inverts visual order vs
  // DOM order below `sm`, so Cancel (first in DOM, first in Tab order) would
  // render visually BELOW the primary Action button — the exact "CSS
  // reordering breaks reading/tab order" pattern this project forbids. The
  // footer must stack in DOM order (Cancel above Action) on phone widths and
  // only becomes a `sm:flex-row` row at `sm+`.
  it('footer stacks in DOM order (no *-reverse class)', () => {
    render(<Harness open onOpenChange={() => {}} onAction={() => {}} />)
    const cancelButton = screen.getByRole('button', { name: 'Cancel' })
    const footer = cancelButton.closest('div')
    expect(footer).not.toBeNull()
    expect(footer!.className).not.toMatch(/-reverse\b/)
    expect(footer!.className).toMatch(/\bflex-col\b/)
  })
})
