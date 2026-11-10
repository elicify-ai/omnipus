import type { Meta, StoryObj } from '@storybook/react-vite'
import { WordBoundaryText } from './word-boundary-text'
const meta = {
  title: 'Design System/WordBoundaryText', component: WordBoundaryText,
  args: { text: 'Post-publish verification interruption end-state' },
  render: (args) => <div data-testid="word-sample" className="w-40"><WordBoundaryText {...args} className="whitespace-normal break-normal wrap-break-word" /></div>,
  parameters: { designSystem: { forcedColorTargets: ['[data-testid="word-sample"]'], forcedColors: { differences: [{ cue: 'foreground', selector: '[data-testid="word-sample"]', property: 'color', againstSelector: '#storybook-root', againstProperty: 'backgroundColor' }] }, browserAssertions: [{ selector: '[data-testid="word-sample"]', text: 'Post-publish verification interruption end-state' }], reflowExemptions: [] } },
} satisfies Meta<typeof WordBoundaryText>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = {}
export const LongWord: Story = { args: { text: `https://example.com/${'x'.repeat(180)}` } }
