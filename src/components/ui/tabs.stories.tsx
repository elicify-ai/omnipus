import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { Tabs, TabsContent, TabsList, TabsTrigger } from './tabs'
const meta = { title: 'Design System/Tabs', component: Tabs, render: (args) => <div data-testid="tabs"><Tabs {...args} /></div>, parameters: { designSystem: { keyboard: [{ trigger: '[data-testid="tab-one"]', key: 'ArrowRight', expectFocus: '[data-testid="tab-two"]' }], pointerTargets: ['[data-testid="tab-one"]', '[data-testid="tab-two"]'], motionTargets: ['[data-testid="tab-one"]'], forcedColorTargets: ['[data-testid="tabs"]'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="tab-one"]',property:'color',againstSelector:'[data-testid="tab-one"]',againstProperty:'backgroundColor'}],focus:['[data-testid="tab-one"]'],states:[{selector:'[data-testid="tab-one"]',attribute:'aria-selected',value:'true'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="tab-one"]', attribute: 'aria-selected', value: 'true' }] } } } satisfies Meta<typeof Tabs>
export default meta
type Story = StoryObj<typeof meta>
const children = <><TabsList aria-label="Workspace sections"><TabsTrigger data-testid="tab-one" value="one">Overview</TabsTrigger><TabsTrigger data-testid="tab-two" value="two">Activity</TabsTrigger><TabsTrigger value="disabled" disabled>Disabled</TabsTrigger></TabsList><TabsContent value="one">Overview content</TabsContent><TabsContent value="two">Activity content</TabsContent></>
export const Default: Story = { args: { defaultValue: 'one', children } }
export const Disabled: Story = { args: Default.args }
export const Dense: Story = { args: Default.args, decorators: [(Story) => <div className="text-[length:var(--type-utility-xs-size)]"><Story /></div>] }
export const NarrowViewport: Story = { args: Default.args, parameters: { viewport: { defaultViewport: 'mobile1' } } }
export const Activation: Story = {
  args: Default.args,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('tab', { name: 'Activity' }))
    await expect(canvas.getByRole('tab', { name: 'Activity' })).toHaveAttribute('aria-selected', 'true')
    await expect(canvas.getByRole('tabpanel')).toHaveTextContent('Activity content')
  },
}
