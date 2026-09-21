import * as React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from './accordion'
import { Command, CommandInput } from './command'
import { ConfirmDialog } from './confirm-dialog'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle, DialogTrigger } from './dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from './dropdown-menu'
import { Popover, PopoverContent, PopoverTrigger } from './popover'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetTitle } from './sheet'

describe('overlay contracts', () => {
  it('separates stacked dialog footer actions without changing the desktop row spacing', () => {
    const { container } = render(<DialogFooter><button>Cancel</button><button>Save</button></DialogFooter>)
    expect(container.firstChild).toHaveClass('gap-[var(--space-2)]', 'max-sm:pointer-coarse:gap-[var(--space-4)]', 'sm:space-x-[var(--space-2)]', 'sm:gap-0')
  })

  it('separates stacked sheet footer actions without changing the desktop row spacing', () => {
    const { container } = render(<SheetFooter><button>Cancel</button><button>Save</button></SheetFooter>)
    expect(container.firstChild).toHaveClass('gap-[var(--space-2)]', 'max-sm:pointer-coarse:gap-[var(--space-4)]', 'sm:space-x-[var(--space-2)]', 'sm:gap-0')
  })

  it('traps dialog focus, dismisses with Escape, and restores its trigger', async () => {
    const user = userEvent.setup()
    render(<><button data-testid="outside">Outside</button><Dialog><DialogTrigger>Open</DialogTrigger><DialogContent><DialogTitle>Details</DialogTitle><DialogDescription>Body</DialogDescription><button>Inside</button></DialogContent></Dialog></>)
    const outside = screen.getByTestId('outside')
    const trigger = screen.getByRole('button', { name: 'Open' })
    await user.click(trigger)
    expect(screen.getByRole('dialog').className).toContain('overscroll-contain')
    expect(screen.getByRole('button', { name: 'Close' })).toHaveAttribute('data-ds-action')
    expect(screen.getByRole('button', { name: 'Inside' })).toHaveFocus()
    await user.tab()
    expect(outside).not.toHaveFocus()
    await user.tab()
    expect(outside).not.toHaveFocus()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('maps named sheet sizes without removing the legacy width override', () => {
    const { rerender } = render(<Sheet open><SheetContent size="sm"><SheetTitle>Sheet</SheetTitle><SheetDescription>Body</SheetDescription></SheetContent></Sheet>)
    expect(screen.getByRole('dialog').className).toContain('sm:max-w-md')
    expect(screen.getByRole('button', { name: 'Close' })).toHaveAttribute('data-ds-action')
    rerender(<Sheet open><SheetContent size="lg" widthClass="sm:max-w-xl"><SheetTitle>Sheet</SheetTitle><SheetDescription>Body</SheetDescription></SheetContent></Sheet>)
    expect(screen.getByRole('dialog').className).toContain('sm:max-w-xl')
  })

  it('gives command search an owned accessible name with caller override', () => {
    const { rerender } = render(<Command><CommandInput /></Command>)
    expect(screen.getByRole('combobox', { name: 'Command search' })).toHaveClass('[@media(pointer:coarse)]:min-h-[44px]')
    rerender(<Command label="Search people"><CommandInput /></Command>)
    expect(screen.getByRole('combobox', { name: 'Search people' })).toBeInTheDocument()
  })

  it('supports accordion keyboard expansion and hides its decorative icon', async () => {
    const user = userEvent.setup()
    render(<Accordion type="single"><AccordionItem value="one"><AccordionTrigger>Section</AccordionTrigger><AccordionContent>Answer</AccordionContent></AccordionItem></Accordion>)
    const trigger = screen.getByRole('button', { name: 'Section' })
    trigger.focus()
    await user.keyboard('{Enter}')
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(trigger.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('keeps confirmation open after confirm, blocks pending repeats, and permits Escape', () => {
    const onOpenChange = vi.fn()
    const onConfirm = vi.fn()
    const { rerender } = render(<ConfirmDialog open onOpenChange={onOpenChange} title="Delete?" description="Permanent" confirmLabel="Delete" onConfirm={onConfirm} />)
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(onConfirm).toHaveBeenCalledOnce()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
    rerender(<ConfirmDialog open pending onOpenChange={onOpenChange} title="Delete?" description="Permanent" confirmLabel="Delete" onConfirm={onConfirm} />)
    expect(screen.getByRole('alertdialog')).toHaveAttribute('aria-busy', 'true')
    fireEvent.pointerDown(document.body)
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Delete' })).toHaveAttribute('aria-busy', 'true')
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(onConfirm).toHaveBeenCalledOnce()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    expect(onOpenChange.mock.calls).toEqual([[false]])
  })

  it('defaults confirm-dialog emphasis to the confirm button, and inverts it when asked', () => {
    const onOpenChange = vi.fn()
    const onConfirm = vi.fn()
    const { rerender } = render(<ConfirmDialog open onOpenChange={onOpenChange} title="Weaken?" description="Lowers protection" confirmLabel="Weaken" cancelLabel="Keep safe" onConfirm={onConfirm} />)
    expect(screen.getByRole('button', { name: 'Keep safe' }).className).toContain('border')
    expect(screen.getByRole('button', { name: 'Weaken' }).className).toContain('bg-[var(--color-accent)]')
    rerender(<ConfirmDialog open emphasis="cancel" onOpenChange={onOpenChange} title="Weaken?" description="Lowers protection" confirmLabel="Weaken" cancelLabel="Keep safe" onConfirm={onConfirm} />)
    expect(screen.getByRole('button', { name: 'Keep safe' }).className).toContain('bg-[var(--color-accent)]')
    expect(screen.getByRole('button', { name: 'Weaken' }).className).toContain('border')
    expect(screen.getByRole('button', { name: 'Weaken' }).className).not.toContain('bg-[var(--color-accent)]')
  })

  it('restores the opener when Escape dismisses a pending confirmation', async () => {
    const user = userEvent.setup()
    function Fixture() {
      const [open, setOpen] = React.useState(false)
      return <><button onClick={() => setOpen(true)}>Pending deletion</button><ConfirmDialog open={open} pending onOpenChange={setOpen} title="Delete?" description="Operation continues after dismissal" confirmLabel="Delete" onConfirm={() => {}} /></>
    }
    render(<Fixture />)
    const opener = screen.getByRole('button', { name: 'Pending deletion' })
    await user.click(opener)
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(opener).toHaveFocus()
  })

  it.each(['Cancel', 'Escape'] as const)('restores the controlled confirmation opener after %s', async (dismissal) => {
    const user = userEvent.setup()
    function Fixture() {
      const [open, setOpen] = React.useState(false)
      return <><button onClick={() => setOpen(true)}>Delete row</button><ConfirmDialog open={open} onOpenChange={setOpen} title="Delete?" description="Permanent" confirmLabel="Delete" onConfirm={() => {}} /></>
    }
    render(<Fixture />)
    const opener = screen.getByRole('button', { name: 'Delete row' })
    await user.click(opener)
    if (dismissal === 'Cancel') await user.click(screen.getByRole('button', { name: 'Cancel' }))
    else await user.keyboard('{Escape}')
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('restores focus inside a parent dialog after closing a nested confirmation', async () => {
    const user = userEvent.setup()
    function Fixture() {
      const [confirmOpen, setConfirmOpen] = React.useState(false)
      return <Dialog><DialogTrigger>Open editor</DialogTrigger><DialogContent><DialogTitle>Editor</DialogTitle><DialogDescription>Edit item</DialogDescription><button onClick={() => setConfirmOpen(true)}>Delete row</button><ConfirmDialog open={confirmOpen} onOpenChange={setConfirmOpen} title="Delete?" description="Permanent" confirmLabel="Delete" onConfirm={() => {}} /></DialogContent></Dialog>
    }
    render(<Fixture />)
    await user.click(screen.getByRole('button', { name: 'Open editor' }))
    const opener = screen.getByRole('button', { name: 'Delete row' })
    await user.click(opener)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(opener).toHaveFocus())
    expect(opener.closest('[role="dialog"]')).not.toBeNull()
  })

  it('restores focus after a StrictMode controlled close', async () => {
    const user = userEvent.setup()
    function Fixture() {
      const [open, setOpen] = React.useState(false)
      return <><button onClick={() => setOpen(true)}>Delete row</button><ConfirmDialog open={open} onOpenChange={setOpen} title="Delete?" description="Permanent" confirmLabel="Delete" onConfirm={() => {}} /></>
    }
    render(<React.StrictMode><Fixture /></React.StrictMode>)
    const opener = screen.getByRole('button', { name: 'Delete row' })
    await user.click(opener)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('restores focus when the parent controls the close directly', async () => {
    let closeFromParent = () => {}
    function Fixture() {
      const [open, setOpen] = React.useState(false)
      closeFromParent = () => setOpen(false)
      return <><button onClick={() => setOpen(true)}>Delete row</button><ConfirmDialog open={open} onOpenChange={setOpen} title="Delete?" description="Permanent" confirmLabel="Delete" onConfirm={() => {}} /></>
    }
    const user = userEvent.setup()
    render(<Fixture />)
    const opener = screen.getByRole('button', { name: 'Delete row' })
    await user.click(opener)
    React.act(() => closeFromParent())
    await waitFor(() => expect(opener).toHaveFocus())
  })
})

describe('overlay source contracts', () => {
  it('preserves dropdown submenu composition', async () => {
    render(<DropdownMenu open><DropdownMenuTrigger>Actions</DropdownMenuTrigger><DropdownMenuContent><DropdownMenuSub><DropdownMenuSubTrigger>More</DropdownMenuSubTrigger><DropdownMenuSubContent><DropdownMenuItem>Nested action</DropdownMenuItem></DropdownMenuSubContent></DropdownMenuSub></DropdownMenuContent></DropdownMenu>)
    expect(await screen.findByRole('menuitem', { name: 'More' })).toHaveAttribute('aria-haspopup', 'menu')
  })

  it('preserves popover portal composition', async () => {
    render(<Popover><PopoverTrigger>Details</PopoverTrigger><PopoverContent>Popover body</PopoverContent></Popover>)
    fireEvent.click(screen.getByRole('button', { name: 'Details' }))
    expect(await screen.findByText('Popover body')).toBeVisible()
  })
})
