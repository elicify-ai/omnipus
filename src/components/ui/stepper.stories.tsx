import type { Meta, StoryObj } from '@storybook/react-vite'
import { Stepper } from './stepper'

const meta = {
  title: 'Design System/Stepper',
  component: Stepper,
  args: { steps: ['Choose file', 'Preview', 'Confirm', 'Result'], current: 1, label: 'Import steps' },
  parameters: {
    designSystem: {
      forcedColors: { boundaries: ['[data-slot="stepper"] li[aria-current]'] },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-slot="stepper"] li[aria-current]', attribute: 'aria-current', value: 'step' }],
    },
  },
} satisfies Meta<typeof Stepper>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
export const FirstStep: Story = {
  args: { steps: ['Choose agent', 'Review', 'Download'], current: 0, label: 'Export steps' },
}
