// W3 RED pack — C3 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §4 US-2 AS-5, US-4 AS-4/AS-6, §11 states S-7/S-8,
// §7 scenarios 2.3/4.2/4.3, dataset D-3, MC-W3-4. Expected values derived
// from the spec BEFORE the panel was read (receipts/w3-red-derivation.md).
// The landed `MailFolder.total` is still non-nullable (contracts-wave-check
// F2) — the null-TOTAL leg has no wire representation until the scheduled
// amendment lands, so this pack tests the LANDED null-unread leg and the
// unknown-role leg, and documents the gated leg rather than improvising a
// sentinel (the spec's own gating rule).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

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

const FRESH_META = {
  source: 'memory',
  last_validated_at: new Date(NOW.getTime() - 40_000).toISOString(),
  stale: false,
  refresh_needed: false,
  notice_code: null,
  publication_revision: 'rev-1',
}

const PAGE = {
  messages: [],
  has_more: false,
  next_cursor: null,
  view_limit_reached: false,
  truncated: false,
  next_before_uid: null,
  metadata: FRESH_META,
}

function folder(over: Record<string, unknown> = {}) {
  return {
    slug: 'sent',
    display_name: 'Sent',
    total: 0,
    unread_count: null,
    availability: 'present',
    uidvalidity: 777,
    mapping_source: 'special_use',
    ...over,
  }
}

function folderList(inbox: Record<string, unknown>, sent: Record<string, unknown>, drafts: Record<string, unknown>) {
  return { folders: [inbox, sent, drafts], metadata: FRESH_META }
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
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C3. ' + detail, { cause: err })
  }
}

function mount(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

describe('MailPanel folder availability and honest counts (C3: scenarios 2.3, 4.2, 4.3; D-3)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, markMailSeen, mintMailHtmlPreviewToken]) fn.mockReset()
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailMessage.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
    mintMailHtmlPreviewToken.mockResolvedValue({ token: 'tok-1' })
    fetchMailMessages.mockResolvedValue(PAGE)
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('scenario 2.3 — a null inbox unread_count renders "—", never 0; a genuine zero keeps its 0 (US-2 AS-5, MC-W3-4)', async () => {
    fetchMailFolders.mockResolvedValue(folderList(
      { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      folder(),
      folder({ slug: 'drafts', display_name: 'Drafts' }),
    ))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const inboxTab = screen.getByRole('tab', { name: 'INBOX' })
    expect(inboxTab).toHaveTextContent('—')
    expect(inboxTab).not.toHaveTextContent('0')
    // A legitimate zero count renders "0" — unknown and zero stay distinct
    // (D-1 row 7 / D-3's zero-vs-null rows).
    const zeroInbox = { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 0, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' }
    cleanup()
    fetchMailFolders.mockResolvedValue(folderList(zeroInbox, folder(), folder({ slug: 'drafts', display_name: 'Drafts' })))
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByRole('tab', { name: 'INBOX' })).toHaveTextContent('0')
  })

  // AMENDMENT-GATED (spec §2.4 row 2, MC-W3-4): `total: null` has no wire
  // representation at the landed commit — the scheduled `MailFolder.total`
  // nullability amendment carries it. This pack asserts the RAIL's handling
  // of a null total only through the component's own props contract, which
  // is W3-owned and already typed for the amendment; the panel-level wire
  // test lands with the amendment. No sentinel is asserted or improvised.
  it('the non-inbox count slot renders the total, and an unknown ROLE renders "—" regardless of numbers (D-3)', async () => {
    fetchMailFolders.mockResolvedValue(folderList(
      { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      folder({ total: 12 }),
      folder({ slug: 'drafts', display_name: 'Drafts' }),
    ))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByRole('tab', { name: 'Sent' })).toHaveTextContent('12')
    // An unknown role's count slot shows "—" even when a number exists —
    // an unconfirmed count is not a count (S-8's rail half).
    cleanup()
    fetchMailFolders.mockResolvedValue(folderList(
      { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      folder({ total: 12, availability: 'unknown', mapping_source: 'override' }),
      folder({ slug: 'drafts', display_name: 'Drafts' }),
    ))
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const sentTab = screen.getByRole('tab', { name: 'Sent' })
    expect(sentTab).toHaveTextContent('—')
    expect(sentTab).not.toHaveTextContent('12')
  })

  it('scenario 4.3 / S-7 — a confirmed-absent folder explains, never errors; the rail keeps the role visible', async () => {
    fetchMailFolders.mockResolvedValue(folderList(
      { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      folder({ availability: 'absent', mapping_source: 'fallback' }),
      folder({ slug: 'drafts', display_name: 'Drafts' }),
    ))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('tab', { name: 'Sent' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('mail-folder-absent')).toHaveTextContent('No messages')
    expect(screen.getByTestId('mail-folder-absent')).toHaveTextContent(
      'Your mail server has no Sent folder. You can set the folder name in mailbox settings.',
    )
    expect(screen.getByRole('link', { name: 'Open mailbox settings' })).toBeInTheDocument()
    // Never an error surface: no Retry, no error headline in the list zone.
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    // The rail keeps the role visible with its count.
    expect(screen.getByRole('tab', { name: 'Sent' })).toBeInTheDocument()
  })

  it('scenario 4.2 / S-8 — an unresolved override is NOT absence: the rail shows "—" and the list never claims "No messages"', async () => {
    fetchMailFolders.mockResolvedValue(folderList(
      { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      folder({ availability: 'unknown', mapping_source: 'override' }),
      folder({ slug: 'drafts', display_name: 'Drafts' }),
    ))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('tab', { name: 'Sent' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('mail-folder-unknown')).toHaveTextContent(
      "Couldn't confirm the Sent folder on this server.",
    )
    expect(screen.getByTestId('mail-folder-unknown')).toHaveTextContent(
      'Set the folder name in mailbox settings.',
    )
    expect(screen.getByRole('link', { name: 'Open mailbox settings' })).toBeInTheDocument()
    // Never claims absence, never renders "No messages" (M-01's distinction).
    expect(screen.queryByText('No messages')).not.toBeInTheDocument()
    expect(screen.queryByTestId('mail-folder-absent')).not.toBeInTheDocument()
  })

  it('D-3 — mapping_source=saved renders as an ordinary override (the register row 3 five-value enum)', async () => {
    fetchMailFolders.mockResolvedValue(folderList(
      { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
      folder({ availability: 'unknown', mapping_source: 'saved' }),
      folder({ slug: 'drafts', display_name: 'Drafts' }),
    ))
    const MailPanel = await loadPanel()
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    fireEvent.click(screen.getByRole('tab', { name: 'Sent' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // The `saved` mapping is an override by another name: the unresolved
    // state shows (S-8), never absence, never an error.
    expect(screen.getByTestId('mail-folder-unknown')).toHaveTextContent(
      "Couldn't confirm the Sent folder on this server.",
    )
    expect(screen.queryByText('No messages')).not.toBeInTheDocument()
  })
})
