import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { MailMessage } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { MailPanel } from './MailPanel'

const {
  fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages,
  fetchMailMessage, fetchMailSummary, saveMailDraft, sendMailDraft,
} = vi.hoisted(() => ({
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

// US-7 / B-30: saving replaces the draft UID but retains its Message-ID.
const originalDraft: MailMessage = {
  message_id: '<draft@example.test>', uid: 1, uidvalidity: 77, folder: 'drafts',
  subject: 'Review request', from: 'mia@example.test', from_name: 'Mia',
  to: ['alice@example.test'], cc: [], bcc: [], date: '2026-09-29T10:00:00Z',
  seen: true, is_draft: true, is_omnipus_draft: true, read_by_agent: false,
  reply_to: null, in_reply_to: null, references: null, body_text: 'Agent draft',
  body_markdown: 'Agent draft', has_html: false, markdown_lossy: false, attachments: [],
}
const editedDraft: MailMessage = {
  ...originalDraft, uid: 2, body_text: 'Human-approved body', body_markdown: 'Human-approved body',
}
const sentCopy: MailMessage = { ...editedDraft, uid: 9, folder: 'sent', is_draft: false }

// F9, US-7 AS-2, B-31: a successful send removes the approved copy from Drafts.
describe('MailPanel — draft send success state', () => {
  afterEach(() => {
    cleanup()
    sessionStorage.clear()
    useUiStore.setState({ toasts: [] })
  })

  it('shows sent without querying the removed draft or showing 404 and Retry', async () => {
    sessionStorage.clear()
    useUiStore.setState({ toasts: [] })
    fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    let sent = false
    fetchMailFolders.mockReset().mockImplementation(async () => ({ folders: [
      { slug: 'inbox', display_name: 'INBOX', total: 0, unread_count: 0 },
      { slug: 'sent', display_name: 'Sent', total: sent ? 1 : 0, unread_count: null },
      { slug: 'drafts', display_name: 'Drafts', total: sent ? 0 : 1, unread_count: null },
    ] }))
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
    saveMailDraft.mockReset().mockResolvedValue(editedDraft)

    const postSendRemovedRefLookups: unknown[][] = []
    fetchMailMessages.mockReset().mockImplementation(async (_ws: string, _agent: string, folder: string) => ({
      messages: folder === 'sent' ? [sentCopy] : folder === 'drafts' && !sent
        ? [saveMailDraft.mock.calls.length > 0 ? editedDraft : originalDraft] : [],
      truncated: false, next_before_uid: null,
    }))
    fetchMailMessage.mockReset().mockImplementation(async (ws: string, agent: string, folder: string, ref: string) => {
      if (folder === 'sent') {
        if (ref === 'uid:77:9' || ref === 'mid:<draft@example.test>') return sentCopy
        throw new Error('404: The requested resource was not found')
      }
      if (folder === 'drafts' && ref === 'uid:77:1' && !sent) return originalDraft
      if (folder === 'drafts' && ref === 'uid:77:2') {
        if (!sent) return editedDraft
        postSendRemovedRefLookups.push([ws, agent, folder, ref])
        throw new Error('404: The requested resource was not found')
      }
      throw new Error(`Unexpected message lookup: ${folder}/${ref}`)
    })
    sendMailDraft.mockReset().mockImplementation(async () => {
      sent = true
      return { message_id: '<draft@example.test>', sent_saved: true, save_warning: null, draft_cleanup_warning: null }
    })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const onLocationChange = vi.fn()
    render(
      <QueryClientProvider client={client}>
        <MailPanel workspaceId="ws-1" initialFolder="drafts" onLocationChange={onLocationChange} />
      </QueryClientProvider>,
    )

    // Superseded oracle, re-pinned (W3 spec §2.4 "the panel always sends
    // limit=25 explicitly" + §3.2 cache-first open event; landed in a3a678b34,
    // 2026-10-02): the drafts list read now carries mode and an explicit limit.
    await waitFor(() => expect(fetchMailMessages).toHaveBeenCalledWith('ws-1', 'mia', 'drafts', { limit: 25, mode: 'cache_first', retry: false }))
    expect(await screen.findByTestId('mail-message-list')).toHaveTextContent('Review request')
    fireEvent.click(await screen.findByRole('button', { name: /Review request/ }))
    expect(await screen.findByText('Agent draft')).toBeInTheDocument()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Edit' })) })
    await act(async () => {
      fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), {
        target: { value: 'Human-approved body' },
      })
    })
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Save' })) })
    await waitFor(() => expect(saveMailDraft).toHaveBeenCalledWith('ws-1', 'mia', 'uid:77:1', expect.objectContaining({
      body_markdown: 'Human-approved body', uidvalidity: 77, uid: 1,
    })))
    await waitFor(() => expect(fetchMailMessage).toHaveBeenCalledWith('ws-1', 'mia', 'drafts', 'uid:77:2', { retry: false }))
    expect(await screen.findByText('Human-approved body')).toBeInTheDocument()

    const listCallsBeforeSend = fetchMailMessages.mock.calls.length
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Send' })) })
    await waitFor(() => expect(sendMailDraft).toHaveBeenCalledWith('ws-1', 'mia', 'uid:77:2', {
      to: ['alice@example.test'], cc: [], bcc: [], subject: 'Review request',
      body_markdown: 'Human-approved body', uidvalidity: 77, uid: 2, keep_attachment_parts: [],
    }))
    await waitFor(() => expect(useUiStore.getState().toasts).toEqual(expect.arrayContaining([
      expect.objectContaining({ message: 'Draft sent', variant: 'success' }),
    ])))
    await waitFor(() => expect(fetchMailMessages.mock.calls.length).toBeGreaterThan(listCallsBeforeSend))
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Drafts' })).toHaveTextContent('0'))
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Sent' })).toHaveTextContent('1'))
    await waitFor(() => expect(client.isFetching()).toBe(0))

    expect(postSendRemovedRefLookups).toEqual([])
    expect(screen.queryByText('404: The requested resource was not found')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^retry$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^send$/i })).not.toBeInTheDocument()
    expect(onLocationChange).not.toHaveBeenLastCalledWith({ mailboxId: 'mia', folder: 'drafts', messageRef: 'uid:77:2' })
  })
})
