import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { Button } from './button'
import { ListGroup } from './list-group'

const rows = (
  <ul className="flex flex-col gap-[var(--space-1)] py-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
    <li>Support channel</li>
    <li>Release notes</li>
    <li>Team chat</li>
  </ul>
)

const meta = {
  title: 'Design System/ListGroup',
  component: ListGroup,
  args: { label: 'Telegram', count: 3, children: rows, 'data-testid': 'list-group' },
  parameters: {
    designSystem: {
      keyboard: [{ trigger: '[data-testid="list-group"] [aria-controls]', key: 'Enter', expectExpanded: true }],
      pointerTargets: ['[data-testid="list-group"] [aria-controls]'],
      forcedColors: {
        // The rendered-component target the forced-colors check resolves
        // (boundaries[0] or differences[0]): the toggle's text must stay
        // distinct from its own background, as DisclosureRow declares.
        differences: [{ cue: 'foreground', selector: '[data-testid="list-group"] [aria-controls]', property: 'color', againstSelector: '[data-testid="list-group"] [aria-controls]', againstProperty: 'backgroundColor' }],
        focus: ['[data-testid="list-group"] [aria-controls]'],
        states: [{ selector: '[data-testid="list-group"] [aria-controls]', attribute: 'aria-expanded', value: 'false' }],
      },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-testid="list-group"] [aria-controls]', attribute: 'aria-expanded', value: 'false' }],
    },
  },
} satisfies Meta<typeof ListGroup>
export default meta
type Story = StoryObj<typeof meta>

export const Collapsed: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const toggle = canvas.getByRole('button', { name: 'Telegram 3' })
    await userEvent.click(toggle)
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(canvas.getByText('Support channel')).toBeVisible()
    await userEvent.click(toggle)
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  },
}
export const Expanded: Story = {
  args: { defaultExpanded: true },
  parameters: { designSystem: { keyboard: [{ trigger: '[data-testid="list-group"] [aria-controls]', key: 'Enter', expectExpanded: false }], pointerTargets: ['[data-testid="list-group"] [aria-controls]'], reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="list-group"] [aria-controls]', attribute: 'aria-expanded', value: 'true' }] } },
}
export const WithActions: Story = {
  args: { actions: <Button size="sm" variant="ghost">Rename</Button> },
}
