import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, within } from 'storybook/test'
import { Spinner } from './spinner'

const meta = {
  title: 'Design System/Spinner',
  component: Spinner,
  args: { label: 'Loading connectors' },
  parameters: {
    designSystem: {
      motionTargets: ['[data-testid="spinner"] svg'],
      forcedColors: { differences: [{ cue: 'foreground', selector: '[data-testid="spinner"]', property: 'color', againstSelector: '[data-testid="spinner"]', againstProperty: 'backgroundColor' }] },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-testid="spinner"] [role="status"]', attribute: 'aria-label', value: 'Loading connectors' }],
    },
  },
  render: (args) => <div data-testid="spinner" className="inline-flex text-[var(--color-secondary)]"><Spinner {...args} /></div>,
} satisfies Meta<typeof Spinner>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  play: async ({ canvasElement }) => {
    await expect(within(canvasElement).getByRole('status', { name: 'Loading connectors' })).toBeInTheDocument()
  },
}
export const Large: Story = { args: { size: 'lg' } }
