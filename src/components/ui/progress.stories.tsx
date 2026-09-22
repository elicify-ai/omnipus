import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, within } from 'storybook/test'
import { Progress } from './progress'

const meta = {
  title: 'Design System/Progress', component: Progress,
  render: (args) => <Progress {...args} data-testid="progress" />,
  parameters: { designSystem: {
    motionTargets: ['[data-testid="progress"] > *'], forcedColorTargets: ['[data-testid="progress"]'],
    forcedColors: { differences: [{ cue: 'progress', selector: '[data-testid="progress"] > *', property: 'backgroundColor', againstSelector: '[data-testid="progress"]', againstProperty: 'backgroundColor', actualSystemColor: 'Highlight', againstSystemColor: 'Canvas' }] },
    reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="progress"]', attribute: 'aria-valuenow', value: '40' }],
  } },
} satisfies Meta<typeof Progress>
export default meta
type Story = StoryObj<typeof meta>
export const Determinate: Story = { args: { value: 40, max: 100, label: 'Upload progress' }, play: async ({ canvasElement }) => { await expect(within(canvasElement).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40') } }
export const Zero: Story = { args: { value: 0, label: 'Upload progress' } }
export const Indeterminate: Story = { args: { value: null, label: 'Indexing progress' } }

const conflictingNumericAttributes: Record<string, number> = {
  'aria-valuenow': 75, 'aria-valuemin': 10, 'aria-valuemax': 50,
}

export const ProtectedDeterminate: Story = {
  render: () => <Progress {...conflictingNumericAttributes} value={25} max={100} label="Upload progress" data-testid="progress" />,
  play: async ({ canvasElement }) => {
    const bar = within(canvasElement).getByRole('progressbar', { name: 'Upload progress' })
    await expect(bar).toHaveAttribute('aria-valuenow', '25')
    await expect(bar).toHaveAttribute('aria-valuemin', '0')
    await expect(bar).toHaveAttribute('aria-valuemax', '100')
    await expect(bar).toHaveAttribute('aria-valuetext', '25%')
    const indicator = bar.firstElementChild as HTMLElement
    await expect(indicator.style.transform).toBe('translateX(-75%)')
    const trackBounds = bar.getBoundingClientRect()
    const fillBounds = indicator.getBoundingClientRect()
    await expect((fillBounds.right - trackBounds.left) / trackBounds.width).toBeCloseTo(0.25, 3)
  },
}

export const ProtectedIndeterminate: Story = {
  render: () => <Progress {...conflictingNumericAttributes} value={null} label="Indexing progress" data-testid="progress" />,
  play: async ({ canvasElement }) => {
    const bar = within(canvasElement).getByRole('progressbar', { name: 'Indexing progress' })
    await expect(bar).not.toHaveAttribute('aria-valuenow')
    await expect(bar).toHaveAttribute('aria-valuemin', '0')
    await expect(bar).toHaveAttribute('aria-valuemax', '100')
    await expect(bar).toHaveAttribute('data-state', 'indeterminate')
    await expect(bar.firstElementChild).not.toHaveAttribute('style')
  },
}
