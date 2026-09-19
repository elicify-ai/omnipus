import type { Meta, StoryObj } from '@storybook/react-vite'
import { Avatar, AvatarFallback, AvatarImage } from './avatar'
const meta = { title: 'Design System/Avatar', component: Avatar, render: (args) => <Avatar {...args} data-testid="avatar" />, parameters: { designSystem: { forcedColorTargets: ['[data-testid="avatar"] > div'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="avatar"] > div',property:'color',againstSelector:'[data-testid="avatar"] > div',againstProperty:'backgroundColor'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="avatar"]', text: 'DP' }] } } } satisfies Meta<typeof Avatar>
export default meta
type Story = StoryObj<typeof meta>
const fallback = <AvatarFallback>DP</AvatarFallback>
export const Default: Story = { args: { children: fallback } }
export const Small: Story = { args: { ...Default.args, size: 'sm' } }
export const Large: Story = { args: { ...Default.args, size: 'lg' } }
export const Image: Story = { args: { ...Default.args, children: <AvatarImage src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="Daniel" /> } }
export const Dense: Story = { args: { ...Default.args, size: 'sm' } }
export const NarrowViewport: Story = { args: Default.args, parameters: { viewport: { defaultViewport: 'mobile1' } } }
