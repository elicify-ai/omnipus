import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
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

// US-7: the save response identifies a replacement copy of the same Message-ID.
const oldDraft: MailMessage = {
  message_id: '<draft@example.test>',
  uid: 1,
  uidvalidity: 77,
  folder: 'drafts',
  subject: 'Review request',
  from: 'mia@example.test',
  from_name: 'Mia',
  to: ['alice@example.test'],
  cc: [],
  date: '2026-09-29T10:00:00Z',
  seen: true,
  is_draft: true,
  is_omnipus_draft: true,
  read_by_agent: false,
  reply_to: null,
  in_reply_to: null,
  references: null,
  body_text: 'Agent draft',
  has_html: false,
  bcc: null,
  attachments: [],
  body_markdown: 'Agent draft',
  markdown_lossy: false,
}
const savedDraft: MailMessage = {
  ...oldDraft,
  uid: 2,
  body_text: 'Human-approved body',
  body_markdown: 'Human-approved body',
}

function renderDraft() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MailPanel workspaceId="ws-1" initialFolder="drafts" />
    </QueryClientProvider>,
  )
}

async function openOldDraft() {
  fireEvent.click(await screen.findByRole('button', { name: /Review request/ }))
  expect(await screen.findByText('Agent draft')).toBeInTheDocument()
}

function editBody() {
  fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
  fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), {
    target: { value: 'Human-approved body' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
}

describe('MailPanel — saving a replacement draft (US-7)', () => {
  beforeEach(() => {
    useUiStore.setState({ toasts: [] })
    fetchAgents.mockReset().mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [
      { slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null },
    ] })
    fetchMailMessages.mockReset().mockImplementation(async () => ({
      messages: [fetchMailMessages.mock.calls.length === 1 ? oldDraft : savedDraft],
      truncated: false,
      next_before_uid: null,
    }))
    fetchMailMessage.mockReset().mockImplementation(async (_ws: string, _agent: string, _folder: string, ref: string) => {
      if (ref === 'uid:77:1') return oldDraft
      if (ref === 'uid:77:2') return savedDraft
      throw new Error(`Unexpected draft ref: ${ref}`)
    })
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
    saveMailDraft.mockReset().mockResolvedValue(savedDraft)
    sendMailDraft.mockReset().mockResolvedValue({ message_id: '<draft@example.test>', sent_saved: true, save_warning: null, draft_cleanup_warning: null })
  })

  afterEach(() => {
    cleanup()
    useUiStore.setState({ toasts: [] })
  })

  it('selects the returned UID, refreshes Drafts and preview, and sends the human-approved copy', async () => {
    let releaseSave: (updated: MailMessage) => void = () => { throw new Error('Save not started') }
    saveMailDraft.mockImplementationOnce(() => new Promise<MailMessage>((resolve) => { releaseSave = resolve }))
    renderDraft()
    await openOldDraft()
    const listCallsBeforeSave = fetchMailMessages.mock.calls.length
    const folderCallsBeforeSave = fetchMailFolders.mock.calls.length
    editBody()

    await waitFor(() => expect(saveMailDraft).toHaveBeenCalledWith('ws-1', 'mia', 'uid:77:1', expect.objectContaining({
      to: ['alice@example.test'], subject: 'Review request', body_markdown: 'Human-approved body', uidvalidity: 77, uid: 1,
    })))
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument()
    await act(async () => { releaseSave(savedDraft) })

    await waitFor(() => expect(fetchMailMessages.mock.calls.length).toBeGreaterThan(listCallsBeforeSave))
    await waitFor(() => expect(fetchMailFolders.mock.calls.length).toBeGreaterThan(folderCallsBeforeSave))
    await waitFor(() => expect(fetchMailMessage).toHaveBeenCalledWith('ws-1', 'mia', 'drafts', 'uid:77:2', { retry: false }))
    expect(await screen.findByText('Human-approved body')).toBeInTheDocument()
    expect(screen.queryByText('Agent draft')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Review request/ })).toHaveAttribute('aria-current', 'true')
    expect(useUiStore.getState().toasts).toEqual(expect.arrayContaining([
      expect.objectContaining({ message: 'Draft saved', variant: 'success' }),
    ]))

    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sendMailDraft).toHaveBeenCalledWith('ws-1', 'mia', 'uid:77:2', expect.objectContaining({
      to: ['alice@example.test'], subject: 'Review request', body_markdown: 'Human-approved body', uidvalidity: 77, uid: 2,
    })))
    expect(sendMailDraft).toHaveBeenCalledTimes(1)
  })

  it('keeps the editor and the human edit available when saving fails', async () => {
    saveMailDraft.mockRejectedValueOnce(Object.assign(new Error('stale'), { code: 'stale_draft' }))
    renderDraft()
    await openOldDraft()
    editBody()

    await waitFor(() => expect(useUiStore.getState().toasts).toEqual(expect.arrayContaining([
      expect.objectContaining({ message: 'stale_draft', variant: 'error' }),
    ])))
    expect(screen.getByRole('textbox', { name: 'Message' })).toHaveTextContent('Human-approved body')
    expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument()
    expect(sendMailDraft).not.toHaveBeenCalled()
  })

  it('shows a non-blocking cleanup warning after selecting the replacement draft', async () => {
    saveMailDraft.mockResolvedValueOnce({ ...savedDraft, draft_cleanup_warning: 'A duplicate draft may exist' })
    renderDraft()
    await openOldDraft()
    editBody()

    await waitFor(() => expect(useUiStore.getState().toasts).toEqual(expect.arrayContaining([
      expect.objectContaining({ message: 'A duplicate draft may exist', variant: 'warning' }),
    ])))
    expect(await screen.findByText('Human-approved body')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled()
  })

  it('aligns the draft editor rows and message with the preview padding token', async () => {
    renderDraft()
    await openOldDraft()
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))

    const toRow = screen.getByRole('textbox', { name: 'To' }).closest('[data-compose-header-row]')
    const subjectRow = screen.getByRole('textbox', { name: 'Subject' }).closest('[data-compose-header-row]')
    const messageRegion = screen.getByRole('textbox', { name: 'Message' }).closest('[data-compose-message-region]')
    expect(toRow).toHaveClass('px-[var(--space-3)]')
    expect(subjectRow).toHaveClass('px-[var(--space-3)]')
    expect(messageRegion).toHaveClass('px-[var(--space-3)]')
  })
})
