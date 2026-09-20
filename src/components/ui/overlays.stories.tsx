import type { Meta, StoryObj } from '@storybook/react-vite'
import { useState } from 'react'
import { expect, userEvent, waitFor, within } from 'storybook/test'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from './accordion'
import { Button } from './button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from './command'
import { ConfirmDialog } from './confirm-dialog'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle, DialogTrigger } from './dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from './dropdown-menu'
import { Popover, PopoverContent, PopoverTrigger } from './popover'
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetTitle, SheetTrigger } from './sheet'

const meta = { title: 'Design System/Overlays', parameters: { layout: 'centered' } } satisfies Meta
export default meta
type Story = StoryObj<typeof meta>

const config = (extra: Record<string, unknown> = {}) => ({ designSystem: {
  pointerTargets: ['[data-ds-trigger]'],
  browserAssertions: [{ selector: '[data-ds-trigger]', attribute: 'data-ds-trigger', value: 'true' }],
  ...extra,
} })

const modalForcedColors = (selector: string, focus = `${selector} [data-ds-action]`) => ({
  boundaries: [selector],
  focus: [focus],
})

export const DialogContract: Story = {
  parameters: config({ motionTargets: ['[role=dialog]'], forcedColors: modalForcedColors('[role=dialog]') }),
  render: () => <Dialog><DialogTrigger data-ds-trigger="true" className="h-11 px-[var(--space-3)]">Open dialog</DialogTrigger><DialogContent><DialogTitle>Dialog</DialogTitle><DialogDescription>Scrollable modal content</DialogDescription></DialogContent></Dialog>,
  play: async ({ canvasElement }) => { const canvas = within(canvasElement); await userEvent.click(canvas.getByRole('button', { name: 'Open dialog' })); await expect(within(document.body).getByRole('dialog')).toBeVisible() },
}
export const DialogKeyboard: Story = {
  parameters: config({ keyboard: [{ trigger: '[data-ds-trigger]', key: 'Enter', expectFocus: '[role=dialog] button' }] }),
  render: DialogContract.render,
}
export const DialogFooterTargets: Story = {
  parameters: { designSystem: { pointerTargets: ['[data-dialog-cancel]', '[data-dialog-save]'] } },
  render: () => <Dialog open><DialogContent><DialogTitle>Edit record</DialogTitle><DialogDescription>Footer target spacing</DialogDescription><DialogFooter><Button data-dialog-cancel variant="outline">Cancel</Button><Button data-dialog-save>Save</Button></DialogFooter></DialogContent></Dialog>,
}
export const SheetContract: Story = {
  parameters: config({ motionTargets: ['[role=dialog]'], forcedColors: modalForcedColors('[role=dialog]') }),
  render: () => <Sheet><SheetTrigger data-ds-trigger="true" className="h-11 px-[var(--space-3)]">Open sheet</SheetTrigger><SheetContent size="md"><SheetTitle>Sheet</SheetTitle><SheetDescription>Named medium size</SheetDescription></SheetContent></Sheet>,
  play: async ({ canvasElement }) => { await userEvent.click(within(canvasElement).getByRole('button', { name: 'Open sheet' })); await expect(within(document.body).getByRole('dialog')).toBeVisible() },
}
export const SheetKeyboard: Story = {
  parameters: config({ keyboard: [{ trigger: '[data-ds-trigger]', key: 'Enter', expectFocus: '[role=dialog] button' }] }),
  render: SheetContract.render,
}
export const SheetFooterTargets: Story = {
  parameters: { designSystem: { pointerTargets: ['[data-sheet-cancel]', '[data-sheet-save]'] } },
  render: () => <Sheet open><SheetContent size="md"><SheetTitle>Edit details</SheetTitle><SheetDescription>Footer target spacing</SheetDescription><SheetFooter><Button data-sheet-cancel variant="outline">Cancel</Button><Button data-sheet-save>Save</Button></SheetFooter></SheetContent></Sheet>,
}

function ControlledConfirmationFixture() {
  const [open, setOpen] = useState(true)
  return <ConfirmDialog open={open} onOpenChange={setOpen} title="Delete?" description="This cannot be undone." cancelLabel={<span data-ds-cancel>Cancel</span>} confirmLabel={<span data-ds-confirm>Delete</span>} onConfirm={() => {}} destructive />
}

function ControlledConfirmationKeyboardFixture() {
  const [open, setOpen] = useState(false)
  return <><button type="button" tabIndex={0} data-confirm-dialog-opener onClick={() => setOpen(true)}>Delete record</button><ConfirmDialog open={open} onOpenChange={setOpen} title="Delete?" description="This cannot be undone." cancelLabel="Cancel" confirmLabel="Delete" onConfirm={() => {}} destructive /></>
}

export const ConfirmationContract: Story = {
  parameters: { designSystem: { keyboard: [{ trigger: '[data-confirm-dialog-cancel]', key: 'Space', expectFocus: '[data-confirm-dialog-cancel]' }], pointerTargets: ['[data-confirm-dialog-cancel]', '[data-confirm-dialog-action]'], motionTargets: ['[role=alertdialog]'], forcedColors: modalForcedColors('[role=alertdialog]', '[data-confirm-dialog-cancel]'), browserAssertions: [{ selector: '[role=alertdialog]', attribute: 'role', value: 'alertdialog' }] } },
  render: () => <ControlledConfirmationFixture />,
  play: async () => { const body = within(document.body); await userEvent.click(body.getByRole('button', { name: 'Delete' })); await expect(body.getByRole('alertdialog')).toBeVisible() },
}
export const ConfirmationKeyboard: Story = {
  parameters: { designSystem: { keyboard: [
    { trigger: '[data-confirm-dialog-opener]', key: 'Enter', expectFocus: '[data-confirm-dialog-cancel]' },
    { trigger: '[data-confirm-dialog-cancel]', key: 'Space', expectFocus: '[data-confirm-dialog-opener]' },
    { trigger: '[data-confirm-dialog-opener]', key: 'Enter', expectFocus: '[data-confirm-dialog-cancel]' },
    { trigger: '[data-confirm-dialog-cancel]', key: 'Escape', expectFocus: '[data-confirm-dialog-opener]' },
  ] } },
  render: () => <ControlledConfirmationKeyboardFixture />,
}
function PendingConfirmationFixture() {
  const [open, setOpen] = useState(false)
  return <><button type="button" tabIndex={0} data-pending-confirmation-opener onClick={() => setOpen(true)}>Open pending confirmation</button><ConfirmDialog open={open} pending onOpenChange={setOpen} title="Deleting record" description="The operation continues if you dismiss this dialog." confirmLabel="Delete" onConfirm={() => {}} /></>
}

export const PendingConfirmationEscape: Story = {
  parameters: { designSystem: { keyboard: [
    { trigger: '[data-pending-confirmation-opener]', key: 'Enter', expectFocus: '[role=alertdialog]' },
    { trigger: '[role=alertdialog]', key: 'Escape', expectFocus: '[data-pending-confirmation-opener]' },
  ] } },
  render: () => <PendingConfirmationFixture />,
  play: async ({ canvasElement }) => {
    const opener = within(canvasElement).getByRole('button', { name: 'Open pending confirmation' })
    await userEvent.click(opener)
    await expect(within(document.body).getByRole('alertdialog')).toHaveAttribute('aria-busy', 'true')
    await expect(within(document.body).getByRole('button', { name: /^Delete$/ })).toBeDisabled()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => {
      expect(within(document.body).queryByRole('alertdialog')).not.toBeInTheDocument()
      expect(opener).toHaveFocus()
    })
  },
}

export const MenuContract: Story = {
  // The addon cannot classify individual axe nodes. Playwright replaces this
  // one rule with the stricter reviewed proof in accessibility-exceptions.json.
  parameters: { ...config({ motionTargets: ['[role=menu]'], forcedColors: { boundaries: ['[role=menu]'] } }), a11y: { config: { rules: [{ id: 'aria-hidden-focus', enabled: false }] } } },
  render: () => <DropdownMenu><DropdownMenuTrigger data-ds-trigger="true" className="h-11 px-[var(--space-3)]">Menu</DropdownMenuTrigger><DropdownMenuContent><DropdownMenuItem>Item</DropdownMenuItem><DropdownMenuSub><DropdownMenuSubTrigger>More</DropdownMenuSubTrigger><DropdownMenuSubContent><DropdownMenuItem>Nested</DropdownMenuItem></DropdownMenuSubContent></DropdownMenuSub></DropdownMenuContent></DropdownMenu>,
  play: async ({ canvasElement }) => { await userEvent.click(within(canvasElement).getByRole('button', { name: 'Menu' })); await expect(within(document.body).getByRole('menuitem', { name: 'Item' })).toBeVisible() },
}
export const MenuKeyboard: Story = {
  parameters: config({ keyboard: [{ trigger: '[data-ds-trigger]', key: 'Enter', expectFocus: '[role=menuitem]:not([aria-haspopup])' }] }),
  render: MenuContract.render,
}
export const PopoverContract: Story = {
  parameters: config({ motionTargets: ['[data-radix-popper-content-wrapper]'], forcedColors: { boundaries: ['[role=dialog]'] } }),
  render: () => <Popover><PopoverTrigger data-ds-trigger="true" className="h-11 px-[var(--space-3)]">Popover</PopoverTrigger><PopoverContent aria-label="Popover details">Popover content</PopoverContent></Popover>,
  play: async ({ canvasElement }) => { await userEvent.click(within(canvasElement).getByRole('button', { name: 'Popover' })); await expect(within(document.body).getByText('Popover content')).toBeVisible() },
}
export const PopoverKeyboard: Story = {
  parameters: config({ keyboard: [{ trigger: '[data-ds-trigger]', key: 'Enter', expectFocus: '[role=dialog]' }] }),
  render: PopoverContract.render,
}
export const CommandContract: Story = {
  parameters: config({ keyboard: [{ trigger: '[data-ds-trigger]', key: 'ArrowDown', expectFocus: '[data-ds-trigger]' }], motionTargets: ['[cmdk-root]'], forcedColors: { differences: [{ cue: 'foreground', selector: '[cmdk-input]', property: 'color', againstSelector: '[cmdk-root]', againstProperty: 'backgroundColor' }] } }),
  render: () => <Command><CommandInput data-ds-trigger="true" className="min-h-11" /><CommandList><CommandEmpty>No results</CommandEmpty><CommandGroup><CommandItem>Alpha</CommandItem></CommandGroup></CommandList></Command>,
  play: async ({ canvasElement }) => { const canvas = within(canvasElement); const input = canvas.getByRole('combobox'); input.focus(); await userEvent.keyboard('{ArrowDown}'); await expect(canvas.getByText('Alpha')).toHaveAttribute('data-selected', 'true') },
}
export const AccordionContract: Story = {
  parameters: config({ motionTargets: ['[data-ds-content]'], forcedColors: { differences: [{ cue: 'foreground', selector: '[data-ds-trigger]', property: 'color', againstSelector: 'body', againstProperty: 'backgroundColor' }] } }),
  render: () => <Accordion type="single" collapsible><AccordionItem value="one"><AccordionTrigger data-ds-trigger="true" className="min-h-11">Section</AccordionTrigger><AccordionContent data-ds-content>Content</AccordionContent></AccordionItem></Accordion>,
  play: async ({ canvasElement }) => { const trigger = within(canvasElement).getByRole('button', { name: 'Section' }); await userEvent.click(trigger); await expect(trigger).toHaveAttribute('aria-expanded', 'true') },
}
export const AccordionKeyboard: Story = {
  parameters: config({ keyboard: [{ trigger: '[data-ds-trigger]', key: 'Enter', expectExpanded: true }] }),
  render: AccordionContract.render,
}
