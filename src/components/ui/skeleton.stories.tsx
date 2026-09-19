import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, waitFor, within } from 'storybook/test'
import { Skeleton } from './skeleton'

const meta = {
  title: 'Design System/Skeleton', component: Skeleton,
  render: (args) => <Skeleton {...args} data-testid="skeleton" className="h-16 w-full" />,
  parameters: { designSystem: {
    motionTargets: ['[data-testid="skeleton"]'], forcedColorTargets: ['[data-testid="skeleton"]'], forcedColors:{differences:[{cue:'indicator',selector:'[data-testid="skeleton"]',property:'backgroundColor',againstSelector:'[data-design-system-config]',againstProperty:'backgroundColor'}]},reflowExemptions: [],
    browserAssertions: [{ selector: '[data-testid="skeleton"]', attribute: 'aria-hidden', value: 'true' }],
  } },
} satisfies Meta<typeof Skeleton>
export default meta
type Story = StoryObj<typeof meta>
export const Loading: Story = { args: { pending: true }, play: async ({ canvasElement }) => { await waitFor(() => expect(within(canvasElement).getByTestId('skeleton')).toHaveAttribute('data-visible', 'true')) } }
export const ReservedBeforeDelay: Story = { args: { pending: false } }
