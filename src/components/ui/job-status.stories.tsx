import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, fn, userEvent, within } from 'storybook/test'
import { JobStatus } from './job-status'

const meta = {
  title: 'Design System/JobStatus', component: JobStatus,
  args: { status: 'progress', label: 'Index documents', progress: 35, onCancel: fn(), onBackground: fn() },
  render: (args) => <div data-testid="job-status"><JobStatus {...args} /></div>,
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="job-status"] button:first-of-type', key: 'Enter', expectFocus: '[data-testid="job-status"] button:first-of-type' }], pointerTargets: ['[data-testid="job-status"] button:first-of-type', '[data-testid="job-status"] button:nth-of-type(2)'], motionTargets: ['[data-testid="job-status"] [role="progressbar"] > *'],
    forcedColorTargets: ['[data-testid="job-status"]'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="job-status"]',property:'color',againstSelector:'[data-testid="job-status"]',againstProperty:'backgroundColor'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="job-status"]', text: '35%' }],
  } },
} satisfies Meta<typeof JobStatus>
export default meta
type Story = StoryObj<typeof meta>
export const Progressing: Story = { play: async ({ args, canvasElement }) => { await userEvent.click(within(canvasElement).getByRole('button', { name: 'Cancel' })); await expect(args.onCancel).toHaveBeenCalledOnce() } }
export const Queued: Story = { args: { status: 'queued', progress: undefined } }
export const RunningUnknown: Story = { args: { status: 'running', progress: undefined } }
export const Paused: Story = { args: { status: 'paused' } }
export const Failed: Story = { args: { status: 'failed', onRetry: fn(), onCancel: undefined, onBackground: undefined } }
export const Complete: Story = { args: { status: 'complete', progress: 100, onCancel: undefined, onBackground: undefined } }
export const Cancelled: Story = { args: { status: 'cancelled', onRetry: fn(), onCancel: undefined, onBackground: undefined } }
