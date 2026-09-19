import type { Meta, StoryObj } from '@storybook/react-vite'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from './card'
const meta = { title: 'Design System/Card', component: Card, render: (args) => <Card {...args} data-testid="card" />, parameters: { designSystem: { forcedColorTargets: ['[data-testid="card"]'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="card"]',property:'color',againstSelector:'[data-testid="card"]',againstProperty:'backgroundColor'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="card"]', text: 'Sovereign workspace' }] } } } satisfies Meta<typeof Card>
export default meta
type Story = StoryObj<typeof meta>
const content = <><CardHeader><CardTitle>Sovereign workspace</CardTitle><CardDescription>Private by default.</CardDescription></CardHeader><CardContent>Workspace content</CardContent><CardFooter>Updated now</CardFooter></>
export const Default: Story = { args: { children: content, className: 'max-w-md' } }
export const Dense: Story = { args: { ...Default.args, className: 'max-w-sm [&>div]:p-4' } }
export const NarrowViewport: Story = { args: { ...Default.args, className: 'w-full' }, parameters: { viewport: { defaultViewport: 'mobile1' } } }
