// W3 RED pack — C1 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §7 scenarios 1.1/1.7/1.3/1.4, §4 US-1 AS-1/AS-3/AS-4/AS-8,
// §4 US-2 AS-4/AS-6/AS-7, §7 scenarios 2.2/2.4/2.6, §11 states S-1/S-5/S-6/S-12,
// MC-W3-2. Expected values derived from the spec BEFORE the panel was read
// (receipts/w3-red-derivation.md). The unit under test is the real MailPanel;
// the mocks stand at the SPA's network edge (@/lib/api, @/lib/api/mail) —
// the cache-view adapter, presence adapter and formatters inside the panel
// all run REAL (never mocked here).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const NOW = new Date('2026-10-02T10:00:00Z')
const iso = (msBeforeNow: number): string => new Date(NOW.getTime() - msBeforeNow).toISOString()

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
  markMailSeen,
  mintMailHtmlPreviewToken,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
  markMailSeen: vi.fn(),
  mintMailHtmlPreviewToken: vi.fn(),
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
    mintMailHtmlPreviewToken,
  }
})

import { formatMailTime } from './mail-format'

function rows(prefix: string, count: number) {
  return Array.from({ length: count }, (_, i) => ({
    uid: i + 1,
    uidvalidity: 777,
    message_id: `<${prefix}-${i}@example.test>`,
    folder: 'inbox',
    subject: `${prefix} ${i + 1}`,
    from: 'ada@example.test',
    from_name: 'Ada',
    to: ['mia@example.test'],
    cc: [],
    date: '2026-09-28T10:00:00Z',
    seen: true,
    is_draft: false,
    is_omnipus_draft: false,
    read_by_agent: false,
    has_attachments: false,
    message_ref: 'uid:777:1',
  }))
}

const page = (over: Record<string, unknown> = {}) => ({
  messages: rows('Cached', 2),
  has_more: false,
  next_cursor: null,
  view_limit_reached: false,
  truncated: false,
  next_before_uid: null,
  metadata: {
    source: 'memory',
    last_validated_at: iso(40_000),
    stale: false,
    refresh_needed: false,
    notice_code: null,
    publication_revision: 'rev-list-1',
  },
  ...over,
})

const folderList = (metadata: Record<string, unknown> | null) => ({
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 2, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
  ],
  ...(metadata !== null ? { metadata } : {}),
})

const FRESH = {
  source: 'memory',
  last_validated_at: iso(40_000),
  stale: false,
  refresh_needed: false,
  notice_code: null,
  publication_revision: 'rev-folders-1',
}

const STALE = {
  source: 'memory',
  last_validated_at: iso(7 * 60_000),
  stale: true,
  refresh_needed: true,
  notice_code: null,
  publication_revision: 'rev-folders-0',
}

const LIST_STALE = { ...STALE, publication_revision: 'rev-list-0' }

async function loadPanel(): Promise<React.ComponentType<{ workspaceId: string }>> {
  const specifier = './MailPanel'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailPanel?: React.ComponentType<{ workspaceId: string }> }
    if (typeof mod.MailPanel !== 'function') throw new Error('MailPanel is not a function export')
    return mod.MailPanel
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C1. ' + detail, { cause: err })
  }
}

function mount(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

describe('MailPanel cache-first display (C1: scenarios 1.1, 1.7, 1.3, 1.4; US-2 AS-4/AS-6/AS-7)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, markMailSeen, mintMailHtmlPreviewToken]) fn.mockReset()
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(folderList(FRESH))
    fetchMailMessages.mockResolvedValue(page())
    fetchMailMessage.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
    mintMailHtmlPreviewToken.mockResolvedValue({ token: 'tok-1' })
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('scenario 1.1 — a 40-second-fresh cache renders immediately with "Checked 40 seconds ago" and dials nothing live (US-1 AS-8)', async () => {
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The cached rows render before any live read could settle.
    expect(screen.getByText('Cached 1')).toBeInTheDocument()
    expect(screen.getByText('Cached 2')).toBeInTheDocument()
    expect(screen.getByTestId('mail-freshness-line')).toHaveTextContent('Checked 40 seconds ago')
    // Exactly one cache-first read per surface; ZERO mode=live requests (MC-W3-2).
    expect(fetchMailFolders).toHaveBeenCalledTimes(1)
    expect(fetchMailFolders.mock.calls[0][2]).toMatchObject({ mode: 'cache_first' })
    expect(fetchMailMessages).toHaveBeenCalledTimes(1)
    expect(fetchMailMessages.mock.calls[0][3]).toMatchObject({ mode: 'cache_first', limit: 25 })
    expect(fetchMailFolders.mock.calls.every((c) => (c[2] as { mode?: string })?.mode !== 'live')).toBe(true)
    expect(fetchMailMessages.mock.calls.every((c) => (c[3] as { mode?: string })?.mode !== 'live')).toBe(true)
  })

  it('scenario 1.7 — a 7-minute-stale open renders cached rows at once, dials exactly one live, updates in place without a skeleton', async () => {
    fetchMailFolders
      .mockResolvedValueOnce(folderList(STALE)) // the open's cache-first leg
    fetchMailMessages
      .mockResolvedValueOnce(page({ metadata: LIST_STALE })) // the open's cache-first leg
    // Both live legs hang until the test releases them — the deferred edge
    // lets the test observe the in-flight state honestly.
    let releaseFoldersLive: (value: unknown) => void = () => undefined
    let releaseListLive: (value: unknown) => void = () => undefined
    fetchMailFolders.mockImplementationOnce(() => new Promise((resolve) => { releaseFoldersLive = resolve }))
    fetchMailMessages.mockImplementationOnce(() => new Promise((resolve) => { releaseListLive = resolve }))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The cache legs settled and each issued its ONE live leg — in flight now.
    expect(fetchMailFolders).toHaveBeenCalledTimes(2)
    expect(fetchMailFolders.mock.calls[1][2]).toMatchObject({ mode: 'live' })
    expect(fetchMailMessages).toHaveBeenCalledTimes(2)
    expect(fetchMailMessages.mock.calls[1][3]).toMatchObject({ mode: 'live' })
    // Stale-and-checking: the cached rows stay visible, no skeleton (S-5).
    expect(screen.getByText('Cached 1')).toBeInTheDocument()
    expect(screen.queryByTestId('mail-list-loading')).not.toBeInTheDocument()
    expect(screen.getByTestId('mail-freshness-line')).toHaveTextContent('Last checked 7 minutes ago · Checking…')
    // The live legs settle: rows update in place, the line resolves to S-3.
    await act(async () => {
      releaseFoldersLive(folderList({ ...STALE, publication_revision: 'rev-folders-2' }))
      releaseListLive(page({ messages: rows('Lived', 2), metadata: { ...STALE, source: 'live', last_validated_at: NOW.toISOString(), publication_revision: 'rev-list-2' } }))
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.getByText('Lived 1')).toBeInTheDocument()
    expect(screen.queryByText('Cached 1')).not.toBeInTheDocument()
    expect(screen.getByTestId('mail-freshness-line')).toHaveTextContent('Checked just now')
    // Still exactly one live request per surface (never 2+ — MC-W3-2).
    expect(fetchMailFolders).toHaveBeenCalledTimes(2)
    expect(fetchMailMessages).toHaveBeenCalledTimes(2)
  })

  it('scenario 2.2 — a failed live refresh keeps the stale rows, the ORIGINAL checked time, and Retry (US-2 AS-4)', async () => {
    fetchMailFolders.mockResolvedValue(folderList(FRESH))
    fetchMailMessages
      .mockResolvedValueOnce(page({ metadata: LIST_STALE })) // the cache leg settles stale
      .mockRejectedValueOnce(new Error('timeout while dialing')) // its one live leg fails
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The stale rows remain — never a skeleton, never an empty list.
    expect(screen.getByText('Cached 1')).toBeInTheDocument()
    // The line shows the ORIGINAL last-validated time — never advanced.
    const expectedTime = formatMailTime(LIST_STALE.last_validated_at)
    expect(screen.getByTestId('mail-freshness-line')).toHaveTextContent(
      `Couldn't refresh — showing messages as of ${expectedTime}.`,
    )
    expect(screen.getByTestId('mail-freshness-line').textContent).not.toContain('Checking')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    // No further request fired by the failure itself.
    expect(fetchMailMessages).toHaveBeenCalledTimes(2)
  })

  it('scenario 2.4 — a cache_unavailable notice shows once with live rows and is dismissible (US-2 AS-6, S-12)', async () => {
    fetchMailFolders.mockResolvedValue(folderList({ ...FRESH, source: 'live', last_validated_at: NOW.toISOString() }))
    fetchMailMessages.mockResolvedValue(page({
      metadata: { source: 'live', last_validated_at: NOW.toISOString(), stale: false, refresh_needed: false, notice_code: 'cache_unavailable', publication_revision: 'rev-list-1' },
    }))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getAllByText('Mail cache unavailable; using live access.')).toHaveLength(1)
    // The freshness line reflects the LIVE source — the notice never masks it.
    expect(screen.getByTestId('mail-freshness-line')).toHaveTextContent('Checked just now')
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.queryByText('Mail cache unavailable; using live access.')).not.toBeInTheDocument()
  })

  it('scenario 1.4 — source=none shows the loading state and exactly one live read populates the list; never an empty mailbox (US-1 AS-4)', async () => {
    fetchMailFolders.mockResolvedValue(folderList(FRESH))
    fetchMailMessages.mockResolvedValueOnce(page({
      messages: [],
      metadata: { source: 'none', last_validated_at: null, stale: false, refresh_needed: true, notice_code: null, publication_revision: null },
    }))
    let releaseLive: (value: unknown) => void = () => undefined
    fetchMailMessages.mockImplementationOnce(() => new Promise((resolve) => { releaseLive = resolve }))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The cache-first leg answered "no cache": S-1's loading state shows while
    // the one live leg runs — and "source=none" is NEVER displayed as an empty
    // mailbox (US-1 AS-4), so no "No messages" rendering may appear either.
    expect(fetchMailMessages).toHaveBeenCalledTimes(2)
    expect(fetchMailMessages.mock.calls[1][3]).toMatchObject({ mode: 'live' })
    expect(screen.getByLabelText('Loading messages')).toBeInTheDocument()
    expect(screen.queryByText('No messages')).not.toBeInTheDocument()
    await act(async () => {
      releaseLive(page({ messages: rows('Cold', 2) }))
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.getByText('Cold 1')).toBeInTheDocument()
    expect(fetchMailMessages).toHaveBeenCalledTimes(2) // never a second live
  })

  it('scenario 1.3 — a closed panel issues no folder/list/header request, ever (US-1 AS-3)', async () => {
    const MailPanel = await loadPanel()
    const view = mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const foldersAtClose = fetchMailFolders.mock.calls.length
    const listAtClose = fetchMailMessages.mock.calls.length
    view.unmount()
    for (let cycle = 0; cycle < 5; cycle++) {
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    }
    expect(fetchMailFolders).toHaveBeenCalledTimes(foldersAtClose)
    expect(fetchMailMessages).toHaveBeenCalledTimes(listAtClose)
  })

  // Spec §7 scenario 2.6: source=live with a null last-validated time and
  // refresh_needed=false renders "Last checked unknown." and NO refresh
  // follows ("(no refresh follows)" is the scenario's own parenthetical;
  // §3.1 issues live ONLY when absent or older-than-five-minutes — a null
  // stamp is neither). Expected RED: the implementation issues the live leg
  // for this input (finding F1).
  it('scenario 2.6 — a live-sourced, null-stamped response shows "Last checked unknown." and no refresh follows (US-2 AS-7)', async () => {
    fetchMailFolders.mockResolvedValue(folderList(FRESH))
    fetchMailMessages.mockResolvedValue(page({
      metadata: { source: 'live', last_validated_at: null, stale: false, refresh_needed: false, notice_code: null, publication_revision: 'rev-live-null' },
    }))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(fetchMailMessages.mock.calls.every((c) => (c[3] as { mode?: string })?.mode !== 'live')).toBe(true)
    expect(screen.getByTestId('mail-freshness-line')).toHaveTextContent('Last checked unknown.')
    expect(screen.getByTestId('mail-freshness-line').textContent).not.toContain('Checking')
  })
})
