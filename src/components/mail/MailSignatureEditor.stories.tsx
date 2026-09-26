// MailSignatureEditor story — signature editing with the live sanitized
// preview (FR-008). Sample data only.
import type { Meta, StoryObj } from '@storybook/react-vite'
import { MailSignatureEditor } from './MailSignatureEditor'
import { sampleMailData } from './sampleMail'

const meta = {
  title: 'Mail/SignatureEditor',
  component: MailSignatureEditor,
} satisfies Meta<typeof MailSignatureEditor>
export default meta
type Story = StoryObj<typeof meta>

export const SignatureEditor: Story = {
  args: { initialHtml: sampleMailData.signatureHtml, maxChars: sampleMailData.signatureMaxChars },
  render: (args) => (
    <div style={{ height: 780, overflowY: 'auto' }} className="w-full bg-[var(--color-surface-1)] p-[var(--space-3)]">
      <MailSignatureEditor {...args} />
    </div>
  ),
}
