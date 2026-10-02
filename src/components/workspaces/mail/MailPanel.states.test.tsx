/**
 * RED contract — Mail panel states. Spec §16, US-3, US-6, D29/R2-8, MC-36;
 * folder-refresh cadence per founder ruling Q-C (2026-10-02), which supersedes
 * the D25 30-second refetch (W3 §4 US-1 AS-7, MC-W3-2, MC-W3-10).
 *
 * Implement src/components/workspaces/mail/MailPanel.tsx exporting MailPanel.
 * Props: { workspaceId: string }.
 * It reads these functions from @/lib/api (add them; they are not there yet):
 *   fetchMailboxes()
 *   fetchMailFolders(workspaceId, agentId)
 *   fetchMailMessages(workspaceId, agentId, folder)
 *   fetchMailSummary(workspaceId)
 * Folders refresh at most once per eligible event (panel open with absent or
 * older-than-five-minute data, folder switch, manual Refresh, own action) —
 * never on a repeating timer, and nothing refreshes while the panel is closed.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
  markMailSeen,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
  markMailSeen: vi.fn(),
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
    fetchMailSummary,
    markMailSeen,
  }
})

async function loadPanel(): Promise<React.ComponentType<{ workspaceId: string }>> {
  // The import remains runtime-only (@vite-ignore) so a missing file fails
  // this test (BLOCKED) instead of failing collection before assertions run.
  const specifier = './MailPanel'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailPanel?: React.ComponentType<{ workspaceId: string }> }
    if (typeof mod.MailPanel !== 'function') throw new Error('MailPanel is not a function export')
    return mod.MailPanel
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailPanel not implemented — required by spec §16 / US-3. ' + detail, {
      cause: err,
    })
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

describe('Mail panel states (US-3, US-6; refresh cadence per Q-C, superseding D25)', () => {
  beforeEach(() => {
    fetchAgents.mockReset()
    fetchMailboxes.mockReset()
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset()
    fetchMailMessage.mockReset()
    fetchMailSummary.mockReset()
    markMailSeen.mockReset()
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(folders)
    fetchMailMessages.mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
    fetchMailMessage.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
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

  it('opens a list message with its uidvalidity-scoped server ref', async () => {
    fetchMailMessages.mockResolvedValue({
      messages: [{
        message_id: '<quarterly@example.test>',
        uid: 42,
        uidvalidity: 777,
        folder: 'inbox',
        subject: 'Quarterly',
        from: 'ada@example.test',
        from_name: 'Ada',
        to: ['mia@example.test'],
        cc: [],
        date: '2026-09-28T10:00:00Z',
        seen: true,
        is_draft: false,
        is_omnipus_draft: false,
        read_by_agent: false,
      }],
      truncated: false,
      next_before_uid: null,
    })

    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)
    fireEvent.click(await screen.findByRole('button', { name: /Quarterly/ }))

    await waitFor(() => {
      expect(fetchMailMessage).toHaveBeenCalledWith(
        'ws-1',
        'mia',
        'inbox',
        'uid:777:42',
        { retry: false },
      )
    })
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

  it('refreshes folders once per open event, never on a timer and never after close (Q-C supersedes D25)', async () => {
    // Founder ruling Q-C (mail-feature-decisions.md, 2026-10-02), restoring the
    // ADR P1.1 stale-gating and superseding the D25 30-second refetch:
    //   "A — the design's stale-gating stands (a live refresh on panel open or
    //   folder switch only when the data is absent or older than five minutes;
    //   manual Refresh always; own-action refresh unchanged). This restores what
    //   the founder already directed ('counts and lists should not be frequent')."
    // W3 spec §4 US-1 AS-7: when 30 seconds elapse repeatedly the folder rail and
    // message list issue no timer-driven requests ("the D25 cadence is gone") while
    // the watcher banner's saved-state summary poll is unchanged — so fetchMailSummary
    // calls are deliberately NOT asserted here. MC-W3-10 ("Timer removal is real"):
    // a 35-second advanced-clock test asserts zero folder/list requests after mount.
    // §7 Scenario 1.3: a closed panel never refreshes. §3.1: at most one live request
    // per eligible event, issued only when the data is absent or older than five
    // minutes. The fixture below carries no freshness metadata, so the data is
    // absent/unknown (US-2 AS-7 — never "just checked") and the stale-gate is open:
    // the open event issues exactly one folders fetch.
    vi.useFakeTimers()
    try {
      const MailPanel = await loadPanel()
      const view = renderPanel(<MailPanel workspaceId="ws-1" />)
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      // The open event itself: exactly one folders fetch — the absent-data arm
      // of the stale-gate (§3.1), never zero and never a second one.
      expect(fetchMailFolders.mock.calls.length).toBe(1)
      // The retired D25 interval itself: 30 seconds must produce nothing (US-1 AS-7).
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
      expect(fetchMailFolders.mock.calls.length).toBe(1)
      // MC-W3-10's 35-second form: still nothing — a timer at any cadence up to
      // and including 35 s would have fired by now.
      await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
      expect(fetchMailFolders.mock.calls.length).toBe(1)
      // Closed panel never refreshes (§7 Scenario 1.3): unmounting stops everything.
      view.unmount()
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
      expect(fetchMailFolders.mock.calls.length).toBe(1)
    } finally {
      vi.useRealTimers()
    }
  })
})
