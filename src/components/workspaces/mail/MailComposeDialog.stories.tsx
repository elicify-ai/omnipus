import type { Meta, StoryObj } from '@storybook/react-vite'
import { fn } from 'storybook/test'
import { MailComposeDialog } from './MailComposeDialog'

const meta = {
  title: 'Workspaces/Mail/Compose',
  component: MailComposeDialog,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    mode: 'new',
    onSend: fn(),
    onClose: fn(),
  },
} satisfies Meta<typeof MailComposeDialog>

export default meta
type Story = StoryObj<typeof meta>

export const NewMessage: Story = {}

export const Reply: Story = {
  args: {
    mode: 'reply',
    replyTo: {
      from: 'ada@example.test',
      subject: 'Launch notes',
      messageId: '<launch-notes@example.test>',
    },
  },
}
