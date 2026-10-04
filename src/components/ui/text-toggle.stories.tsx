import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { TextToggle } from './text-toggle'

function Harness({
  initial = false,
  disabled = false,
}: {
  initial?: boolean
  disabled?: boolean
}) {
  const [pressed, setPressed] = useState(initial)
  return (
    <TextToggle
      pressed={pressed}
      disabled={disabled}
      onPressedChange={setPressed}
      data-testid="text-toggle"
    >
      Auto
    </TextToggle>
  )
}

const meta = {
  title: 'Design System/TextToggle',
  component: TextToggle,
  args: {
    pressed: false,
    onPressedChange: () => {},
    children: 'Auto',
  },
  render: () => <Harness />,
  parameters: {
    designSystem: {
      keyboard: [
        { trigger: '[data-testid="text-toggle"]', key: 'Space', expectFocus: '[data-testid="text-toggle"]' },
      ],
      pointerTargets: ['[data-testid="text-toggle"]'],
      motionTargets: ['[data-testid="text-toggle"]'],
      forcedColorTargets: ['[data-testid="text-toggle"]'],
      forcedColors: {
        boundaries: ['[data-testid="text-toggle"]'],
        differences: [
          {
            cue: 'foreground',
            selector: '[data-testid="text-toggle"]',
            property: 'color',
            againstSelector: '[data-testid="text-toggle"]',
            againstProperty: 'backgroundColor',
            actualSystemColor: 'HighlightText',
            againstSystemColor: 'Highlight',
          },
        ],
        focus: ['[data-testid="text-toggle"]'],
        states: [{ selector: '[data-testid="text-toggle"]', attribute: 'data-state', value: 'on' }],
        stateTransitions: [
          {
            selector: '[data-testid="text-toggle"]',
            attribute: 'data-state',
            from: 'on',
            action: 'click',
            to: 'off',
            differences: [
              {
                cue: 'foreground',
                selector: '[data-testid="text-toggle"]',
                property: 'color',
                againstSelector: '[data-testid="text-toggle"]',
                againstProperty: 'backgroundColor',
                actualSystemColor: 'CanvasText',
                againstSystemColor: 'Canvas',
              },
            ],
          },
        ],
      },
      reflowExemptions: [],
      browserAssertions: [
        { selector: '[data-testid="text-toggle"]', attribute: 'aria-pressed', value: 'true' },
        { selector: '[data-testid="text-toggle"]', text: 'Auto' },
      ],
    },
  },
} satisfies Meta<typeof TextToggle>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const toggle = within(canvasElement).getByRole('button', { name: 'Auto' })
    await userEvent.click(toggle)
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    await expect(toggle).toHaveAttribute('data-state', 'on')
  },
}

export const On: Story = {
  render: () => <Harness initial />,
}

export const Disabled: Story = {
  render: () => <Harness initial disabled />,
  play: async ({ canvasElement }) => {
    const toggle = within(canvasElement).getByRole('button', { name: 'Auto' })
    await userEvent.click(toggle)
    await userEvent.keyboard('{Space}')
    await expect(toggle).toBeDisabled()
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
  },
}
