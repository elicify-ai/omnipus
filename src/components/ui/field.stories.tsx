import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { Field } from './field'
import { Input } from './input'

const meta = {
  title: 'Design System/Field',
  component: Field,
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="field-input"]', key: 'a', expectFocus: '[data-testid="field-input"]' }],
    pointerTargets: ['[data-testid="field-input"]'],
    forcedColorTargets: ['[data-testid="field-input"]'],
    forcedColors:{boundaries:['[data-testid="field-input"]'],differences:[{cue:'foreground',selector:'[data-testid="field-input"]',property:'color',againstSelector:'[data-testid="field-input"]',againstProperty:'backgroundColor'}],focus:['[data-testid="field-input"]']},reflowExemptions: [],
    browserAssertions: [{ selector: '[data-testid="field-input"]', attribute: 'aria-describedby' }],
  } },
} satisfies Meta<typeof Field>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  args: { label: 'Workspace name', description: 'Visible to teammates', children: <Input data-testid="field-input" defaultValue="Alpha" /> },
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole('textbox', { name: 'Workspace name' })
    await userEvent.clear(input)
    await userEvent.type(input, 'Sovereign')
    await expect(input).toHaveValue('Sovereign')
  },
}
export const Required: Story = { args: { ...Default.args, required: true } }
export const Invalid: Story = { args: { ...Default.args, error: 'Workspace name is required' } }
export const ReadOnly: Story = {
  args: { ...Default.args, children: <Input data-testid="field-input" value="Fixed" readOnly /> },
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole('textbox', { name: 'Workspace name' })
    await userEvent.type(input, ' changed')
    await expect(input).toHaveValue('Fixed')
  },
}
export const Disabled: Story = {
  args: { ...Default.args, children: <Input data-testid="field-input" disabled /> },
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole('textbox', { name: 'Workspace name' })
    await userEvent.type(input, 'blocked')
    await expect(input).toBeDisabled()
    await expect(input).toHaveValue('')
  },
}
export const NarrowViewport: Story = { args: Default.args, parameters: { viewport: { defaultViewport: 'mobile1' } } }

export const Optional: Story = {
  args: { ...Default.args, optionalLabel: '(optional)' },
  play: async ({ canvasElement }) => {
    const input = within(canvasElement).getByRole('textbox', { name: 'Workspace name (optional)' })
    await expect(input).not.toBeRequired()
    await expect(within(canvasElement).getByText('(optional)')).toBeVisible()
  },
}
