import type { Meta, StoryObj } from '@storybook/react-vite'
import { Button } from './button'
import { PageHeader } from './page-header'

const meta = {
  title: 'Design System/PageHeader',
  component: PageHeader,
  args: { title: 'Connectors', description: 'Bring messages from other apps into your workspaces.' },
  parameters: {
    designSystem: {
      forcedColors: { differences: [{ cue: 'foreground', selector: '[data-slot="page-header"] h1', property: 'color', againstSelector: '[data-slot="page-header"] h1', againstProperty: 'backgroundColor' }] },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-slot="page-header"] h1', text: 'Connectors' }],
    },
  },
} satisfies Meta<typeof PageHeader>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
export const WithActions: Story = {
  args: {
    actions: <Button variant="outline" size="sm">Import</Button>,
    primaryAction: <Button size="sm">Add connector</Button>,
  },
}
