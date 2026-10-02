// W3 RED pack — C2 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §4 US-3 (AS-1…AS-7), MC-W3-1, §7 scenarios 3.1–3.6,
// §11 states S-21/S-22/S-23/S-30, D-2. Expected values derived from the spec
// BEFORE the panel was read (receipts/w3-red-derivation.md). The unit under
// test is the real MailPanel; the mocks stand at the network edge.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'

const NOW = new Date('2026-10-02T10:00:00Z')

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

/** A synthetic folder of TOTAL messages, newest first (D-2's ladder). */
const TOTAL = 230
const PAGE = 25

function rowFor(n: number) {
  // Newest first: message #1 is the newest.
  return {
    uid: TOTAL - n + 1,
    uidvalidity: 777,
    message_id: `<m${n}@example.test>`,
    folder: 'inbox',
    subject: `Message ${n}`,
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
  }
}

function pageFor(startRow: number, over: Record<string, unknown> = {}) {
  // startRow is the 1-based index of the page's first (newest) row.
  const count = Math.min(PAGE, TOTAL - startRow + 1)
  const remainingAfter = TOTAL - (startRow + count - 1)
  return {
    messages: Array.from({ length: count }, (_, i) => rowFor(startRow + i)),
    has_more: remainingAfter > 0,
    next_cursor: remainingAfter > 0 ? `cursor-at-${startRow + count - 1}` : null,
    view_limit_reached: false,
    truncated: false,
    next_before_uid: null,
    metadata: {
      source: 'memory',
      last_validated_at: new Date(NOW.getTime() - 40_000).toISOString(),
      stale: false,
      refresh_needed: false,
      notice_code: null,
      publication_revision: `rev-page-${startRow}`,
    },
    ...over,
  }
}

const FOLDERS = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: TOTAL, unread_count: 0, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
  ],
  metadata: {
    source: 'memory',
    last_validated_at: new Date(NOW.getTime() - 40_000).toISOString(),
    stale: false,
    refresh_needed: false,
    notice_code: null,
    publication_revision: 'rev-folders-1',
  },
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
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C2. ' + detail, { cause: err })
  }
}

function mount(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

async function renderPanel(): Promise<ReturnType<typeof mount>> {
  const MailPanel = await loadPanel()
  return mount(<MailPanel workspaceId="ws-1" />)
}

function renderedRowCount(): number {
  return document.querySelectorAll('[data-mail-message-row="true"]').length
}

describe('MailPanel paging and search (C2: scenarios 3.1–3.6, MC-W3-1)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, markMailSeen, mintMailHtmlPreviewToken]) fn.mockReset()
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(FOLDERS)
    fetchMailMessage.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
    mintMailHtmlPreviewToken.mockResolvedValue({ token: 'tok-1' })
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('scenario 3.1 — the first page shows exactly 25 rows with Load more; Load more appends exactly the next page without re-fetching page 1', async () => {
    fetchMailMessages.mockImplementation((_ws: string, _agent: string, _folder: string, params: { cursor?: string }) => {
      if (params.cursor === undefined) return Promise.resolve(pageFor(1))
      if (params.cursor === 'cursor-at-25') return Promise.resolve(pageFor(26))
      return Promise.reject(new Error(`unexpected cursor ${String(params.cursor)}`))
    })
    await renderPanel()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(renderedRowCount()).toBe(25)
    expect(screen.getByText('Message 1')).toBeInTheDocument() // newest first
    const loadMore = screen.getByRole('button', { name: 'Load more' })
    // The open event's cache-first read was the ONLY page-1 request so far.
    expect(fetchMailMessages).toHaveBeenCalledTimes(1)
    expect(fetchMailMessages.mock.calls[0][3]).toMatchObject({ limit: 25 })
    fireEvent.click(loadMore)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // Exactly ONE additional request, carrying page 2's cursor and limit=25.
    expect(fetchMailMessages).toHaveBeenCalledTimes(2)
    expect(fetchMailMessages.mock.calls[1][3]).toMatchObject({ limit: 25, cursor: 'cursor-at-25' })
    // 50 rows total, prior rows preserved in stable newest-first order.
    expect(renderedRowCount()).toBe(50)
    const listRows = document.querySelectorAll('[data-mail-message-row="true"]')
    expect(within(listRows[0] as HTMLElement).getByText('Message 1')).toBeInTheDocument()
    expect(within(listRows[25] as HTMLElement).getByText('Message 26')).toBeInTheDocument()
  })

  it('MC-W3-1 — the ladder tops out at 200 rows; the control is gone and no further browse request fires', async () => {
    fetchMailMessages.mockImplementation((_ws: string, _agent: string, _folder: string, params: { cursor?: string }) => {
      if (params.cursor === undefined) return Promise.resolve(pageFor(1))
      const at = Number(String(params.cursor).replace('cursor-at-', ''))
      // The 7th load reaches 200 displayed rows: the server marks the view limit.
      const nextStart = at + 1
      const isCeiling = nextStart + PAGE - 1 >= 200
      return Promise.resolve(pageFor(nextStart, isCeiling
        ? { view_limit_reached: true, has_more: false, next_cursor: null }
        : {}))
    })
    await renderPanel()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(renderedRowCount()).toBe(25)
    // Six Load-more activations: 25 → 50 → 75 → 100 → 125 → 150 → 175.
    for (let i = 0; i < 6; i++) {
      fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    }
    expect(renderedRowCount()).toBe(175)
    expect(screen.getByRole('button', { name: 'Load more' })).toBeInTheDocument()
    // The seventh load reaches the 200-row ceiling…
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(renderedRowCount()).toBe(200)
    // …and the 8th activation is impossible: the control is GONE (MC-W3-1).
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
    expect(screen.getByTestId('mail-ceiling-message')).toHaveTextContent(
      "You're viewing the newest 200 messages. Search to find older ones.",
    )
    const callsAtCeiling = fetchMailMessages.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
    expect(fetchMailMessages).toHaveBeenCalledTimes(callsAtCeiling) // no further browse request
  })

  it('scenario 3.3 — search runs live folder-scoped with its own Load more; "Back to INBOX" restores the loaded browse rows', async () => {
    const searchRow = { ...rowFor(301), subject: 'Ancient invoice' }
    fetchMailMessages.mockImplementation((_ws: string, _agent: string, _folder: string, params: { search?: string; cursor?: string }) => {
      if (params.search === 'ancient') {
        if (params.cursor === undefined) {
          return Promise.resolve({
            messages: [searchRow],
            has_more: false,
            next_cursor: null,
            view_limit_reached: false,
            metadata: { source: 'live', last_validated_at: NOW.toISOString(), stale: false, refresh_needed: false, notice_code: null, publication_revision: 'rev-search-1' },
          })
        }
        return Promise.reject(new Error('unexpected search cursor'))
      }
      if (params.cursor === undefined) return Promise.resolve(pageFor(1))
      if (params.cursor === 'cursor-at-25') return Promise.resolve(pageFor(26))
      return Promise.reject(new Error(`unexpected cursor ${String(params.cursor)}`))
    })
    await renderPanel()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(renderedRowCount()).toBe(50)
    fireEvent.change(screen.getByLabelText('Search inbox'), { target: { value: 'ancient' } })
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The search request ran live: the search param, never a cache mode.
    const searchCall = fetchMailMessages.mock.calls.find((c) => (c[3] as { search?: string }).search === 'ancient')
    expect(searchCall).toBeDefined()
    expect((searchCall![3] as { mode?: string }).mode).not.toBe('cache_first')
    expect(searchCall![3]).toMatchObject({ limit: 25, search: 'ancient' })
    expect(screen.getByText('Ancient invoice')).toBeInTheDocument()
    expect(screen.getByTestId('mail-search-exit')).toHaveTextContent('Back to INBOX')
    fireEvent.click(screen.getByTestId('mail-search-exit'))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The browse view is restored with its loaded rows intact (scenario 3.3).
    expect(renderedRowCount()).toBe(50)
    expect(screen.getByText('Message 1')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Load more' })).toBeInTheDocument()
  })

  it('scenario 3.4 / S-22 — an empty search reads exactly "No messages match \\"<query>\\"." with the browse view one control away', async () => {
    fetchMailMessages.mockImplementation((_ws: string, _agent: string, _folder: string, params: { search?: string; cursor?: string }) => {
      if (params.search === 'zzz-nothing') {
        return Promise.resolve({
          messages: [],
          has_more: false,
          next_cursor: null,
          view_limit_reached: false,
          metadata: { source: 'live', last_validated_at: NOW.toISOString(), stale: false, refresh_needed: false, notice_code: null, publication_revision: 'rev-search-1' },
        })
      }
      if (params.cursor === undefined) return Promise.resolve(pageFor(1))
      return Promise.reject(new Error('unexpected cursor'))
    })
    await renderPanel()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.change(screen.getByLabelText('Search inbox'), { target: { value: 'zzz-nothing' } })
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('mail-search-empty')).toHaveTextContent('No messages match "zzz-nothing".')
    // The browse view remains one control away.
    expect(screen.getByTestId('mail-search-exit')).toBeInTheDocument()
  })

  it('S-30 — the search ceiling copy is NOT the browse-ceiling copy (R2-M2: the two states are distinct)', async () => {
    const searchRow = rowFor(301)
    fetchMailMessages.mockImplementation((_ws: string, _agent: string, _folder: string, params: { search?: string; cursor?: string }) => {
      if (params.search === 'bulk') {
        if (params.cursor === undefined) {
          return Promise.resolve({
            messages: Array.from({ length: 25 }, (_, i) => ({ ...searchRow, uid: 900 - i, subject: `Bulk ${i + 1}` })),
            has_more: true,
            next_cursor: 'search-cursor-25',
            view_limit_reached: false,
            metadata: { source: 'live', last_validated_at: NOW.toISOString(), stale: false, refresh_needed: false, notice_code: null, publication_revision: 'rev-search-1' },
          })
        }
        return Promise.resolve({
          messages: Array.from({ length: 25 }, (_, i) => ({ ...searchRow, uid: 870 - i, subject: `Bulk ${26 + i}` })),
          has_more: false,
          next_cursor: null,
          view_limit_reached: true,
          metadata: { source: 'live', last_validated_at: NOW.toISOString(), stale: false, refresh_needed: false, notice_code: null, publication_revision: 'rev-search-2' },
        })
      }
      if (params.cursor === undefined) return Promise.resolve(pageFor(1))
      return Promise.reject(new Error('unexpected cursor'))
    })
    await renderPanel()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.change(screen.getByLabelText('Search inbox'), { target: { value: 'bulk' } })
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('mail-search-ceiling')).toHaveTextContent(
      "You're viewing the newest 200 matches. Refine your search to find older messages.",
    )
    expect(screen.queryByTestId('mail-ceiling-message')).not.toBeInTheDocument()
    expect(screen.queryByText(/newest 200 messages/)).not.toBeInTheDocument()
  })

  it('scenario 3.5 — a typed 409 on Load more resets to the first page with the S-23 notice and no cursor replay', async () => {
    fetchMailMessages.mockImplementation((_ws: string, _agent: string, _folder: string, params: { cursor?: string; mode?: string }) => {
      if (params.cursor === undefined) return Promise.resolve(pageFor(1))
      if (params.cursor === 'cursor-at-25') return Promise.reject(new ApiError(409, 'stale cursor', { body: JSON.stringify({ code: 'stale_cursor' }) }))
      return Promise.reject(new Error('unexpected cursor'))
    })
    await renderPanel()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(renderedRowCount()).toBe(25)
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The visible notice, shown once (S-23).
    expect(screen.getAllByText('The folder changed. Showing the newest messages.')).toHaveLength(1)
    // The view reset to the folder's first page.
    expect(renderedRowCount()).toBe(25)
    // The reset re-read ran cache-first WITHOUT the stale cursor, and nothing
    // replays it: three calls total (open, load-more, reset), the reset
    // carrying no cursor and no mode=live.
    expect(fetchMailMessages).toHaveBeenCalledTimes(3)
    const resetParams = fetchMailMessages.mock.calls[2][3] as { cursor?: string; mode?: string }
    expect(resetParams.cursor).toBeUndefined()
    expect(resetParams.mode).not.toBe('live')
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
    expect(fetchMailMessages).toHaveBeenCalledTimes(3) // no silent retry
  })
})
