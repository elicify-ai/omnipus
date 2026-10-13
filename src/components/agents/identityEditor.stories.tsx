import type { Meta, StoryObj } from '@storybook/react-vite'

import { CreateAgentWizard } from './CreateAgentWizard'

const meta = {
  title: 'Agents/Identity editor',
  component: CreateAgentWizard,
} satisfies Meta<typeof CreateAgentWizard>

export default meta
type Story = StoryObj<typeof meta>

export const Create: Story = {
  args: {
    initialType: 'Main',
    onSubmit: async () => {},
    onClose: () => {},
  },
}
