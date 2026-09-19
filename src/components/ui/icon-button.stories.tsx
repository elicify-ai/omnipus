import { X } from '@phosphor-icons/react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, fn, userEvent, within } from 'storybook/test'
import { IconButton } from './icon-button'

const meta = {
  title: 'Design System/IconButton', component: IconButton,
  render: (args) => <IconButton {...args} data-testid="icon-button" />,
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="icon-button"]', key: 'Space', expectFocus: '[data-testid="icon-button"]' }],
    pointerTargets: ['[data-testid="icon-button"]'], motionTargets: ['[data-testid="icon-button"]'],
    forcedColorTargets: ['[data-testid="icon-button"]'], forcedColors:{boundaries:['[data-testid="icon-button"]'],differences:[{cue:'foreground',selector:'[data-testid="icon-button"]',property:'color',againstSelector:'[data-testid="icon-button"]',againstProperty:'backgroundColor'}],focus:['[data-testid="icon-button"]']},reflowExemptions: [],
    browserAssertions: [{ selector: '[data-testid="icon-button"]', attribute: 'aria-label', value: 'Close' }],
  } },
} satisfies Meta<typeof IconButton>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = { args: { 'aria-label': 'Close', children: <X aria-hidden /> } }
export const Small: Story = { args: { ...Default.args, size: 'sm' } }
export const Large: Story = { args: { ...Default.args, size: 'lg' } }
export const Disabled: Story = { args: { ...Default.args, disabled: true } }
export const Pending: Story = { args: { ...Default.args, actionState: 'pending' } }
export const Destructive: Story = { args: { ...Default.args, variant: 'destructive' } }
export const Outline: Story = { args: { ...Default.args, variant: 'outline' } }
export const Secondary: Story = { args: { ...Default.args, variant: 'secondary' } }
export const Ghost: Story = { args: { ...Default.args, variant: 'ghost' } }
export const Success: Story = { args: { ...Default.args, actionState: 'success' } }
export const Error: Story = { args: { ...Default.args, actionState: 'error' } }
export const InvokesAction: Story = {
  args: { ...Default.args, onClick: fn() },
  play: async ({ args, canvasElement }) => {
    await userEvent.click(within(canvasElement).getByRole('button', { name: 'Close' }))
    await expect(args.onClick).toHaveBeenCalledOnce()
  },
}
export const FocusVisible: Story = { args: Default.args, play: async ({ canvasElement }) => { (canvasElement.querySelector('[data-testid="icon-button"]') as HTMLElement)?.focus() } }
export const AdjacentTargets: Story = {
  args: Default.args,
  render: () => <div className="flex gap-[var(--space-control-gap)] [@media(pointer:coarse)]:gap-[var(--space-4)]"><IconButton data-testid="previous" aria-label="Previous"><X aria-hidden /></IconButton><IconButton data-testid="next" aria-label="Next"><X aria-hidden /></IconButton></div>,
  parameters: { designSystem: { pointerTargets: ['[data-testid="previous"]', '[data-testid="next"]'] } },
}
export const Dense: Story = { args: { ...Default.args, size: 'sm' }, parameters: { viewport: { defaultViewport: 'tablet' } } }
export const NarrowViewport: Story = { args: Default.args, parameters: { viewport: { defaultViewport: 'mobile1' } } }
