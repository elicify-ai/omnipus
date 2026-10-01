import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-error'
import { MailPanel } from './MailPanel'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailSummary } = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailSummary: vi.fn(),
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAgents,
  fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api/mail')>(),
  fetchMailFolders,
  fetchMailMessages,
  fetchMailSummary,
}))

function renderMail() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" /></QueryClientProvider>)
}

describe('F3 — configured mailbox cannot connect', () => {
  beforeEach(() => {
    fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', configured: true, enabled: true, username: 'mia-outage@example.test' },
    ])
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset().mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  })
  afterEach(() => cleanup())

  it('explains a closed IMAP connection, retains the mailbox identity, and retries with retry=true', async () => {
    // The real 127.0.0.1:2999 gateway response is 502 code=connect_refused.
    fetchMailFolders.mockRejectedValue(new ApiError(502, 'mail server error: connect_refused', { code: 'connect_refused' }))
    renderMail()

    const error = await screen.findByTestId('mail-folders-error')
    expect(within(error).getByText("Can't connect to this mailbox")).toBeInTheDocument()
    expect(within(error).getByText('Error class: connect_refused')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Mailbox' })).toHaveTextContent('Mia · mia-outage@example.test')
    expect(screen.queryByTestId('mail-choose-mailbox')).not.toBeInTheDocument()
    fireEvent.click(within(error).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(fetchMailFolders).toHaveBeenLastCalledWith('ws-1', 'mia', { retry: true }))
  })

  it('names the underlying connection class during watcher backoff rather than diagnosing backoff as the cause', async () => {
    fetchMailFolders.mockRejectedValue(new ApiError(503, 'mailbox in backoff', { code: 'backoff' }))
    fetchMailSummary.mockResolvedValue({ items: [{
      agent_id: 'mia', watcher_state: 'backoff', unseen_total: 0,
      last_error_class: 'connect_refused', last_success_at: null,
      last_seen_uid: null, next_attempt_at: '2026-09-30T12:00:00Z',
    }] })
    renderMail()

    const error = await screen.findByTestId('mail-folders-error')
    expect(within(error).getByText("Can't connect to this mailbox")).toBeInTheDocument()
    expect(within(error).getByText('Error class: connect_refused')).toBeInTheDocument()
    const banner = screen.getByTestId('mail-connection-banner')
    expect(banner).toHaveTextContent("Can't connect to this mailbox")
    expect(banner).toHaveTextContent('connect_refused')
    expect(banner).toHaveTextContent(/retrying at/i)
  })
})
