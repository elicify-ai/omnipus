/**
 * RED contract — Mail panel states. Spec §16, US-3, US-6, D25, D29/R2-8, MC-36.
 *
 * Implement src/components/workspaces/mail/MailPanel.tsx exporting MailPanel.
 * Props: { workspaceId: string }.
 * It reads these functions from @/lib/api (add them; they are not there yet):
 *   fetchMailboxes()
 *   fetchMailFolders(workspaceId, agentId)
 *   fetchMailMessages(workspaceId, agentId, folder)
 *   fetchMailSummary(workspaceId)
 * While mounted it refetches folders every 30 seconds and stops when unmounted (D25).
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const { fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailSummary } = vi.hoisted(() => ({
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailSummary: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailSummary }
})

async function loadPanel(): Promise<React.ComponentType<{ workspaceId: string }>> {
  // The specifier is built at runtime so a missing file fails the test
  // (BLOCKED) instead of failing collection before any assertion runs.
  const specifier = './' + 'MailPanel'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailPanel?: React.ComponentType<{ workspaceId: string }> }
    if (typeof mod.MailPanel !== 'function') throw new Error('MailPanel is not a function export')
    return mod.MailPanel
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailPanel not implemented — required by spec §16 / US-3. ' + detail)
  }
}

function renderPanel(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

const folders = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 2, unread_count: 1 },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null },
  ],
}

describe('Mail panel states (US-3, US-6, D25)', () => {
  beforeEach(() => {
    fetchMailboxes.mockReset()
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset()
    fetchMailSummary.mockReset()
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(folders)
    fetchMailMessages.mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
    fetchMailSummary.mockResolvedValue({ items: [] })
  })

  afterEach(() => cleanup())

  it('lists Inbox, Sent and Drafts from the live folder payload', async () => {
    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)
    expect(await screen.findByText('INBOX')).toBeInTheDocument()
    expect(screen.getByText('Sent')).toBeInTheDocument()
    expect(screen.getByText('Drafts')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
  })

  it('names the error class and offers retry instead of an empty list (US-3 AS-4)', async () => {
    fetchMailFolders.mockRejectedValue(Object.assign(new Error('down'), { code: 'connect_refused' }))
    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)
    expect(await screen.findByText(/connect_refused/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    expect(screen.queryByText(/no messages/i)).not.toBeInTheDocument()
  })

  it('shows a read-by-agent tag only when the flag says so (US-6 AS-4)', async () => {
    fetchMailMessages.mockResolvedValue({
      messages: [
        { uid: 1, subject: 'Handled', from: 'a@b.test', seen: true, read_by_agent: true },
        { uid: 2, subject: 'Untouched', from: 'c@d.test', seen: false, read_by_agent: false },
      ],
      truncated: false,
      next_before_uid: null,
    })
    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)
    expect(await screen.findByText('Handled')).toBeInTheDocument()
    const tags = screen.getAllByText(/read by agent/i)
    expect(tags).toHaveLength(1)
  })

  it('shows retrying-at when the watcher is backing off (D29/R2-8)', async () => {
    fetchMailSummary.mockResolvedValue({
      items: [{
        agent_id: 'mia', unseen_total: 0, watcher_state: 'backoff',
        last_error_class: 'connect_refused', last_success_at: null, last_seen_uid: null,
        next_attempt_at: '2026-09-26T15:04:00Z',
      }],
    })
    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)
    expect(await screen.findByText(/retrying at/i)).toBeInTheDocument()
  })

  it('refetches folders after 30 seconds and not after the panel is closed (D25)', async () => {
    vi.useFakeTimers()
    try {
      const MailPanel = await loadPanel()
      const view = renderPanel(<MailPanel workspaceId="ws-1" />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      const before = fetchMailFolders.mock.calls.length
      expect(before).toBeGreaterThan(0)
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
      expect(fetchMailFolders.mock.calls.length).toBeGreaterThan(before)
      const atClose = fetchMailFolders.mock.calls.length
      view.unmount()
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
      expect(fetchMailFolders.mock.calls.length).toBe(atClose)
    } finally {
      vi.useRealTimers()
    }
  })
})
