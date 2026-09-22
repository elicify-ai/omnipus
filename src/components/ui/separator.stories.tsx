import type { Meta, StoryObj } from '@storybook/react-vite'
import { Separator } from './separator'
const meta = { title: 'Design System/Separator', component: Separator, render: (args) => <Separator {...args} data-testid="separator" />, parameters: { designSystem: { forcedColorTargets: ['[data-testid="separator"]'], forcedColors:{differences:[{cue:'indicator',selector:'[data-testid="separator"]',property:'backgroundColor',againstSelector:'[data-design-system-config]',againstProperty:'backgroundColor'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="separator"]', attribute: 'data-orientation', value: 'horizontal' }] } } } satisfies Meta<typeof Separator>
export default meta
type Story = StoryObj<typeof meta>
export const Horizontal: Story = { args: {} }
export const Vertical: Story = { args: { orientation: 'vertical', className: 'h-12' } }
export const Semantic: Story = { args: { decorative: false, 'aria-label': 'Section separator' } }
export const Dense: Story = { args: Horizontal.args }
export const NarrowViewport: Story = { args: Horizontal.args, parameters: { viewport: { defaultViewport: 'mobile1' } } }
