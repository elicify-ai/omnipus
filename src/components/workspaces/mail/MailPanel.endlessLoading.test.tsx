/**
 * RED — the Mail panel must not sit on the loading skeleton while the folders
 * request is being silently retried. Production QueryClient defaults retry
 * every failed query 3 times (1s/2s/4s back-off), and each attempt can take
 * up to the backend's 45s command bound, so a mailbox that cannot be reached
 * (or whose Sent folder is missing, 502 folder_missing) looked like an endless
 * spinner. The panel has its own Retry button, so the folders query must
 * surface the first failure at once (as the messages query already does).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { shouldRetryQuery } from '@/lib/queryClient'

const { fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailSummary } = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailSummary: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents, fetchMailboxes }
})
vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return { ...actual, fetchMailFolders, fetchMailSummary }
})

import { MailPanel } from './MailPanel'

describe('Mail panel endless loading', () => {
  beforeEach(() => {
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailSummary.mockResolvedValue({ items: [] })
    fetchMailFolders.mockRejectedValue(Object.assign(new Error('down'), { code: 'timeout' }))
  })
  afterEach(() => cleanup())

  it('shows the error and Retry after ONE failed folders request, not after silent retries', async () => {
    // Same retry policy as the production singleton in src/lib/queryClient.ts.
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: shouldRetryQuery, retryDelay: (a) => Math.min(1000 * 2 ** a, 30_000) },
      },
    })
    render(
      <QueryClientProvider client={client}>
        <MailPanel workspaceId="ws-1" />
      </QueryClientProvider>,
    )
    // 800 ms is far below the first production back-off (1 s).
    expect(await screen.findByTestId('mail-folders-error', {}, { timeout: 800 })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(fetchMailFolders).toHaveBeenCalledTimes(1)
  })
})
