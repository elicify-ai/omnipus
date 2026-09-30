import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
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
    expect(await screen.findByText('404: The requested resource was not found.')).toBeInTheDocument()
    expect(screen.queryByText('This draft was changed or deleted elsewhere. The list has been refreshed.')).not.toBeInTheDocument()
  })

  it('does not resolve a missing Message-ID to an unrelated draft', async () => {
    const rowWithoutMid = { ...staleRow, message_id: null }
    fetchMailMessages.mockResolvedValueOnce({ ...stalePage, messages: [rowWithoutMid] })
      .mockResolvedValueOnce({ ...stalePage, messages: [] })
    renderDraft()
    fireEvent.click(await screen.findByRole('button', { name: /F5 stale draft/ }))
    expect(await screen.findByText('This draft was changed or deleted elsewhere. The list has been refreshed.')).toBeInTheDocument()
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
    expect(await screen.findByText('This draft was changed or deleted elsewhere. The list has been refreshed.')).toBeInTheDocument()
    expect(fetchMailMessage).toHaveBeenCalledTimes(1)
    expect(fetchMailMessage).toHaveBeenCalledWith('ws-1', 'mia', 'drafts', 'uid:3:1', { retry: false })
    expect(screen.getByRole('button', { name: /Other draft/ })).toBeInTheDocument()
  })

  it('keeps an upstream 502 visible and does not claim the list was refreshed', async () => {
    fetchMailMessage.mockRejectedValueOnce(new ApiError(404))
      .mockRejectedValueOnce(new ApiError(502, undefined, { code: 'server_error' }))
    renderDraft()
    fireEvent.click(await screen.findByRole('button', { name: /F5 stale draft/ }))
    expect(await screen.findByText('server_error')).toBeInTheDocument()
    expect(fetchMailMessage).toHaveBeenCalledTimes(2)
    expect(fetchMailMessage).toHaveBeenLastCalledWith('ws-1', 'mia', 'drafts', 'mid:<stale-draft@test.local>', { retry: false })
    expect(fetchMailMessages).toHaveBeenCalledTimes(1)
    expect(screen.queryByText('This draft was changed or deleted elsewhere. The list has been refreshed.')).not.toBeInTheDocument()
  })
})
