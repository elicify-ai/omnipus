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
            actualSystemColor: 'CanvasText',
            againstSystemColor: 'Canvas',
          },
        ],
        focus: ['[data-testid="text-toggle"]'],
        states: [{ selector: '[data-testid="text-toggle"]', attribute: 'data-state', value: 'off' }],
        stateTransitions: [
          {
            selector: '[data-testid="text-toggle"]',
            attribute: 'data-state',
            from: 'off',
            action: 'click',
            to: 'on',
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
          },
        ],
      },
      reflowExemptions: [],
      browserAssertions: [
        { selector: '[data-testid="text-toggle"]', attribute: 'aria-pressed', value: 'false' },
        { selector: '[data-testid="text-toggle"]', text: 'Auto' },
      ],
    },
  },
} satisfies Meta<typeof TextToggle>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  // The appearance gate photographs this story after play finishes. The
  // picture is the quiet off state, so play must not press the control first.
  play: async ({ canvasElement }) => {
    const toggle = within(canvasElement).getByRole('button', { name: 'Auto' })
    await expect(toggle).toHaveAttribute('aria-pressed', 'false')
    await expect(toggle).toHaveAttribute('data-state', 'off')
  },
}

export const On: Story = {
  render: () => <Harness initial />,
}

export const Pressed: Story = {
  play: async ({ canvasElement }) => {
    const toggle = within(canvasElement).getByRole('button', { name: 'Auto' })
    await expect(toggle).toHaveAttribute('aria-pressed', 'false')
    await userEvent.click(toggle)
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    await expect(toggle).toHaveAttribute('data-state', 'on')
  },
}

export const Disabled: Story = {
  render: () => <Harness initial disabled />,
  play: async ({ canvasElement }) => {
    const toggle = within(canvasElement).getByRole('button', { name: 'Auto' })
    await expect(toggle).toBeDisabled()
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    // Disabled controls set pointer-events: none, so a pointer click is
    // rejected before it can prove anything. Keyboard activation must not
    // ask for a change either: the control stays pressed.
    toggle.focus()
    await userEvent.keyboard('{Space}')
    await expect(toggle).toBeDisabled()
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
  },
}
