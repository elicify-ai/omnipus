import type { Meta, StoryObj } from '@storybook/react-vite'
import { MailComposeDialog } from './MailComposeDialog'

const meta = {
  title: 'Workspaces/Mail/Compose dialog',
  component: MailComposeDialog,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    mode: 'new',
    onClose: () => undefined,
    onSend: () => undefined,
  },
} satisfies Meta<typeof MailComposeDialog>

export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
