import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { SegmentedControl, SegmentedControlItem } from './segmented-control'

function Fixture() {
  const [value, setValue] = useState('view')
  return (
    <SegmentedControl value={value} onValueChange={setValue} aria-label="View mode">
      <SegmentedControlItem value="view" data-testid="segment-view">View</SegmentedControlItem>
      <SegmentedControlItem value="edit" data-testid="segment-edit">Edit</SegmentedControlItem>
      <SegmentedControlItem value="disabled" data-testid="segment-disabled" disabled>Disabled</SegmentedControlItem>
    </SegmentedControl>
  )
}

const meta = { title: 'Design System/SegmentedControl', component: SegmentedControl, args: { value: 'view', onValueChange: () => {}, 'aria-label': 'View mode' }, render: () => <Fixture />, parameters: { designSystem: { keyboard: [{ trigger: '[data-testid="segment-view"]', key: 'Tab', expectFocus: '[data-testid="segment-edit"]' }], pointerTargets: ['[data-testid="segment-view"]', '[data-testid="segment-edit"]'], motionTargets: ['[data-testid="segment-edit"]'], forcedColorTargets: ['[data-testid="segment-view"]'], forcedColors: { differences: [{ cue: 'foreground', selector: '[data-testid="segment-view"]', property: 'color', againstSelector: '[data-testid="segment-view"]', againstProperty: 'backgroundColor' }], focus: ['[data-testid="segment-view"]'], states: [{ selector: '[data-testid="segment-view"]', attribute: 'aria-pressed', value: 'true' }] }, reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="segment-view"]', attribute: 'aria-pressed', value: 'true' }] } } } satisfies Meta<typeof SegmentedControl>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
export const Disabled: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByTestId('segment-disabled')).toBeDisabled()
  },
}
export const Selection: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByTestId('segment-edit'))
    await expect(canvas.getByTestId('segment-edit')).toHaveAttribute('aria-pressed', 'true')
    await expect(canvas.getByTestId('segment-view')).toHaveAttribute('aria-pressed', 'false')
  },
}
export const NarrowViewport: Story = { parameters: { viewport: { defaultViewport: 'mobile1' } } }
