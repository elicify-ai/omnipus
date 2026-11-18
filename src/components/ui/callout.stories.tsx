import type { Meta, StoryObj } from '@storybook/react-vite'
import { Button } from './button'
import { Callout, CalloutActions, CalloutDescription, CalloutTitle } from './callout'

const meta = {
  title: 'Design System/Callout',
  component: Callout,
  args: { variant: 'info' },
  parameters: {
    designSystem: {
      forcedColors: { boundaries: ['[data-slot="callout"]'] },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-slot="callout"]', attribute: 'role', value: 'status' }],
    },
  },
  render: (args) => (
    <Callout {...args}>
      <CalloutTitle>Connector paused</CalloutTitle>
      <CalloutDescription>Messages are queued until the connector is resumed.</CalloutDescription>
      <CalloutActions>
        <Button size="sm" variant="outline">Resume</Button>
      </CalloutActions>
    </Callout>
  ),
} satisfies Meta<typeof Callout>
export default meta
type Story = StoryObj<typeof meta>

export const Info: Story = {}
export const Warning: Story = {
  args: { variant: 'warning' },
  parameters: { designSystem: { forcedColors: { boundaries: ['[data-slot="callout"]'] }, reflowExemptions: [], browserAssertions: [{ selector: '[data-slot="callout"]', attribute: 'role', value: 'alert' }] } },
}
export const Error: Story = {
  args: { variant: 'error' },
  parameters: { designSystem: { forcedColors: { boundaries: ['[data-slot="callout"]'] }, reflowExemptions: [], browserAssertions: [{ selector: '[data-slot="callout"]', attribute: 'role', value: 'alert' }] } },
}
export const Success: Story = { args: { variant: 'success' } }
