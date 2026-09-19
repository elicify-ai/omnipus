import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, fn, userEvent, within } from 'storybook/test'
import { Tray } from '@phosphor-icons/react'
import { EmptyState } from './empty-state'

const meta = {
  title: 'Design System/EmptyState', component: EmptyState,
  args: { icon: <Tray size={32} />, message: 'No notifications yet' },
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="empty-state"] button', key: 'Enter', expectFocus: '[data-testid="empty-state"] button' }],
    pointerTargets: ['[data-testid="empty-state"] button'], forcedColorTargets: ['[data-testid="empty-state"]'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="empty-state"]',property:'color',againstSelector:'[data-testid="empty-state"]',againstProperty:'backgroundColor'}]},reflowExemptions: [],
    browserAssertions: [{ selector: '[data-testid="empty-state"]', text: 'No notifications yet' }],
  } },
  render: (args) => <div data-testid="empty-state"><EmptyState {...args} /></div>,
} satisfies Meta<typeof EmptyState>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = {}
export const WithAction: Story = { args: { actionLabel: 'Add notification', onAction: fn() }, play: async ({ args, canvasElement }) => { await userEvent.click(within(canvasElement).getByRole('button', { name: 'Add notification' })); await expect(args.onAction).toHaveBeenCalledOnce() } }
