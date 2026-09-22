import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { Textarea } from './textarea'

const meta = { title: 'Design System/Textarea', component: Textarea, args: { 'aria-label': 'Notes' }, parameters: { designSystem: {
  keyboard: [{ trigger: '[aria-label="Notes"]', key: 'a', expectFocus: '[aria-label="Notes"]' }], pointerTargets: ['[aria-label="Notes"]'],
  forcedColorTargets: ['[aria-label="Notes"]'], forcedColors:{boundaries:['textarea'],differences:[{cue:'foreground',selector:'textarea',property:'color',againstSelector:'textarea',againstProperty:'backgroundColor'}],focus:['textarea']},reflowExemptions: [], browserAssertions: [{ selector: '[aria-label="Notes"]', attribute: 'aria-label', value: 'Notes' }],
} } } satisfies Meta<typeof Textarea>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = { play: async ({ canvasElement }) => { const input = within(canvasElement).getByRole('textbox'); await userEvent.type(input, 'Alpha'); await expect(input).toHaveValue('Alpha') } }
export const ReadOnly: Story = { args: { defaultValue: 'Fixed', readOnly: true } }
export const Invalid: Story = { args: { 'aria-invalid': true } }
export const Disabled: Story = { args: { disabled: true } }
export const NarrowViewport: Story = { parameters: { viewport: { defaultViewport: 'mobile1' } } }
