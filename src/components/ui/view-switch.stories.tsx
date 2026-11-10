import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { ViewSwitch } from './view-switch'

function Fixture() {
  const [value, setValue] = useState('board')
  return <ViewSwitch value={value} onValueChange={setValue} aria-label="Task view" options={[
    { value: 'board', label: 'Board', testId: 'view-board' },
    { value: 'list', label: 'List', testId: 'view-list' },
    { value: 'graph', label: 'Graph', testId: 'view-graph' },
  ]} />
}
const meta = {
  title: 'Design System/ViewSwitch', component: ViewSwitch, render: () => <Fixture />,
  args: { value: 'board', onValueChange: () => {}, options: [], 'aria-label': 'Task view' },
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="view-board"]', key: 'ArrowRight', expectFocus: '[data-testid="view-list"]' }, { trigger: '[data-testid="view-list"]', key: 'End', expectFocus: '[data-testid="view-graph"]' }],
    pointerTargets: ['[data-testid="view-board"]', '[data-testid="view-list"]', '[data-testid="view-graph"]'],
    motionTargets: ['[data-testid="view-board"]'], forcedColorTargets: ['[data-testid="view-board"]'],
    forcedColors: { differences: [{ cue: 'foreground', selector: '[data-testid="view-board"]', property: 'color', againstSelector: '[data-testid="view-board"]', againstProperty: 'backgroundColor' }], focus: ['[data-testid="view-board"]'], states: [{ selector: '[data-testid="view-board"]', attribute: 'aria-checked', value: 'true' }] },
    browserAssertions: [{ selector: '[data-testid="view-board"]', attribute: 'aria-checked', value: 'true' }], reflowExemptions: [],
  } },
} satisfies Meta<typeof ViewSwitch>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = {}
export const Selection: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const board = canvas.getByRole('radio', { name: 'Board' })
    board.focus()
    await userEvent.keyboard('{ArrowRight}')
    await expect(canvas.getByRole('radio', { name: 'List' })).toHaveFocus()
    await expect(canvas.getByRole('radio', { name: 'List' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.keyboard('{Home}')
    await expect(board).toHaveAttribute('aria-checked', 'true')
  },
}
