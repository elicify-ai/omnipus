import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { MailPanel } from './MailPanel'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, saveMailDraft, sendMailDraft } = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
  saveMailDraft: vi.fn(),
  sendMailDraft: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents,
  fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/mail')>()),
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
  saveMailDraft,
  sendMailDraft,
}))

const draft: MailMessage = {
  message_id: '<draft@example.test>', uid: 4, uidvalidity: 77, folder: 'drafts',
  subject: 'Review request', from: 'mia@example.test', from_name: 'Mia',
  to: ['alice@example.test'], cc: ['lead@example.test'], bcc: ['private@example.test'],
  date: '2026-09-29T10:00:00Z', seen: true, is_draft: true, is_omnipus_draft: true,
  read_by_agent: false, reply_to: null, in_reply_to: null, references: null,
  body_text: 'Agent draft', has_html: false, body_markdown: 'Agent draft', markdown_lossy: false,
  attachments: [
    { part_index: 2, filename: 'keep.csv', content_type: 'text/csv', size_bytes: 8 },
    { part_index: 7, filename: 'remove.txt', content_type: 'text/plain', size_bytes: 6 },
  ],
}

function renderDraft(message: MailMessage = draft) {
  fetchMailMessage.mockResolvedValue(message)
  fetchMailMessages.mockResolvedValue({ messages: [message], truncated: false, next_before_uid: null })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" initialFolder="drafts" /></QueryClientProvider>)
}

async function openEditor() {
  fireEvent.click(await screen.findByRole('button', { name: /Review request/ }, { timeout: 30000 }))
  fireEvent.click(await screen.findByRole('button', { name: 'Edit' }, { timeout: 30000 }))
  return screen.getByRole('region', { name: 'Draft' })
}

describe('Draft editor shares Compose fields and attachments (F5)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([{ agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' }])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [{ slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null }] })
    fetchMailMessages.mockReset()
    fetchMailMessage.mockReset()
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
    saveMailDraft.mockReset().mockResolvedValue(draft)
    sendMailDraft.mockReset().mockResolvedValue({ message_id: '<draft@example.test>', sent_saved: true, save_warning: null, draft_cleanup_warning: null })
  })
  afterEach(() => cleanup())

  it('shows saved Cc/Bcc chips and saves changed recipients, not just To', async () => {
    renderDraft()
    const editor = await openEditor()
    expect(within(editor).getByText('lead@example.test')).toBeInTheDocument()
    expect(within(editor).getByText('private@example.test')).toBeInTheDocument()
    fireEvent.click(within(editor).getByRole('button', { name: 'Remove recipient lead@example.test' }))
    fireEvent.change(within(editor).getByRole('textbox', { name: 'Cc' }), { target: { value: 'other@example.test' } })
    fireEvent.change(within(editor).getByRole('textbox', { name: 'Bcc' }), { target: { value: 'second@example.test' } })
    fireEvent.click(within(editor).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(saveMailDraft).toHaveBeenCalledWith('ws-1', 'mia', 'uid:77:4', {
      to: ['alice@example.test'], cc: ['other@example.test'], bcc: ['private@example.test', 'second@example.test'],
      subject: 'Review request', body_markdown: 'Agent draft', uidvalidity: 77, uid: 4,
      keep_attachment_parts: [2, 7],
    }))
  })

  it('removes a carried part by its part_index and encodes an added file on Save', async () => {
    renderDraft({ ...draft, cc: [], bcc: null })
    const editor = await openEditor()
    expect(within(editor).getByRole('textbox', { name: 'Cc' })).toHaveValue('')
    expect(within(editor).getByRole('textbox', { name: 'Bcc' })).toHaveValue('')
    expect(within(editor).getByText('keep.csv')).toBeInTheDocument()
    expect(within(editor).getByText('remove.txt')).toBeInTheDocument()
    fireEvent.click(within(editor).getByRole('button', { name: 'Remove remove.txt' }))
    fireEvent.change(within(editor).getByLabelText('Attach files'), {
      target: { files: [new File(['hello'], 'new.txt', { type: 'text/plain' })] },
    })
    expect(within(editor).getByText('new.txt')).toBeInTheDocument()
    fireEvent.click(within(editor).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(saveMailDraft).toHaveBeenCalledWith('ws-1', 'mia', 'uid:77:4', {
      to: ['alice@example.test'], cc: [], bcc: [], subject: 'Review request', body_markdown: 'Agent draft',
      uidvalidity: 77, uid: 4, keep_attachment_parts: [2],
      attachments: [{ filename: 'new.txt', content_type: 'text/plain', data_base64: 'aGVsbG8=' }],
    }))
  })

  it('rejects adding files above the shared Compose attachment count cap', async () => {
    renderDraft()
    const editor = await openEditor()
    fireEvent.change(within(editor).getByLabelText('Attach files'), {
      target: { files: Array.from({ length: 9 }, (_, i) => new File(['x'], `new-${i}.txt`)) },
    })
    expect(within(editor).getByRole('alert')).toHaveTextContent('Up to 10 attachments are allowed.')
    expect(within(editor).queryByText('new-0.txt')).not.toBeInTheDocument()
    fireEvent.click(within(editor).getByRole('button', { name: 'Save' }))
    expect(saveMailDraft).not.toHaveBeenCalled()
  })

  it('sends the saved Cc/Bcc and carried parts when the user approves the updated draft', async () => {
    const updated: MailMessage = {
      ...draft, uid: 5, cc: ['other@example.test'], bcc: ['hidden@example.test'],
      attachments: [{ part_index: 9, filename: 'final.csv', content_type: 'text/csv', size_bytes: 10 }],
    }
    saveMailDraft.mockResolvedValueOnce(updated)
    renderDraft()
    fetchMailMessage.mockImplementation(async (_workspace: string, _agent: string, _folder: string, ref: string) => {
      if (ref === 'uid:77:4') return draft
      if (ref === 'uid:77:5') return updated
      throw new Error(`Unexpected draft ref: ${ref}`)
    })
    const editor = await openEditor()
    fireEvent.change(within(editor).getByRole('textbox', { name: 'Cc' }), { target: { value: 'other@example.test' } })
    fireEvent.change(within(editor).getByRole('textbox', { name: 'Bcc' }), { target: { value: 'hidden@example.test' } })
    fireEvent.click(within(editor).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(fetchMailMessage).toHaveBeenCalledWith('ws-1', 'mia', 'drafts', 'uid:77:5', { retry: false }))
    fireEvent.click(await screen.findByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sendMailDraft).toHaveBeenCalledWith('ws-1', 'mia', 'uid:77:5', {
      to: ['alice@example.test'], cc: ['other@example.test'], bcc: ['hidden@example.test'],
      subject: 'Review request', body_markdown: 'Agent draft', uidvalidity: 77, uid: 5,
      keep_attachment_parts: [9],
    }))
  })
})
