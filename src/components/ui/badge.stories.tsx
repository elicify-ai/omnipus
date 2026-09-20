import type { Meta, StoryObj } from '@storybook/react-vite'
import { Badge } from './badge'
const meta = { title: 'Design System/Badge', component: Badge, render: (args) => <Badge {...args} data-testid="badge" />, parameters: { designSystem: { motionTargets: ['[data-testid="badge"]'], forcedColorTargets: ['[data-testid="badge"]'], forcedColors:{boundaries:['[data-testid="badge"]'],differences:[{cue:'foreground',selector:'[data-testid="badge"]',property:'color',againstSelector:'[data-testid="badge"]',againstProperty:'backgroundColor',actualSystemColor:'ButtonText',againstSystemColor:'ButtonFace'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="badge"]', text: 'Status' }] } } } satisfies Meta<typeof Badge>
export default meta
type Story = StoryObj<typeof meta>
const base = { children: 'Status' } as const
export const Default: Story = { args: base }
export const Secondary: Story = { args: { ...base, variant: 'secondary' } }
export const Outline: Story = { args: { ...base, variant: 'outline' } }
export const Success: Story = { args: { ...base, variant: 'success' } }
export const Error: Story = { args: { ...base, variant: 'error' } }
export const Destructive: Story = { args: { ...base, variant: 'destructive' } }
export const Warning: Story = { args: { ...base, variant: 'warning' } }
export const Muted: Story = { args: { ...base, variant: 'muted' } }
export const Dense: Story = { args: base, decorators: [(Story) => <div className="text-[length:var(--type-utility-xs-size)]"><Story /></div>] }
export const NarrowViewport: Story = { args: base, parameters: { viewport: { defaultViewport: 'mobile1' } } }
