import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { MailPanel } from './MailPanel'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary } = vi.hoisted(() => ({
  fetchAgents: vi.fn(), fetchMailboxes: vi.fn(), fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(), fetchMailMessage: vi.fn(), fetchMailSummary: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()), fetchAgents, fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/mail')>()),
  fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary,
}))

const message: MailMessage = {
  message_id: '<received@example.test>', uid: 4, uidvalidity: 77, folder: 'inbox',
  subject: 'Quarterly review', from: 'alice@example.test', from_name: 'Alice',
  to: ['mia@example.test'], cc: ['copy@example.test'], bcc: null,
  date: '2026-01-29T10:00:00Z', seen: true, is_draft: false, is_omnipus_draft: false,
  read_by_agent: false, reply_to: null, in_reply_to: null, references: null,
  body_text: 'Notes from Alice', has_html: false, body_markdown: null, markdown_lossy: false,
  attachments: [],
}

function renderMessage(current: MailMessage) {
  fetchMailMessages.mockResolvedValue({ messages: [current], truncated: false, next_before_uid: null })
  fetchMailMessage.mockResolvedValue(current)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" initialFolder={current.folder} /></QueryClientProvider>)
}

async function openMessage(body = 'Notes from Alice') {
  fireEvent.click(await screen.findByRole('button', { name: /Quarterly review/ }, { timeout: 30000 }))
  const preview = screen.getByTestId('mail-reading-zone')
  await within(preview).findByText(body)
  return preview
}

describe('Received preview subject above its metadata (F7)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([{ agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' }])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [
      { slug: 'inbox', display_name: 'Inbox', total: 1, unread_count: 0 },
      { slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null },
      { slug: 'sent', display_name: 'Sent', total: 1, unread_count: null },
    ] })
    fetchMailMessages.mockReset()
    fetchMailMessage.mockReset()
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  })
  afterEach(() => cleanup())

  it('places the subject heading before From, To, Cc, and Date in the received preview', async () => {
    renderMessage(message)
    const preview = await openMessage()
    const heading = within(preview).getByRole('heading', { name: 'Quarterly review' })
    for (const label of ['From: Alice · alice@example.test', 'To: mia@example.test', 'Cc: copy@example.test', 'Date: 29 Jan 2026']) {
      const metadata = within(preview).getByText(label)
      expect(heading.compareDocumentPosition(metadata) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
    }
    expect(within(preview).getByText('Notes from Alice')).toBeInTheDocument()
  })

  it('labels an empty received subject and omits Cc metadata when there is no Cc recipient', async () => {
    const emptySubject = { ...message, subject: '', cc: [] }
    renderMessage(emptySubject)
    fireEvent.click(await screen.findByRole('button', { name: /alice@example.test/ }))
    const preview = screen.getByTestId('mail-reading-zone')
    await within(preview).findByText('Notes from Alice')
    expect(within(preview).getByRole('heading', { name: '(No subject)' })).toBeInTheDocument()
    expect(within(preview).queryByText(/^Cc:/)).not.toBeInTheDocument()
  })

  it('adds the heading to received mail without duplicating subject headings on drafts or sent copies', async () => {
    renderMessage(message)
    const preview = await openMessage()
    expect(within(preview).getAllByRole('heading', { name: 'Quarterly review' })).toHaveLength(1)
    cleanup()
    for (const folder of ['drafts', 'sent'] as const) {
      renderMessage({ ...message, folder, is_draft: folder === 'drafts', is_omnipus_draft: folder === 'drafts', body_markdown: 'Saved note' })
      const outgoing = await openMessage('Saved note')
      expect(within(outgoing).getAllByRole('heading', { name: 'Quarterly review' })).toHaveLength(1)
      cleanup()
    }
  })
})
