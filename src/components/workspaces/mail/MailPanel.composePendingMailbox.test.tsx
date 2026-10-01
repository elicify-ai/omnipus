import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailSummary } = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailSummary: vi.fn(),
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
  fetchMailSummary,
}))

import { MailPanel } from './MailPanel'

function holdMailboxResponse(response: unknown[]) {
  let resolveRequest: () => void = () => { throw new Error('Mailbox request has not started') }
  fetchMailboxes.mockImplementation(() => new Promise((resolve) => {
    resolveRequest = () => resolve(response)
  }))
  return async () => { await act(async () => { resolveRequest() }) }
}

function renderMail(mailboxId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" mailboxId={mailboxId} /></QueryClientProvider>)
}

beforeEach(() => {
  sessionStorage.clear()
  fetchAgents.mockReset().mockResolvedValue([{ id: 'mia', name: 'Mia' }])
  fetchMailboxes.mockReset()
  fetchMailFolders.mockReset().mockResolvedValue({ folders: [] })
  fetchMailMessages.mockReset().mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
  fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
})
afterEach(() => cleanup())

describe('Mail Compose with a supplied mailbox and a pending mailbox lookup', () => {
  it('opens Compose before a known valid mailbox lookup settles', async () => {
    const release = holdMailboxResponse([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    renderMail('mia')
    await waitFor(() => expect(fetchMailboxes).toHaveBeenCalledTimes(1))

    const compose = screen.getByRole('button', { name: 'Compose' })
    expect(compose).toBeEnabled()
    fireEvent.click(compose)
    expect(screen.getByRole('textbox', { name: 'Message' })).toBeInTheDocument()

    await release()
    expect(compose).toBeEnabled()
  })

  it('disables Compose and points to Connectors if the supplied mailbox is not usable once lookup settles', async () => {
    const release = holdMailboxResponse([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: false, configured: true, username: 'mia@example.test' },
      { agent_id: 'other', workspace_id: 'ws-1', enabled: true, configured: false, username: 'other@example.test' },
      { agent_id: 'mia', workspace_id: 'ws-other', enabled: true, configured: true, username: 'mia@other.test' },
    ])
    renderMail('mia')
    await waitFor(() => expect(fetchMailboxes).toHaveBeenCalledTimes(1))

    const compose = screen.getByRole('button', { name: 'Compose' })
    expect(compose).toBeEnabled()
    await release()
    await waitFor(() => {
      expect(compose).toBeDisabled()
      expect(compose).toHaveAttribute('aria-describedby', 'mail-no-mailbox-help')
      expect(screen.getByTestId('mail-choose-mailbox')).toHaveTextContent('No mailbox is configured for this workspace yet.')
      expect(screen.getByRole('link', { name: 'Connect mailbox' })).toHaveAttribute('href', '#/connectors')
    })
    fireEvent.click(compose)
    expect(screen.queryByRole('textbox', { name: 'Message' })).not.toBeInTheDocument()
  })

  it('disables provisional Compose and displays Retry when mailbox lookup fails', async () => {
    let rejectRequest: () => void = () => { throw new Error('Mailbox request has not started') }
    fetchMailboxes.mockImplementation(() => new Promise((_, reject) => {
      rejectRequest = () => reject(new Error('mailbox lookup failed'))
    }))
    renderMail('mia')
    await waitFor(() => expect(fetchMailboxes).toHaveBeenCalledTimes(1))

    const compose = screen.getByRole('button', { name: 'Compose' })
    expect(compose).toBeEnabled()
    await act(async () => { rejectRequest() })
    expect(compose).toBeDisabled()
    expect(screen.getByRole('alert')).toHaveTextContent('mailbox lookup failed')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })
})
