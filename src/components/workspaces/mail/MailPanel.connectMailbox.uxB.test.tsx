import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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

describe('F4 — Mail without a configured workspace mailbox', () => {
  beforeEach(() => {
    fetchAgents.mockReset().mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockReset().mockResolvedValue([])
    fetchMailFolders.mockReset().mockResolvedValue({ folders: [] })
    fetchMailMessages.mockReset().mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
    window.location.hash = ''
  })
  afterEach(() => {
    cleanup()
    window.location.hash = ''
  })

  it('offers a primary Connect mailbox route to Connectors Email/Add mailbox and explains disabled Compose', async () => {
    const view = renderMail()
    const empty = await screen.findByTestId('mail-choose-mailbox')
    const connect = screen.getByRole('link', { name: 'Connect mailbox' })
    expect(empty).toContainElement(connect)
    expect(connect).toHaveAttribute('href', '#/connectors')
    expect(connect).toHaveClass('bg-[var(--color-accent)]')
    const compose = screen.getByRole('button', { name: 'Compose' })
    expect(compose).toBeDisabled()
    expect(compose).toHaveAttribute('aria-describedby', 'mail-no-mailbox-help')
    expect(screen.getByText(/Email.*Add mailbox/i)).toHaveAttribute('id', 'mail-no-mailbox-help')

    fireEvent.click(connect)
    await waitFor(() => expect(window.location.hash).toBe('#/connectors'))
    view.unmount()

    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    renderMail()
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' })).toHaveTextContent('Mia · mia@example.test'))
    expect(screen.getByRole('button', { name: 'Compose' })).toBeEnabled()
    expect(screen.queryByRole('link', { name: 'Connect mailbox' })).not.toBeInTheDocument()
  })
})
