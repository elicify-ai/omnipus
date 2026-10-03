import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-error'
import type { MailMessagePage, MailMessageSummary } from '@/lib/api/generated/openapi-types'
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

// Superseded oracle, re-pinned (W3 panel spec §11 S-11 + §8.8's re-pin list):
// the drafts-era string "This draft was changed or deleted elsewhere. The list
// has been refreshed." was replaced by the generalized message-changed surface
// "This message changed or was deleted. Refresh the list." — landed in
// a3a678b34 (cache-first panel, 2026-10-02).
const MESSAGE_CHANGED = 'This message changed or was deleted. Refresh the list.'
// The settled panel list read (W3 spec §2.4 "the panel always sends limit=25
// explicitly", §3.2 cache-first open event), landed in a3a678b34.
const OPEN_LIST_CALL = { limit: 25, mode: 'cache_first', retry: false } as const

const staleRow: MailMessageSummary = {
  message_id: '<stale-draft@test.local>', uid: 1, uidvalidity: 3,
  folder: 'drafts', subject: 'F5 stale draft', from: 'mia@test.local', from_name: null,
  to: [], cc: [], date: '2026-09-30T00:00:00Z', seen: false,
  is_draft: true, is_omnipus_draft: true, read_by_agent: false,
}
const stalePage: MailMessagePage = { messages: [staleRow], truncated: false, next_before_uid: null }

function renderDraft() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" initialFolder="drafts" /></QueryClientProvider>)
}

describe('MailPanel — stale draft recovery errors', () => {
  beforeEach(() => {
    fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@test.local' },
    ])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [
      { slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: null },
    ] })
    fetchMailMessages.mockReset().mockResolvedValue(stalePage)
    fetchMailMessage.mockReset().mockRejectedValue(new ApiError(404))
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  })
  afterEach(cleanup)

  it('does not claim the list was refreshed when its refresh returns 404', async () => {
    fetchMailMessages.mockResolvedValueOnce(stalePage).mockRejectedValueOnce(new ApiError(404))
    renderDraft()
    fireEvent.click(await screen.findByRole('button', { name: /F5 stale draft/ }))
    await waitFor(() => expect(fetchMailMessages).toHaveBeenCalledTimes(2))
    // S-11 (MailPanel.tsx::detailIsStaleReference): a 404 for a message that no
    // longer resolves always lands on the message-changed surface, whose text
    // explains and offers "Refresh list" — it never claims a refresh happened.
    const readingPane = within(screen.getByTestId('mail-reading-zone'))
    expect(await readingPane.findByText(MESSAGE_CHANGED)).toBeInTheDocument()
    expect(readingPane.getByRole('button', { name: 'Refresh list' })).toBeEnabled()
    expect(readingPane.queryByText(/has been refreshed/i)).not.toBeInTheDocument()
  })

  it('does not resolve a missing Message-ID to an unrelated draft', async () => {
    const rowWithoutMid = { ...staleRow, message_id: null }
    fetchMailMessages.mockResolvedValueOnce({ ...stalePage, messages: [rowWithoutMid] })
      .mockResolvedValueOnce({ ...stalePage, messages: [] })
    renderDraft()
    fireEvent.click(await screen.findByRole('button', { name: /F5 stale draft/ }))
    expect(await screen.findByText(MESSAGE_CHANGED)).toBeInTheDocument()
    expect(fetchMailMessage).toHaveBeenCalledTimes(1)
    expect(fetchMailMessage).toHaveBeenCalledWith('ws-1', 'mia', 'drafts', 'uid:3:1', { retry: false })
    expect(fetchMailMessages).toHaveBeenCalledTimes(2)
  })

  it('does not choose a different visible row with a duplicate Message-ID', async () => {
    const otherRow = { ...staleRow, uid: 2, subject: 'Other draft' }
    fetchMailMessages.mockResolvedValueOnce({ ...stalePage, messages: [staleRow, otherRow] })
      .mockResolvedValueOnce({ ...stalePage, messages: [otherRow] })
    renderDraft()
    fireEvent.click(await screen.findByRole('button', { name: /F5 stale draft/ }))
    expect(await screen.findByText(MESSAGE_CHANGED)).toBeInTheDocument()
    expect(fetchMailMessage).toHaveBeenCalledTimes(1)
    expect(fetchMailMessage).toHaveBeenCalledWith('ws-1', 'mia', 'drafts', 'uid:3:1', { retry: false })
    expect(screen.getByRole('button', { name: /Other draft/ })).toBeInTheDocument()
  })

  it.each([
    { status: 502, errorCode: 'server_error' },
    { status: 503, errorCode: 'backoff' },
  ])('keeps an initial $status ($errorCode) visible without draft recovery', async ({ status, errorCode }) => {
    // I2 / email-mail-view-spec.md §2.3 + US-3 AS-4: upstream failure
    // and backoff are not evidence that the selected draft disappeared.
    // Only the INITIAL UID request gets this response. beforeEach's 404
    // remains for any mistaken Message-ID fallback, so recovery is observable.
    fetchMailMessage.mockRejectedValueOnce(new ApiError(status, undefined, { code: errorCode }))
    renderDraft()

    const draftRow = await screen.findByRole('button', { name: /F5 stale draft/ })
    expect(fetchMailMessages.mock.calls).toEqual([
      ['ws-1', 'mia', 'drafts', OPEN_LIST_CALL],
    ])
    fireEvent.click(draftRow)

    // S-10 surface: the failure names its class on the caption line
    // ("Error class: <class>"), not as a bare element.
    const preview = within(screen.getByTestId('mail-reading-zone'))
    expect(await preview.findByText(`Error class: ${errorCode}`, { exact: true })).toBeVisible()
    expect(preview.getByRole('button', { name: /^Retry$/ })).toBeEnabled()
    expect(fetchMailMessage.mock.calls).toEqual([
      ['ws-1', 'mia', 'drafts', 'uid:3:1', { retry: false }],
    ])
    expect(fetchMailMessages.mock.calls).toEqual([
      ['ws-1', 'mia', 'drafts', OPEN_LIST_CALL],
    ])
    expect(screen.queryByText(MESSAGE_CHANGED, { exact: true })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /F5 stale draft/ })).toBeVisible()
  })

  it('keeps an upstream 502 visible and does not claim the list was refreshed', async () => {
    fetchMailMessage.mockRejectedValueOnce(new ApiError(404))
      .mockRejectedValueOnce(new ApiError(502, undefined, { code: 'server_error' }))
    renderDraft()
    fireEvent.click(await screen.findByRole('button', { name: /F5 stale draft/ }))
    expect(await screen.findByText('Error class: server_error')).toBeInTheDocument()
    expect(fetchMailMessage).toHaveBeenCalledTimes(2)
    expect(fetchMailMessage).toHaveBeenLastCalledWith('ws-1', 'mia', 'drafts', 'mid:<stale-draft@test.local>', { retry: false })
    expect(fetchMailMessages).toHaveBeenCalledTimes(1)
    expect(screen.queryByText(MESSAGE_CHANGED)).not.toBeInTheDocument()
  })
})
