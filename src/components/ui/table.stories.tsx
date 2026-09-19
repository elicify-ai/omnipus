import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, within } from 'storybook/test'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './table'
const meta = { title: 'Design System/Table', component: Table, render: (args) => <Table {...args} containerProps={{ role: 'region', 'aria-label': 'Scrollable agents', tabIndex: 0 }} aria-label="Agents" data-testid="table" />, parameters: { designSystem: { motionTargets: ['[data-testid="table-row"]'], forcedColorTargets: ['[data-testid="table"]'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="table"]',property:'color',againstSelector:'[data-testid="table"]',againstProperty:'backgroundColor'}]},reflowExemptions: [{ selector: '[data-table-scroll]', reason: 'Wide data tables use a labeled horizontal scroll region.' }], browserAssertions: [{ selector: '[data-testid="table"]', attribute: 'aria-label', value: 'Agents' }] } } } satisfies Meta<typeof Table>
export default meta
type Story = StoryObj<typeof meta>
const rows = <><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Status</TableHead></TableRow></TableHeader><TableBody><TableRow data-testid="table-row"><TableCell>Mia</TableCell><TableCell>Active</TableCell></TableRow></TableBody></>
export const Default: Story = { args: { children: rows } }
export const Selected: Story = { args: { children: <><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Status</TableHead></TableRow></TableHeader><TableBody><TableRow data-state="selected" data-testid="table-row"><TableCell>Mia</TableCell><TableCell>Active</TableCell></TableRow></TableBody></> } }
export const Dense: Story = { args: { ...Default.args, className: 'text-xs' } }
export const NarrowViewport: Story = { args: Default.args, parameters: { viewport: { defaultViewport: 'mobile1' } } }
export const OverflowRegion: Story = {
  args: { children: rows, style: { minWidth: 768 } },
  decorators: [(Story) => <div style={{ width: 256, maxWidth: 256, minWidth: 0 }}><Story /></div>],
  parameters: { designSystem: { keyboard: [{ trigger: '[data-table-scroll]', key: 'ArrowRight', expectFocus: '[data-table-scroll]', expectScroll: { selector: '[data-table-scroll]', axis: 'x', direction: 'increase' } }] } },
  play: async ({ canvasElement }) => {
    const region = within(canvasElement).getByRole('region', { name: 'Scrollable agents' })
    await expect(region.scrollWidth).toBeGreaterThan(region.clientWidth)
    region.focus()
    await expect(region).toHaveFocus()
  },
}
