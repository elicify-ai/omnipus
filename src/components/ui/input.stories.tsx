import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { Input } from './input'

const meta = { title: 'Design System/Input', component: Input, args: { 'aria-label': 'Project name' }, parameters: { designSystem: {
  keyboard: [{ trigger: '[aria-label="Project name"]', key: 'a', expectFocus: '[aria-label="Project name"]' }], pointerTargets: ['[aria-label="Project name"]'],
  motionTargets: ['[aria-label="Project name"]'], forcedColorTargets: ['[aria-label="Project name"]'], forcedColors:{boundaries:['input'],differences:[{cue:'foreground',selector:'input',property:'color',againstSelector:'input',againstProperty:'backgroundColor'}],focus:['input']},reflowExemptions: [], browserAssertions: [{ selector: '[aria-label="Project name"]', property: 'type', value: 'text' }],
} } } satisfies Meta<typeof Input>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = { play: async ({ canvasElement }) => { const input = within(canvasElement).getByRole('textbox'); await userEvent.type(input, 'Alpha'); await expect(input).toHaveValue('Alpha') } }
export const ReadOnly: Story = { args: { defaultValue: 'Fixed', readOnly: true }, play: async ({ canvasElement }) => { const input = within(canvasElement).getByRole('textbox'); await userEvent.type(input, ' changed'); await expect(input).toHaveValue('Fixed') } }
export const Invalid: Story = { args: { 'aria-invalid': true } }
export const Disabled: Story = { args: { disabled: true }, play: async ({ canvasElement }) => { const input = within(canvasElement).getByRole('textbox'); await userEvent.type(input, 'blocked'); await expect(input).toBeDisabled(); await expect(input).toHaveValue('') } }
export const NarrowViewport: Story = { parameters: { viewport: { defaultViewport: 'mobile1' } } }
