import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { Badge } from './badge'
import { Tooltip } from './tooltip'

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
  args: {
    content: 'No kernel sandbox is enforcing. Safe tool calls still run without asking, but shell commands are checked by reading the command text only.',
    // Satisfies TooltipProps' required `children` for CSF3's story-arg
    // typing — the render below supplies the actual trigger JSX directly
    // and never reads `args.children`.
    children: <Badge variant="warning">Auto — no sandbox</Badge>,
  },
  render: (args) => (
    <Tooltip {...args} data-testid="tooltip-trigger">
      <Badge variant="warning">Auto — no sandbox</Badge>
    </Tooltip>
  ),
  // Matches the Popover/Dialog overlay stories' own convention
  // (overlays.stories.tsx) — without it the trigger renders left-aligned in
  // Storybook's default 'padded' layout, not viewport-centered, which is
  // what actually caused the reflow/zoom overflow this fix corrects.
  parameters: { layout: 'centered', designSystem },
} satisfies Meta<typeof Tooltip>
export default meta
type Story = StoryObj<typeof meta>

// Default — hover reveals the bubble; used by every generic browser-kind
// check (axe, browser, forced-colors, pointer, reduced-motion, root-size,
// zoom, reflow) via the manifest, all reading the OPEN state this play()
// function leaves behind before the story-finished signal fires.
export const Default: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const trigger = canvas.getByTestId('tooltip-trigger')
    await userEvent.hover(trigger)
    await expect(canvas.getByRole('tooltip')).toBeVisible()
    await expect(trigger).toHaveAttribute('aria-describedby')
  },
}

// Keyboard — proves the trigger is keyboard-reachable and that Escape
// dismisses the bubble WITHOUT moving focus off the trigger (not a focus
// trap). Playwright's `.press()` focuses the target before sending the key,
// so this single step both reaches the trigger by keyboard and exercises
// the dismiss path.
export const Keyboard: Story = {
  parameters: {
    designSystem: {
      ...designSystem,
      keyboard: [
        {
          trigger: '[data-testid="tooltip-trigger"]',
          key: 'Escape',
          expectFocus: '[data-testid="tooltip-trigger"]',
        },
      ],
    },
  },
}
