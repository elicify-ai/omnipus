// MailDraftPanel stories — the chat-link draft flow (D12: View / Edit / Send
// / Discard; D24 foreign-draft formatting-loss notice). Sample data only.
import type { Meta, StoryObj } from '@storybook/react-vite'
import { MailDraftPanel } from './MailDraftPanel'
import { sampleExternalDraft, sampleMailData } from './sampleMail'

const meta = {
  title: 'Mail/DraftPanel',
  component: MailDraftPanel,
} satisfies Meta<typeof MailDraftPanel>
export default meta
type Story = StoryObj<typeof meta>

function Canvas({ children }: { children: React.ReactNode }) {
  return <div style={{ height: 780 }} className="w-full">{children}</div>
}

export const AgentDraftView: Story = {
  args: { draft: sampleMailData.draft },
  render: (args) => (
    <Canvas>
      <MailDraftPanel {...args} />
    </Canvas>
  ),
}

export const ForeignDraftEdit: Story = {
  args: { draft: sampleExternalDraft, initialMode: 'edit' },
  render: (args) => (
    <Canvas>
      <MailDraftPanel {...args} />
    </Canvas>
  ),
}
