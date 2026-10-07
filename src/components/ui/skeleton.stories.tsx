import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, waitFor, within } from 'storybook/test'
import { motionResolvedTokens } from '../../design-system/tokens'
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
// Real timer in a real browser: the indicator appears after motion.loading.delay, and webkit under CI
// contention was measured at 1238-1362 ms against the old 1000 ms default. Deadline = token delay + a 4600 ms
// margin (5000 ms at the 400 ms token). The exact 399/400 ms boundary is pinned by skeleton.test.tsx (fake timers).
const loadingDelayMs = Number.parseFloat(motionResolvedTokens['motion.loading.delay'])
const loadingWaitTimeoutMs = loadingDelayMs + 4600
export const Loading: Story = {
  args: { pending: true },
  play: async ({ canvasElement }) => {
    await waitFor(
      () => expect(within(canvasElement).getByTestId('skeleton')).toHaveAttribute('data-visible', 'true'),
      { timeout: loadingWaitTimeoutMs },
    )
  },
}
export const ReservedBeforeDelay: Story = { args: { pending: false } }
