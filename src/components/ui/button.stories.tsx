import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, fn, userEvent, within } from 'storybook/test'
import { Button } from './button'
import { useState } from 'react'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from './dialog'

const meta = {
  title: 'Design System/Button', component: Button,
  render: (args) => <Button {...args} data-testid="button" />,
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="button"]', key: 'Enter', expectFocus: '[data-testid="button"]' }],
    pointerTargets: ['[data-testid="button"]'], motionTargets: ['[data-testid="button"]'],
    forcedColorTargets: ['[data-testid="button"]'],
    forcedColors: {
      boundaries: ['[data-testid="button"]'],
      differences: [{ cue: 'foreground', selector: '[data-testid="button"]', property: 'color', againstSelector: '[data-testid="button"]', againstProperty: 'backgroundColor', actualSystemColor: 'ButtonText', againstSystemColor: 'ButtonFace' }],
      focus: ['[data-testid="button"]'],
    },
    reflowExemptions: [],
    browserAssertions: [{ selector: '[data-testid="button"]', attribute: 'type', value: 'button' }],
  } },
} satisfies Meta<typeof Button>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = { args: { children: 'Continue' } }
export const Destructive: Story = { args: { ...Default.args, children: 'Delete', variant: 'destructive' } }
export const Outline: Story = { args: { ...Default.args, variant: 'outline' } }
export const Secondary: Story = { args: { ...Default.args, variant: 'secondary' } }
export const Ghost: Story = { args: { ...Default.args, variant: 'ghost' } }
export const Link: Story = { args: { ...Default.args, variant: 'link' } }
export const Small: Story = { args: { ...Default.args, size: 'sm' } }
export const Large: Story = { args: { ...Default.args, size: 'lg' } }
export const Disabled: Story = { args: { ...Default.args, disabled: true } }
export const Pending: Story = { args: { ...Default.args, actionState: 'pending' } }
export const Success: Story = {
  args: { ...Default.args, actionState: 'success' },
  play: async ({ canvasElement }) => {
    const button = within(canvasElement).getByRole('button', { name: 'Continue' })
    await expect(button).toHaveAttribute('aria-description', 'Action succeeded')
    await expect(button.querySelector('[data-action-feedback="success"]')).toBeInTheDocument()
  },
}
export const Error: Story = {
  args: { ...Default.args, children: 'Try again', actionState: 'error', onClick: fn() },
  play: async ({ args, canvasElement }) => {
    const button = within(canvasElement).getByRole('button', { name: 'Try again' })
    await expect(button).toHaveAttribute('aria-description', 'Action failed')
    await expect(button.querySelector('[data-action-feedback="error"]')).toBeInTheDocument()
    await userEvent.click(button)
    await expect(args.onClick).toHaveBeenCalledOnce()
  },
}
export const Dense: Story = { args: { ...Default.args, size: 'sm' }, parameters: { viewport: { defaultViewport: 'tablet' } } }
export const NarrowViewport: Story = { args: Default.args, parameters: { viewport: { defaultViewport: 'mobile1' } } }
export const AdjacentTargets: Story = {
  args: Default.args,
  render: () => <div className="flex gap-[var(--space-control-gap)]"><Button data-testid="previous">Previous</Button><Button data-testid="next">Next</Button></div>,
  parameters: { designSystem: { pointerTargets: ['[data-testid="previous"]', '[data-testid="next"]'] } },
}

export const IdleInvokesAction: Story = {
  args: { children: 'Run', onClick: fn() },
  play: async ({ args, canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole('button', { name: 'Run' }))
    await expect(args.onClick).toHaveBeenCalledOnce()
  },
}
export const PendingBlocksAction: Story = {
  args: { children: 'Save', actionState: 'pending', onClick: fn() },
  play: async ({ args, canvasElement }) => {
    const button = within(canvasElement).getByRole('button', { name: 'Save' })
    await expect(userEvent.click(button)).rejects.toThrow(/pointer-events: none/)
    await expect(button).toBeDisabled()
    await expect(args.onClick).not.toHaveBeenCalled()
  },
}
export const DisabledLinkBlocksAction: Story = {
  args: { asChild: true, disabled: true, children: <a href="#settings" tabIndex={0}>Settings</a>, onClick: fn() },
  play: async ({ args, canvasElement }) => {
    const link = within(canvasElement).getByRole('link', { name: 'Settings' })
    await userEvent.click(link)
    await expect(link).toHaveAttribute('href', '#settings')
    await expect(args.onClick).not.toHaveBeenCalled()
  },
}
export const DisabledLinkBlocksAuxiliaryAction: Story = {
  render: () => <><Button asChild disabled><a data-testid="disabled-link" href="#auxiliary-settings">Settings</a></Button><Button asChild actionState="pending"><a data-testid="pending-link" href="#auxiliary-pending">Pending settings</a></Button><Button asChild><a data-testid="enabled-link" href="#auxiliary-enabled">Enabled settings</a></Button></>,
  parameters: { designSystem: { browserAssertions: [{ selector: '[data-testid="disabled-link"]', attribute: 'href', value: '#auxiliary-settings' }] } },
  play: async ({ canvasElement }) => {
    const link = within(canvasElement).getByRole('link', { name: 'Settings' })
    const allowed = link.dispatchEvent(new MouseEvent('auxclick', { button: 1, bubbles: true, cancelable: true }))
    await expect(allowed).toBe(false)
  },
}
export const ExplicitSubmit: Story = {
  render: (args) => <form><Button {...args}>Save</Button></form>,
  args: { type: 'submit' },
  play: async ({ canvasElement }) => {
    await expect(within(canvasElement).getByRole('button', { name: 'Save' })).toHaveAttribute('type', 'submit')
  },
}

function ModalActionFeedbackFixture() {
  const [saved, setSaved] = useState(false)
  return <Dialog open><DialogContent><DialogTitle>Edit details</DialogTitle><DialogDescription>Save your changes</DialogDescription><Button data-testid="button" actionState={saved ? 'success' : 'idle'} onClick={() => setSaved(true)}>Save</Button></DialogContent></Dialog>
}

export const ModalActionFeedback: Story = {
  render: () => <ModalActionFeedbackFixture />,
  play: async () => {
    const dialog = within(document.body).getByRole('dialog')
    const region = within(dialog).getByRole('status')
    await expect(region).toBeEmptyDOMElement()
    await userEvent.click(within(dialog).getByRole('button', { name: /^Save$/ }))
    await expect(within(dialog).getByRole('status')).toBe(region)
    await expect(region).toHaveTextContent('Action succeeded')
    await expect(region.closest('[aria-hidden="true"]')).toBeNull()
  },
}

function ComposedFormExample() {
  const [submissions, setSubmissions] = useState(0)
  const [resets, setResets] = useState(0)
  return (
    <form aria-label="Composed actions" onSubmit={(event) => { event.preventDefault(); setSubmissions((count) => count + 1) }} onReset={() => setResets((count) => count + 1)}>
      <input aria-label="Name" defaultValue="Initial" tabIndex={0} />
      <Button asChild type="button"><button tabIndex={0}>Cancel</button></Button>
      <Button asChild type="reset"><button>Reset</button></Button>
      <Button asChild type="submit"><button>Save</button></Button>
      <p data-testid="form-counts">Submissions: {submissions}; Resets: {resets}</p>
    </form>
  )
}

export const ComposedFormTypes: Story = {
  render: () => <ComposedFormExample />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const input = canvas.getByRole('textbox', { name: 'Name' })
    await userEvent.clear(input)
    await userEvent.type(input, 'Edited')
    await userEvent.click(canvas.getByRole('button', { name: 'Cancel' }))
    await expect(canvas.getByTestId('form-counts')).toHaveTextContent('Submissions: 0; Resets: 0')
    await expect(input).toHaveValue('Edited')
    await userEvent.click(canvas.getByRole('button', { name: 'Reset' }))
    await expect(canvas.getByTestId('form-counts')).toHaveTextContent('Submissions: 0; Resets: 1')
    await expect(input).toHaveValue('Initial')
    await userEvent.click(canvas.getByRole('button', { name: 'Save' }))
    await expect(canvas.getByTestId('form-counts')).toHaveTextContent('Submissions: 1; Resets: 1')
  },
}
