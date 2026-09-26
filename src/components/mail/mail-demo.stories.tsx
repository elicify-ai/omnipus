// mail-demo.stories.tsx — "Mail/Demo — Interactive": the one CLICKABLE
// prototype story. Unlike the frozen state stories (mail.stories.tsx and the
// per-component stories beside it — kept unchanged), this one wraps the real
// mail components with local React state and sample data so every interaction
// works: open/read messages, load remote images, switch folders/mailboxes,
// edit/discard/send the draft, compose and send, the signature editor's live
// preview, and the connection-error banner (Retry now clears it). Sample
// data only — no backend (D35).
import type { Meta, StoryObj } from '@storybook/react-vite'
import { MailInteractiveDemo } from './MailInteractiveDemo'

const meta = {
  title: 'Mail/Demo',
  component: MailInteractiveDemo,
  argTypes: {
    connectionError: {
      control: 'boolean',
      description: 'Start with the connection-error banner visible; "Retry now" clears it.',
    },
  },
} satisfies Meta<typeof MailInteractiveDemo>
export default meta
type Story = StoryObj<typeof meta>

// Same fixed-height canvas as the state stories so the panel (h-full) has a
// real surface in the Storybook iframe.
function Canvas({ children }: { children: React.ReactNode }) {
  return <div style={{ height: 780 }} className="w-full">{children}</div>
}

export const Interactive: Story = {
  args: { connectionError: false },
  render: (args) => (
    <Canvas>
      {/* Keyed on the control so toggling the connection error rebuilds the
          demo state (the banner variant needs a fresh mount). */}
      <MailInteractiveDemo key={args.connectionError ? 'error' : 'ok'} connectionError={args.connectionError} />
    </Canvas>
  ),
}
