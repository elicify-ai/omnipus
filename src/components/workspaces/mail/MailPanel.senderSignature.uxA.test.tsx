import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { MAIL_HTML_FRAME_SANDBOX } from './MailHtmlFrame'
import { MailPanel } from './MailPanel'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, mintMailSignaturePreviewToken } = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
  mintMailSignaturePreviewToken: vi.fn(),
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
  mintMailSignaturePreviewToken,
}))

const signatureHtml = '<strong>Mia at Omnipus</strong><br>Reply to me directly'
const draft: MailMessage = {
  message_id: '<draft@example.test>', uid: 4, uidvalidity: 77, folder: 'drafts',
  subject: 'Review request', from: 'mia@example.test', from_name: 'Mia',
  to: ['alice@example.test'], cc: [], bcc: null, date: '2026-09-29T10:00:00Z',
  seen: true, is_draft: true, is_omnipus_draft: true, read_by_agent: false,
  reply_to: null, in_reply_to: null, references: null, body_text: 'Agent draft',
  has_html: false, attachments: [], body_markdown: 'Agent draft', markdown_lossy: false,
}

function renderPanel(initialFolder?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MailPanel workspaceId="ws-1" initialFolder={initialFolder} />
    </QueryClientProvider>,
  )
}

async function expectSignatureFrame() {
  await waitFor(() => expect(mintMailSignaturePreviewToken).toHaveBeenCalledWith({ signature_html: signatureHtml }))
  const frame = await screen.findByTitle('Mailbox signature preview')
  expect(frame).toHaveAttribute('src', '/mail-preview/html/signature-token')
  expect(frame).toHaveAttribute('sandbox', MAIL_HTML_FRAME_SANDBOX)
  expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(frame).not.toHaveAttribute('srcdoc')
  // The mailbox HTML is never injected into the parent document.
  expect(document.querySelector('strong')).toBeNull()
}

describe('Mail sender and signature before approval (F2)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    fetchAgents.mockReset().mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([{ agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test', signature_html: signatureHtml }])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [{ slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null }] })
    fetchMailMessages.mockReset().mockResolvedValue({ messages: [draft], truncated: false, next_before_uid: null })
    fetchMailMessage.mockReset().mockResolvedValue(draft)
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
    mintMailSignaturePreviewToken.mockReset().mockResolvedValue({ token: 'signature-token', expires_in_seconds: 120 })
  })

  afterEach(() => cleanup())

  it('shows the selected agent and its actual signature in Compose', async () => {
    renderPanel()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Compose' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }))
    const composer = screen.getByRole('dialog')
    expect(within(composer).getByText('Mia · mia@example.test')).toBeInTheDocument()
    expect(within(composer).getByText('From')).toBeInTheDocument()
    await expectSignatureFrame()
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument()
  })

  it('shows the same sender and signature while editing an agent draft', async () => {
    renderPanel('drafts')
    fireEvent.click(await screen.findByRole('button', { name: /Review request/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    expect(within(screen.getByRole('region', { name: 'Draft' })).getByText('Mia · mia@example.test')).toBeInTheDocument()
    await expectSignatureFrame()
  })

  it('does not mint a signature frame for a mailbox with no signature', async () => {
    fetchMailboxes.mockResolvedValueOnce([{ agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' }])
    renderPanel()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Compose' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }))
    expect(within(screen.getByRole('dialog')).getByText('Mia · mia@example.test')).toBeInTheDocument()
    expect(screen.queryByTitle('Mailbox signature preview')).not.toBeInTheDocument()
    expect(mintMailSignaturePreviewToken).not.toHaveBeenCalled()
  })
})
