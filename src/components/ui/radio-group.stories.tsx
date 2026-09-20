import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { RadioGroup, RadioGroupItem } from './radio-group'

function Fixture() {
  const [value, setValue] = useState('top-level')
  return (
    <RadioGroup value={value} onValueChange={setValue} aria-label="Board depth">
      <RadioGroupItem value="top-level" data-testid="radio-top-level">Top-level</RadioGroupItem>
      <RadioGroupItem value="show-all" data-testid="radio-show-all">Show all</RadioGroupItem>
      <RadioGroupItem value="disabled" data-testid="radio-disabled" disabled>Disabled</RadioGroupItem>
    </RadioGroup>
  )
}

const meta = { title: 'Design System/RadioGroup', component: RadioGroup, args: { value: 'top-level', onValueChange: () => {}, 'aria-label': 'Board depth' }, render: () => <Fixture />, parameters: { designSystem: { keyboard: [{ trigger: '[data-testid="radio-top-level"]', key: 'ArrowRight', expectFocus: '[data-testid="radio-show-all"]' }], pointerTargets: ['[data-testid="radio-top-level"]', '[data-testid="radio-show-all"]'], motionTargets: ['[data-testid="radio-top-level"]'], forcedColorTargets: ['[data-testid="radio-top-level"]'], forcedColors: { differences: [{ cue: 'foreground', selector: '[data-testid="radio-top-level"]', property: 'color', againstSelector: '[data-testid="radio-top-level"]', againstProperty: 'backgroundColor' }], focus: ['[data-testid="radio-top-level"]'], states: [{ selector: '[data-testid="radio-top-level"]', attribute: 'aria-checked', value: 'true' }] }, reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="radio-top-level"]', attribute: 'aria-checked', value: 'true' }] } } } satisfies Meta<typeof RadioGroup>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
export const Disabled: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByTestId('radio-disabled')).toBeDisabled()
  },
}
export const ArrowKeyNavigation: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const first = canvas.getByTestId('radio-top-level')
    first.focus()
    await userEvent.keyboard('{ArrowRight}')
    await expect(canvas.getByTestId('radio-show-all')).toHaveFocus()
    await expect(canvas.getByTestId('radio-show-all')).toHaveAttribute('aria-checked', 'true')
  },
}
export const NarrowViewport: Story = { parameters: { viewport: { defaultViewport: 'mobile1' } } }
