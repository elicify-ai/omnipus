import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailSummary,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailSummary: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents,
    fetchMailboxes,
  }
})

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return {
    ...actual,
    fetchMailFolders,
    fetchMailMessages,
    fetchMailSummary,
  }
})

import { MailPanel } from './MailPanel'

const originalScrollIntoView = Element.prototype.scrollIntoView

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MailPanel workspaceId="ws-1" />
    </QueryClientProvider>,
  )
}

describe('Mail panel mailbox ownership labels', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn()
    fetchAgents.mockReset()
    fetchMailboxes.mockReset()
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset()
    fetchMailSummary.mockReset()

    fetchAgents.mockResolvedValue([
      { figure: 'Omnipus', role: 'general', id: 'agent', name: 'Mia' },
      { figure: 'Omnipus', role: 'general', id: 'alice', name: 'Ava' },
    ])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'agent', workspace_id: 'ws-1', enabled: true, configured: true, username: 'agent@test.local' },
      { agent_id: 'alice', workspace_id: 'ws-1', enabled: true, configured: true, username: 'alice@test.local' },
      { agent_id: 'missing-agent', workspace_id: 'ws-1', enabled: true, configured: true, username: 'orphan@test.local' },
    ])
    fetchMailFolders.mockResolvedValue({ folders: [] })
    fetchMailMessages.mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
    fetchMailSummary.mockResolvedValue({ items: [] })
  })

  afterEach(() => {
    Element.prototype.scrollIntoView = originalScrollIntoView
    cleanup()
  })

  it('joins every mailbox to its owning agent name and falls back to the agent id', async () => {
    renderPanel()

    const selector = await screen.findByRole('combobox', { name: 'Mailbox' })
    await waitFor(() => expect(selector).toHaveTextContent('Mia · agent@test.local'))
    fireEvent.click(selector)

    expect(await screen.findByRole('option', { name: 'Mia · agent@test.local' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Ava · alice@test.local' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'missing-agent · orphan@test.local' })).toBeInTheDocument()
    expect(screen.queryByText(/^agent$/)).not.toBeInTheDocument()
    expect(screen.queryByText(/^alice$/)).not.toBeInTheDocument()
  })
})
