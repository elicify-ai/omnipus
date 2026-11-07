import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, waitFor, within } from 'storybook/test'
import { CopyButton } from './copy-button'

const meta = {
  title: 'Design System/CopyButton',
  component: CopyButton,
  args: { value: 'https://example.invalid/hooks/abc123', label: 'Copy webhook URL' },
  parameters: {
    designSystem: {
      keyboard: [{ trigger: '[data-testid="copy-button-story"] button', key: 'Enter', expectFocus: '[data-testid="copy-button-story"] button' }],
      pointerTargets: ['[data-testid="copy-button-story"] button'],
      forcedColors: { focus: ['[data-testid="copy-button-story"] button'], differences: [{ cue: 'foreground', selector: '[data-testid="copy-button-story"] button', property: 'color', againstSelector: '[data-testid="copy-button-story"] button', againstProperty: 'backgroundColor' }] },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-testid="copy-button-story"] button', attribute: 'aria-label', value: 'Copy webhook URL' }],
    },
  },
  render: (args) => <div data-testid="copy-button-story" className="inline-flex"><CopyButton {...args} /></div>,
} satisfies Meta<typeof CopyButton>
export default meta
type Story = StoryObj<typeof meta>

// The play function asserts only what holds whatever the browser's clipboard
// permission: the click produces an announced outcome (Copied or Copy
// failed — never silence) and the control keeps its name.
export const Default: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const button = canvas.getByRole('button', { name: 'Copy webhook URL' })
    await userEvent.click(button)
    await waitFor(() => expect(['copied', 'failed']).toContain(button.getAttribute('data-copy-state')))
    await expect(canvas.getByRole('button', { name: 'Copy webhook URL' })).toBe(button)
  },
}
