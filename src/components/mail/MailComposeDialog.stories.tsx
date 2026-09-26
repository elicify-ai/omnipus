// MailComposeDialog story — the human-initiated compose flow (D12). Sample
// data only; Send/Discard in the prototype are inert.
import type { Meta, StoryObj } from '@storybook/react-vite'
import { MailPanel } from './MailPanel'
import { sampleMailData } from './sampleMail'

const meta = {
  title: 'Mail/ComposeDialog',
  component: MailPanel,
} satisfies Meta<typeof MailPanel>
export default meta
type Story = StoryObj<typeof meta>

function Canvas({ children }: { children: React.ReactNode }) {
  return <div style={{ height: 780 }} className="w-full">{children}</div>
}

export const ComposeOpen: Story = {
  args: { data: sampleMailData, composeOpen: true },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}
