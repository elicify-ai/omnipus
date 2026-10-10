import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { ListPreviewLayout, ListPreviewRegion } from '@/components/panel-shell/ListPreviewLayout'
import { MailPanel } from './MailPanel'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, saveMailDraft } = vi.hoisted(() => ({
  fetchAgents: vi.fn(), fetchMailboxes: vi.fn(), fetchMailFolders: vi.fn(), fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(), fetchMailSummary: vi.fn(), saveMailDraft: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()), fetchAgents, fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/mail')>()),
  fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, saveMailDraft,
}))

const draft: MailMessage = {
  message_id: '<draft@example.test>', uid: 4, uidvalidity: 77, folder: 'drafts', subject: 'Review request',
  from: 'mia@example.test', from_name: 'Mia', to: ['alice@example.test'], cc: [], bcc: null,
  date: '2026-09-29T10:00:00Z', seen: true, is_draft: true, is_omnipus_draft: true,
  read_by_agent: false, reply_to: null, in_reply_to: null, references: null,
  body_text: 'Agent draft', has_html: false, body_markdown: 'Agent draft', markdown_lossy: false,
  attachments: [],
}

function renderPanel(layout: 'stacked' | 'split' = 'stacked') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" initialFolder="drafts" layout={layout} /></QueryClientProvider>)
}

async function openEditor() {
  fireEvent.click(await screen.findByRole('button', { name: /Review request/ }))
  fireEvent.click(await screen.findByRole('button', { name: 'Edit' }))
}

describe('Draft editor docked height (F6)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    fetchAgents.mockReset().mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([{ agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' }])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [{ slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null }] })
    fetchMailMessages.mockReset().mockResolvedValue({ messages: [draft], truncated: false, next_before_uid: null })
    fetchMailMessage.mockReset().mockResolvedValue(draft)
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
    saveMailDraft.mockReset().mockResolvedValue(draft)
  })
  afterEach(() => cleanup())

  it('gives the editor at least three quarters of the stacked space and restores normal list-over-preview on Back', async () => {
    renderPanel()
    const layout = await screen.findByTestId('mail-list-preview-layout')
    const list = screen.getByTestId('mail-list-region')
    const preview = screen.getByTestId('mail-reading-zone')
    expect(layout).toHaveAttribute('data-layout', 'stacked')
    expect(list).toHaveClass('flex-[45]')
    expect(preview).toHaveClass('flex-[55]')
    await openEditor()
    expect(layout).toHaveClass('flex-col')
    expect(list).toHaveClass('flex-[20]')
    expect(preview).toHaveClass('flex-[80]')
    expect(screen.getByTestId('mail-list-zone')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Back to preview' }))
    expect(list).toHaveClass('flex-[45]')
    expect(preview).toHaveClass('flex-[55]')
  })

  it('restores the normal stacked split after Save completes', async () => {
    renderPanel()
    await openEditor()
    const list = screen.getByTestId('mail-list-region')
    expect(list).toHaveClass('flex-[20]')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(list).toHaveClass('flex-[45]'))
    expect(screen.getByTestId('mail-reading-zone')).toHaveClass('flex-[55]')
  })

  it('does not narrow the list in the full-screen split or change the Library default', async () => {
    renderPanel()
    await openEditor()
    expect(screen.getByTestId('mail-list-region')).toHaveClass('flex-[20]')
    cleanup()
    renderPanel('split')
    await openEditor()
    expect(screen.getByTestId('mail-list-preview-layout')).toHaveAttribute('data-layout', 'split')
    expect(screen.getByTestId('mail-list-region')).toHaveClass('flex-[40]')
    expect(screen.getByTestId('mail-reading-zone')).toHaveClass('flex-[60]')
    render(
      <ListPreviewLayout layout="stacked">
        <ListPreviewRegion layout="stacked" region="list" previewVisible surface="library-list" testId="library-list-check">Library list</ListPreviewRegion>
        <ListPreviewRegion layout="stacked" region="preview" previewVisible testId="library-preview-check">Library preview</ListPreviewRegion>
      </ListPreviewLayout>,
    )
    expect(screen.getByTestId('library-list-check')).toHaveClass('flex-[45]')
    expect(screen.getByTestId('library-preview-check')).toHaveClass('flex-[55]')
  })
})
