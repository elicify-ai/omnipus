// mail.stories.tsx — the D35 prototype stories for the docked Mail panel
// (one CSF meta per file; the draft panel, compose dialog and signature
// editor stories live beside this one, named after their component). The
// founder reviews these in dark theme at desktop (1440x900) and narrow
// (390x844) widths. Sample data only — no backend (D35).
import type { Meta, StoryObj } from '@storybook/react-vite'
import { MailPanel } from './MailPanel'
import {
  sampleConnectionBackoff,
  sampleEmptyDrafts,
  sampleLoading,
  sampleMailData,
} from './sampleMail'

const meta = {
  title: 'Mail/Panel',
  component: MailPanel,
} satisfies Meta<typeof MailPanel>
export default meta
type Story = StoryObj<typeof meta>

// Every story wraps the panel in a fixed-height canvas so the docked
// layout (h-full) has a real surface in the Storybook iframe.
function Canvas({ children }: { children: React.ReactNode }) {
  return <div style={{ height: 780 }} className="w-full">{children}</div>
}

export const InboxList: Story = {
  args: { data: sampleMailData },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}

export const ReadingHtmlBlocked: Story = {
  args: { data: sampleMailData, initialSelectedId: 'm1' },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}

export const ReadingImagesLoaded: Story = {
  args: { data: sampleMailData, initialSelectedId: 'm4' },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}

export const ReadingPlainText: Story = {
  args: { data: sampleMailData, initialSelectedId: 'm2' },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}

export const EmptyDrafts: Story = {
  args: { data: sampleEmptyDrafts, initialFolder: 'drafts' },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}

export const Loading: Story = {
  args: { data: sampleLoading, loading: true },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}

export const ConnectionBackoff: Story = {
  args: { data: sampleConnectionBackoff },
  render: (args) => (
    <Canvas>
      <MailPanel {...args} />
    </Canvas>
  ),
}
