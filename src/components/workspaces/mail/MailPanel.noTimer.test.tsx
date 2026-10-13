// W3 RED pack — C6 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §4 US-1 AS-7, MC-W3-10, FR-W3-3, §7 scenario 1.6
// (35 s, then 70 s), §8.7 counterexample mutation 2. Expected values derived
// from the spec BEFORE the panel was read (receipts/w3-red-derivation.md).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const NOW = new Date('2026-10-02T10:00:00Z')

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
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

async function loadPanel(): Promise<React.ComponentType<{ workspaceId: string }>> {
  const specifier = './MailPanel'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailPanel?: React.ComponentType<{ workspaceId: string }> }
    if (typeof mod.MailPanel !== 'function') throw new Error('MailPanel is not a function export')
    return mod.MailPanel
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C6. ' + detail, { cause: err })
  }
}

describe('MailPanel has no repeating timer (C6: US-1 AS-7, MC-W3-10, scenario 1.6)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary]) fn.mockReset()
    fetchAgents.mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue({
      folders: [
        { slug: 'inbox', display_name: 'INBOX', total: 2, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
        { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
        { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      ],
      metadata: FRESH_META,
    })
    fetchMailMessages.mockResolvedValue({
      messages: [], has_more: false, next_cursor: null, view_limit_reached: false,
      truncated: false, next_before_uid: null, metadata: FRESH_META,
    })
    fetchMailMessage.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('scenario 1.6 — 35 s then 70 s produce zero folder/list timer requests; the summary cadence is the only one left', async () => {
    const MailPanel = await loadPanel()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MailPanel workspaceId="ws-1" />
      </QueryClientProvider>,
    )
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The open event's own requests are in: 1 folders read, 1 list read.
    expect(fetchMailFolders).toHaveBeenCalledTimes(1)
    expect(fetchMailMessages).toHaveBeenCalledTimes(1)
    const foldersAfterOpen = fetchMailFolders.mock.calls.length
    const listAfterOpen = fetchMailMessages.mock.calls.length
    // Scenario 1.6's first leg: the clock advances 35 seconds.
    await act(async () => { await vi.advanceTimersByTimeAsync(35_000) })
    expect(fetchMailFolders).toHaveBeenCalledTimes(foldersAfterOpen)
    expect(fetchMailMessages).toHaveBeenCalledTimes(listAfterOpen)
    // …then to 70 seconds: still nothing (MC-W3-10's zero-timer form).
    await act(async () => { await vi.advanceTimersByTimeAsync(70_000) })
    expect(fetchMailFolders).toHaveBeenCalledTimes(foldersAfterOpen)
    expect(fetchMailMessages).toHaveBeenCalledTimes(listAfterOpen)
    // The watcher banner's saved-state summary poll is unchanged: it fired on
    // its 30-second cadence (at 30 s and 60 s) and never dialled mail.
    const summaryCalls = fetchMailSummary.mock.calls.length
    expect(summaryCalls).toBeGreaterThanOrEqual(2)
    expect(fetchMailFolders).toHaveBeenCalledTimes(foldersAfterOpen)
  })

  it('MC-W3-10 — FOLDERS_REFETCH_MS no longer exists in MailPanel.tsx', async () => {
    const source = readFileSync(join(process.cwd(), 'src/components/workspaces/mail/MailPanel.tsx'), 'utf8')
    // The CONSTANT is gone — not merely renamed (a comment mentioning the
    // retired name does not keep a timer alive, so the pin targets the
    // declaration and every use, not the substring).
    expect(source).not.toMatch(/const FOLDERS_REFETCH_MS/)
    expect(source).not.toMatch(/FOLDERS_REFETCH_MS\s*=/)
    // Exactly ONE refetchInterval remains in the panel — the watcher banner's
    // saved-state summary poll (FR-W3-3); no folder/list/count cadence exists.
    const refetchIntervals = source.match(/refetchInterval:/g) ?? []
    expect(refetchIntervals).toHaveLength(1)
    expect(source).toMatch(/refetchInterval: SUMMARY_REFETCH_MS/)
  })
})
