import type { Meta, StoryObj } from '@storybook/react-vite'
import { Avatar, AvatarFallback } from './avatar'
import { AvatarStack } from './avatar-stack'

const mark = (initials: string) => (
  <Avatar size="sm">
    <AvatarFallback>{initials}</AvatarFallback>
  </Avatar>
)
const members = ['JA', 'MI', 'PE', 'RO', 'IN', 'MA', 'TO', 'EL', 'SA', 'KO'].map((initials) => ({ key: initials, node: mark(initials) }))

const meta = {
  title: 'Design System/AvatarStack',
  component: AvatarStack,
  render: (args) => <span data-testid="avatar-stack-host"><AvatarStack {...args} /></span>,
  parameters: {
    designSystem: {
      forcedColorTargets: ['[data-avatar-stack-overflow] > div > div'],
      forcedColors: { differences: [{ cue: 'foreground', selector: '[data-avatar-stack-overflow] > div > div', property: 'color', againstSelector: '[data-avatar-stack-overflow] > div > div', againstProperty: 'backgroundColor' }] },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-avatar-stack-overflow]', text: '+1' }],
    },
  },
} satisfies Meta<typeof AvatarStack>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = { args: { items: members.slice(0, 4) } }
export const Two: Story = { args: { items: members.slice(0, 2) } }
export const Three: Story = { args: { items: members.slice(0, 3) } }
export const Ten: Story = { args: { items: members } }
export const Medium: Story = { args: { items: members.slice(0, 4), size: 'md' } }
export const NarrowViewport: Story = { args: { items: members }, parameters: { viewport: { defaultViewport: 'mobile1' } } }
