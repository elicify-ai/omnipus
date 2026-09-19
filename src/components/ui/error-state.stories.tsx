import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, fn, userEvent, within } from 'storybook/test'
import { ErrorState } from './error-state'

const meta = {
  title: 'Design System/ErrorState', component: ErrorState,
  render: (args) => <div data-testid="error-state"><ErrorState {...args} /></div>,
  args: { message: 'Could not load channels.' },
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="error-state"] button', key: 'Enter', expectFocus: '[data-testid="error-state"] button' }], pointerTargets: ['[data-testid="error-state"] button'],
    forcedColorTargets: ['[data-testid="error-state"] [role="alert"] p'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="error-state"] [role="alert"] p',property:'color',againstSelector:'body',againstProperty:'backgroundColor',actualSystemColor:'CanvasText',againstSystemColor:'Canvas'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="error-state"]', text: 'Could not load channels.' }],
  } },
} satisfies Meta<typeof ErrorState>
export default meta
type Story = StoryObj<typeof meta>
export const MessageOnly: Story = {}
export const Retry: Story = { args: { onRetry: fn() }, play: async ({ args, canvasElement }) => { await userEvent.click(within(canvasElement).getByRole('button', { name: 'Retry' })); await expect(args.onRetry).toHaveBeenCalledOnce() } }
