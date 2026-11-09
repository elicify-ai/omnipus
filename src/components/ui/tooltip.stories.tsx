import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { Info } from '@phosphor-icons/react'
import { Badge } from './badge'
import { IconButton } from './icon-button'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

const TEXT = 'No kernel sandbox is enforcing. Safe tool calls still run without asking; shell commands ask first, except read-only ones and commands an operator rule allows.'

const designSystem = {
  pointerTargets: ['[data-testid="tooltip-trigger"]'],
  browserAssertions: [{ selector: '[data-testid="tooltip-trigger"]', attribute: 'aria-describedby' }],
  motionTargets: ['[role="tooltip"]'],
  forcedColors: {
    boundaries: ['[role="tooltip"]'],
    focus: ['[data-testid="tooltip-trigger"]'],
  },
}

const meta = {
  title: 'Design System/Tooltip',
  component: Tooltip,
  // Default opens on mount so every generic browser-kind check reads the
  // open bubble (axe, browser, forced-colors, pointer, reduced-motion,
  // root-size, reflow).
  args: { defaultOpen: true },
  render: (args) => (
    <Tooltip {...args}>
      <TooltipTrigger asChild>
        <span tabIndex={0} data-testid="tooltip-trigger" className="inline-flex">
          <Badge variant="warning">Auto — no sandbox</Badge>
        </span>
      </TooltipTrigger>
      <TooltipContent>{TEXT}</TooltipContent>
    </Tooltip>
  ),
  // Viewport-centred like the overlay stories (overlays.stories.tsx).
  parameters: { layout: 'centered', designSystem },
} satisfies Meta<typeof Tooltip>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const page = within(canvasElement.ownerDocument.body)
    await expect(await page.findByRole('tooltip')).toBeVisible()
    await expect(within(canvasElement).getByTestId('tooltip-trigger')).toHaveAttribute('aria-describedby')
  },
}

// Keyboard: focus opens the bubble, Escape dismisses it and focus stays on
// the trigger (not a focus trap).
export const Keyboard: Story = {
  args: { defaultOpen: false },
  parameters: {
    designSystem: {
      ...designSystem,
      browserAssertions: [{ selector: '[data-testid="tooltip-trigger"]', attribute: 'tabindex', value: '0' }],
      keyboard: [{ trigger: '[data-testid="tooltip-trigger"]', key: 'Escape', expectFocus: '[data-testid="tooltip-trigger"]' }],
    },
  },
  play: async ({ canvasElement }) => {
    const trigger = within(canvasElement).getByTestId('tooltip-trigger')
    await userEvent.tab()
    await expect(trigger).toHaveFocus()
    const page = within(canvasElement.ownerDocument.body)
    await expect(await page.findByRole('tooltip')).toBeVisible()
    await userEvent.keyboard('{Escape}')
    await expect(trigger).toHaveFocus()
  },
}

// EdgeOfViewport: an icon-only control flush with the left edge; Radix's
// collision padding (8px = --space-2) keeps the bubble inside the viewport.
export const EdgeOfViewport: Story = {
  parameters: {
    layout: 'fullscreen',
    designSystem: {
      ...designSystem,
      pointerTargets: ['[data-testid="tooltip-edge-trigger"]'],
      browserAssertions: [{ selector: '[data-testid="tooltip-edge-trigger"]', attribute: 'aria-describedby' }],
      forcedColors: { boundaries: ['[role="tooltip"]'], focus: ['[data-testid="tooltip-edge-trigger"]'] },
    },
  },
  render: (args) => (
    <div className="pt-[var(--space-8)]">
      <Tooltip {...args}>
        <TooltipTrigger asChild>
          <IconButton aria-label="Sandbox details" data-testid="tooltip-edge-trigger">
            <Info aria-hidden="true" className="size-[var(--icon-size-medium)]" />
          </IconButton>
        </TooltipTrigger>
        <TooltipContent side="top">{TEXT}</TooltipContent>
      </Tooltip>
    </div>
  ),
}
