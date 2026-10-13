// W3 RED pack — C7 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §4 US-10 (AS-1…AS-3), MC-W3-9, §11 state S-9,
// §7 scenarios 10.1–10.3, FR-W3-19/FR-W3-20, A-6. Expected values derived
// from the spec BEFORE the panel was read (receipts/w3-red-derivation.md).
// The panel runs REAL against the mocked API edge; the MC-W3-9 request-
// builder leg drives the REAL mail client (vi.importActual) over a stubbed
// global fetch so the wire shape itself is asserted, not a mock's memory.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'

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
  last_validated_at: new Date(Date.now() - 40_000).toISOString(),
  stale: false,
  refresh_needed: false,
  notice_code: null,
  publication_revision: 'rev-1',
}

const FOLDERS = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 2, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
  ],
  metadata: FRESH_META,
}

const PAGE = {
  messages: [], has_more: false, next_cursor: null, view_limit_reached: false,
  truncated: false, next_before_uid: null, metadata: FRESH_META,
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
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C7. ' + detail, { cause: err })
  }
}

// Real timers throughout this file: findBy*/waitFor drive their own polling
// and every promise settles naturally (fake timers left a rejected query
// unsettled inside act).

async function renderOpenPanel(): Promise<void> {
  const MailPanel = await loadPanel()
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MailPanel workspaceId="ws-1" />
    </QueryClientProvider>,
  )
  expect(await screen.findByRole('tab', { name: 'INBOX' })).toBeInTheDocument()
}

describe('MailPanel Refresh and Retry (C7: scenarios 10.1–10.3, MC-W3-9)', () => {
  beforeEach(() => {
    for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary]) fn.mockReset()
    fetchAgents.mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(FOLDERS)
    fetchMailMessages.mockResolvedValue(PAGE)
    fetchMailMessage.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
  })

  afterEach(() => {
    cleanup()
  })

  it('scenario 10.1 — Refresh sends one folder-list request with mode=live AND refresh_mapping=true, plus exactly one live list refresh', async () => {
    await renderOpenPanel()
    const foldersBefore = fetchMailFolders.mock.calls.length
    const listBefore = fetchMailMessages.mock.calls.length
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(fetchMailFolders.mock.calls.length).toBeGreaterThan(foldersBefore))
    await waitFor(() => expect(fetchMailMessages.mock.calls.length).toBeGreaterThan(listBefore))
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'INBOX' })).toBeInTheDocument()
    })
    // Exactly ONE folder-list request was added, carrying both markers.
    const newFolderCalls = fetchMailFolders.mock.calls.slice(foldersBefore)
    expect(newFolderCalls).toHaveLength(1)
    expect(newFolderCalls[0][2]).toMatchObject({ mode: 'live', refresh_mapping: true })
    // The CURRENT folder got exactly its one live list refresh — no other
    // folder was contacted (there are no other per-folder requests at all).
    const newListCalls = fetchMailMessages.mock.calls.slice(listBefore)
    expect(newListCalls).toHaveLength(1)
    expect(newListCalls[0][3]).toMatchObject({ mode: 'live' })
  })

  it('scenario 10.2 — Retry is human-only: the failed surface’s next fetch carries retry=true; automatic paths never do (MC-W3-9)', async () => {
    fetchMailFolders.mockRejectedValueOnce(new Error('connect refused'))
    const MailPanel = await loadPanel()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MailPanel workspaceId="ws-1" />
      </QueryClientProvider>,
    )
    // The folders fetch failed on the open event: the S-10-style surface with
    // its Retry is the test's subject.
    const retry = await screen.findByRole('button', { name: 'Retry' })
    fireEvent.click(retry)
    await waitFor(() => expect(fetchMailFolders.mock.calls.length).toBe(2))
    await waitFor(() => expect(screen.getByRole('tab', { name: 'INBOX' })).toBeInTheDocument())
    const retryCall = fetchMailFolders.mock.calls[1]
    expect((retryCall[2] as { retry?: boolean }).retry).toBe(true)
    // Every AUTOMATIC call (the initial open) carries no retry marker — only
    // the human Retry did (MC-W3-9).
    const opts = fetchMailFolders.mock.calls[0][2] as { retry?: boolean }
    expect(opts.retry === true).toBe(false)
  })

  it('scenario 10.3 — the 503 reason surfaces its specific copy; a generic busy shows the generic copy (S-9)', async () => {
    // Scenario 10.3's Given is "a 503 … when the failure renders" — no
    // surface is scoped out. The FOLDER-LIST failure is the panel's most
    // prominent mail failure surface, so its 503 must carry the S-9 copy.
    fetchMailFolders.mockRejectedValue(new ApiError(503, 'Mail is busy', {
      body: JSON.stringify({ code: 'busy', reason: 'server_connection_limit' }),
    }))
    const MailPanel = await loadPanel()
    const { unmount } = render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MailPanel workspaceId="ws-1" />
      </QueryClientProvider>,
    )
    expect(await screen.findByText('The mail server reached its connection limit. Try again shortly.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    unmount()
    cleanup()
    // A generic busy (or an unpopulated reason) keeps the generic copy.
    fetchMailFolders.mockRejectedValue(new ApiError(503, 'Mail is busy', {
      body: JSON.stringify({ code: 'busy', reason: 'pool_busy' }),
    }))
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MailPanel workspaceId="ws-1" />
      </QueryClientProvider>,
    )
    expect(await screen.findByText('Mail is busy. Try again.')).toBeInTheDocument()
  })

  it('scenario 10.3 on the list surface — a 503 list failure renders the reason-specific copy with Retry', async () => {
    fetchMailMessages.mockRejectedValue(new ApiError(503, 'Mail is busy', {
      body: JSON.stringify({ code: 'busy', reason: 'server_connection_limit' }),
    }))
    await renderOpenPanel()
    expect(await screen.findByText('The mail server reached its connection limit. Try again shortly.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('MC-W3-9 (request-builder form) — the REAL client sets retry=true only when asked; Refresh’s markers serialize exactly', async () => {
    const urls: string[] = []
    // A schema-valid MailFolderList — the real client Zod-validates every
    // response, so the stub must satisfy the contract the wire declares.
    const validFolders = {
      folders: [
        { slug: 'inbox', display_name: 'INBOX', total: 2, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
        { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
        { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      ],
    }
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      urls.push(String(input))
      return new Response(JSON.stringify(validFolders), { status: 200, headers: { 'content-type': 'application/json' } })
    }))
    try {
      const actual = await vi.importActual<typeof import('@/lib/api/mail')>('@/lib/api/mail')
      await actual.fetchMailFolders('ws-1', 'mia') // automatic path
      await actual.fetchMailFolders('ws-1', 'mia', { retry: true }) // human Retry
      await actual.fetchMailFolders('ws-1', 'mia', { mode: 'live', refresh_mapping: true }) // manual Refresh
      expect(urls).toHaveLength(3)
      expect(urls[0]).not.toContain('retry=true')
      expect(urls[1]).toContain('retry=true')
      expect(urls[2]).toContain('mode=live')
      expect(urls[2]).toContain('refresh_mapping=true')
      expect(urls[2]).not.toContain('retry=true')
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
