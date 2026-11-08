import type { Meta, StoryObj } from '@storybook/react-vite'
import { Tray } from '@phosphor-icons/react'
import { Button } from './button'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from './empty'

const meta = {
  title: 'Design System/Empty',
  component: Empty,
  parameters: {
    designSystem: {
      forcedColors: { boundaries: ['[data-slot="empty"]'] },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-slot="empty"] [data-slot="empty-title"]', text: 'No notifications yet' }],
    },
  },
  render: () => (
    <Empty>
      <EmptyHeader>
        <EmptyMedia variant="icon"><Tray className="size-[var(--icon-size-prominent)]" /></EmptyMedia>
        <EmptyTitle>No notifications yet</EmptyTitle>
        <EmptyDescription>New messages from your agents appear here.</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button size="sm" variant="outline">Open settings</Button>
      </EmptyContent>
    </Empty>
  ),
} satisfies Meta<typeof Empty>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
