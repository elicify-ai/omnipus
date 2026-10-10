// W3 RED pack — C10 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §4 US-4's F5/F6 panel half (§2.1 MailComposeDialog row:
// "Consume the generated reply-context result (To/Cc, quoted body) for Reply
// and Reply all; keep the existing unsaved-input preservation on failed
// send"), the landed MailReplyContextResponse shape (§2.4), and §8.4 C10's
// scope line. Expected values derived from the spec BEFORE the panel was
// read (receipts/w3-red-derivation.md).
//
// Scope note (stated gap, not an omission): the quote's "No date"
// attribution is server-built inside body_markdown (the dialog renders the
// generated body verbatim), so its F6 leg is exercised by the reply-context
// producer's own tests — the panel tests assert the quote renders verbatim
// and stays editable, which is this file's half.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const NOW = new Date('2026-10-02T10:00:00Z')

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailReplyContext,
  fetchMailSummary,
  sendMailMessage,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailReplyContext: vi.fn(),
  fetchMailSummary: vi.fn(),
  sendMailMessage: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents, fetchMailboxes }
})

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return {
    ...actual,
    fetchMailFolders,
    fetchMailMessages,
    fetchMailMessage,
    fetchMailReplyContext,
    fetchMailSummary,
    sendMailMessage,
  }
})

const FRESH_META = {
  source: 'memory',
  last_validated_at: new Date(NOW.getTime() - 40_000).toISOString(),
  stale: false,
  refresh_needed: false,
  notice_code: null,
  publication_revision: 'rev-1',
}

const REPLY_CONTEXT = {
  to: ['ada@example.test'],
  cc: ['bob@example.test'],
  bcc: [],
  subject: 'Re: Quarterly report',
  // The server's ESCAPED Markdown projection (MailReplyContext contract):
  // markup in the quote arrives markdown-escaped, so the client renders it
  // as literal text and nothing parses as a tag.
  body_markdown: 'On 28 Sep 2026, Ada wrote:\n\n> See *attached* report with \\<script\\>alert(1)\\</script\\> inside.\n',
  in_reply_to: '<quarterly@example.test>',
}

const SUMMARY = {
  uid: 42, uidvalidity: 777, message_id: '<quarterly@example.test>', folder: 'inbox',
  subject: 'Quarterly report', from: 'ada@example.test', from_name: 'Ada',
  to: ['mia@example.test'], cc: [], date: '2026-09-28T10:00:00Z', seen: true,
  is_draft: false, is_omnipus_draft: false, read_by_agent: false,
  has_attachments: false, message_ref: 'uid:777:42',
}

const DETAIL = {
  uid: 42, uidvalidity: 777, message_id: '<quarterly@example.test>', folder: 'inbox',
  subject: 'Quarterly report', from: 'ada@example.test', from_name: 'Ada',
  to: ['mia@example.test'], cc: [], bcc: [], date: '2026-09-28T10:00:00Z',
  seen: true, is_draft: false, is_omnipus_draft: false, read_by_agent: false,
  has_html: false, body_markdown: 'See attached.', body_text: 'See attached.',
  has_attachments: false, message_ref: 'uid:777:42', attachments: [],
}

const FOLDERS = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
  ],
  metadata: FRESH_META,
}

async function loadPanel(): Promise<React.ComponentType<{ workspaceId: string }>> {
  const specifier = './MailPanel'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailPanel?: React.ComponentType<{ workspaceId: string }> }
    if (typeof mod.MailPanel !== 'function') throw new Error('MailPanel is not a function export')
    return mod.MailPanel
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C10. ' + detail, { cause: err })
  }
}

async function openMessageAndCompose(mode: 'reply' | 'reply_all'): Promise<void> {
  const MailPanel = await loadPanel()
  fetchMailMessages.mockResolvedValue({
    messages: [SUMMARY],
    has_more: false, next_cursor: null, view_limit_reached: false, truncated: false, next_before_uid: null,
    metadata: FRESH_META,
  })
  fetchMailMessage.mockResolvedValue(DETAIL)
  fetchMailReplyContext.mockResolvedValue(REPLY_CONTEXT)
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MailPanel workspaceId="ws-1" />
    </QueryClientProvider>,
  )
  fireEvent.click(await screen.findByRole('button', { name: /Quarterly report/ }))
  // The reading pane is open when its Reply action is available.
  await screen.findByRole('button', { name: 'Reply' })
  fireEvent.click(screen.getByRole('button', { name: mode === 'reply' ? 'Reply' : 'Reply all' }))
  await screen.findByRole('dialog')
}

describe('MailComposeDialog reply context (C10: F5/F6 panel half)', () => {
  beforeEach(() => {
    for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailReplyContext, fetchMailSummary, sendMailMessage]) fn.mockReset()
    fetchAgents.mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(FOLDERS)
    fetchMailSummary.mockResolvedValue({ items: [] })
    sendMailMessage.mockResolvedValue({ ok: true })
  })

  afterEach(() => {
    cleanup()
  })

  it('Reply prefills the sender from the generated reply-context response and the server-built quote', async () => {
    await openMessageAndCompose('reply')
    await waitFor(() => expect(fetchMailReplyContext).toHaveBeenCalledTimes(1))
    const ctxArgs = fetchMailReplyContext.mock.calls[0]
    expect(ctxArgs[4]).toMatchObject({ mode: 'reply' })
    // To comes from the generated response's `to` — the SPA implements no
    // second recipient algorithm. Scoped to the dialog: the sender address
    // also appears in the list row and the reading pane.
    const dialog = screen.getByRole('dialog')
    await waitFor(() => expect(within(dialog).getByText('ada@example.test')).toBeInTheDocument())
    const subject = screen.getByPlaceholderText('Subject') as HTMLInputElement
    await waitFor(() => expect(subject.value).toBe('Re: Quarterly report'))
    const body = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement
    await waitFor(() => expect(body.value).toContain('> See *attached* report'))
    // The quote's markup arrives ESCAPED (the server's Markdown projection):
    // it displays as literal text in the editable body, and nothing parsed
    // as a live tag anywhere in the document.
    expect(body.value).toContain('alert(1)')
    expect(document.querySelectorAll('script').length).toBe(0)
  })

  it('Reply all additionally prefills Cc from the generated response; Bcc stays empty', async () => {
    await openMessageAndCompose('reply_all')
    await waitFor(() => expect(fetchMailReplyContext).toHaveBeenCalledTimes(1))
    expect(fetchMailReplyContext.mock.calls[0][4]).toMatchObject({ mode: 'reply_all' })
    const dialog = screen.getByRole('dialog')
    await waitFor(() => expect(within(dialog).getByText('ada@example.test')).toBeInTheDocument())
    expect(within(dialog).getByText('bob@example.test')).toBeInTheDocument()
    expect(dialog.textContent).not.toContain('carol@')
  })

  it('the quoted body is editable and the edit ships — with in_reply_to — on send', async () => {
    await openMessageAndCompose('reply')
    await waitFor(() => {
      const body = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement
      expect(body.value).toContain('> See *attached* report')
    })
    const body = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement
    fireEvent.change(body, { target: { value: `${body.value}\nAnd one more thought.` } })
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sendMailMessage).toHaveBeenCalledTimes(1))
    const sendArgs = sendMailMessage.mock.calls[0]
    const sentBody = sendArgs[2] as Record<string, unknown>
    expect(sentBody.body_markdown).toContain('> See *attached* report')
    expect(sentBody.body_markdown).toContain('And one more thought.')
    expect(sentBody.in_reply_to).toBe('<quarterly@example.test>')
    expect(sentBody.subject).toBe('Re: Quarterly report')
    expect(sentBody.to).toEqual(['ada@example.test'])
  })
})
