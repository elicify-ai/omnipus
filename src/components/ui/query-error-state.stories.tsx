import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, fn, userEvent, within } from 'storybook/test'
import { QueryErrorState } from './query-error-state'

const meta = {
  title: 'Design System/QueryErrorState', component: QueryErrorState,
  args: { message: 'Could not load tasks.', layout: 'fill', testId: 'query-error' },
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="query-error"] button', key: 'Enter', expectFocus: '[data-testid="query-error"] button' }], pointerTargets: ['[data-testid="query-error"] button'],
    forcedColorTargets: ['[data-testid="query-error"] p'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="query-error"] p',property:'color',againstSelector:'body',againstProperty:'backgroundColor',actualSystemColor:'CanvasText',againstSystemColor:'Canvas'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="query-error"]', text: 'Could not load tasks.' }],
  } },
} satisfies Meta<typeof QueryErrorState>
export default meta
type Story = StoryObj<typeof meta>
export const Fill: Story = {}
export const Absolute: Story = { args: { layout: 'absolute' } }
export const Retry: Story = { args: { onRetry: fn() }, play: async ({ args, canvasElement }) => { await userEvent.click(within(canvasElement).getByRole('button', { name: 'Retry' })); await expect(args.onRetry).toHaveBeenCalledOnce() } }
