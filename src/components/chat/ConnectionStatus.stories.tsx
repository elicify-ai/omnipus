import type { Meta, StoryObj } from '@storybook/react-vite'
import {
  AssistantConnectionStatus,
  ChatConnectionStatusLine,
  UserMessageDeliveryStatus,
} from './ConnectionStatus'

const meta = {
  title: 'Chat/Connection status',
  component: UserMessageDeliveryStatus,
  args: { state: 'queued', agentName: 'Mia', latest: true },
} satisfies Meta<typeof UserMessageDeliveryStatus>

export default meta
type Story = StoryObj<typeof meta>

export const Queued: Story = {}
export const Received: Story = { args: { state: 'received' } }
export const Working: Story = { args: { state: 'working' } }
export const Failed: Story = { args: { state: 'failed' } }

export const PausedAnswer: Story = {
  render: () => <AssistantConnectionStatus state="paused" agentName="Mia" />,
}

export const UnfinishedAnswer: Story = {
  render: () => <AssistantConnectionStatus state="unfinished" agentName="Mia" />,
}

export const DeviceOffline: Story = {
  render: () => <ChatConnectionStatusLine state="offline" />,
}

export const OmnipusUnreachable: Story = {
  render: () => <ChatConnectionStatusLine state="unreachable" />,
}

export const CaughtUp: Story = {
  render: () => <ChatConnectionStatusLine state="back" />,
}
