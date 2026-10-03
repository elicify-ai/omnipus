// W3 RED pack — C4 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §4 US-6 AS-1/AS-2/AS-6, §7 scenarios 6.1/6.2/6.4,
// §11 states S-13/S-25, dataset D-4, counterexample mutation 10. Expected
// values derived from the spec BEFORE the panel was read
// (receipts/w3-red-derivation.md). The panel runs REAL; mocks sit at the
// network edge only.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const NOW = new Date('2026-10-02T10:00:00Z')
const MIB = 1024 * 1024

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

function summary(n: number, over: Record<string, unknown> = {}) {
  return {
    uid: n,
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
    ...over,
  }
}

const DETAIL = {
  uid: 42,
  uidvalidity: 777,
  message_id: '<quarterly@example.test>',
  folder: 'inbox',
  subject: 'Quarterly report',
  from: 'ada@example.test',
  from_name: 'Ada',
  to: ['mia@example.test'],
  cc: [],
  bcc: [],
  date: '2026-09-28T10:00:00Z',
  seen: true,
  is_draft: false,
  is_omnipus_draft: false,
  read_by_agent: false,
  has_html: false,
  body_markdown: 'See attached.',
  body_text: 'See attached.',
  has_attachments: true,
  message_ref: 'uid:777:42',
  attachments: [
    { part_index: 1, filename: 'report.pdf', content_type: 'application/pdf', size_bytes: 2202009 },
    { part_index: 2, filename: 'data.bin', content_type: 'application/octet-stream', size_bytes: null },
  ],
}

const FOLDERS = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 5, unread_count: 1, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
  ],
  metadata: FRESH_META,
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
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C4. ' + detail, { cause: err })
  }
}

function mount(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

async function openMessageWith(attachments: Array<{ part_index: number; filename: string; content_type: string; size_bytes: number | null }>): Promise<void> {
  const MailPanel = await loadPanel()
  fetchMailMessages.mockResolvedValue({
    messages: [summary(42, { subject: 'Quarterly report', has_attachments: true })],
    has_more: false, next_cursor: null, view_limit_reached: false, truncated: false, next_before_uid: null,
    metadata: FRESH_META,
  })
  fetchMailMessage.mockResolvedValue({ ...DETAIL, attachments })
  mount(<MailPanel workspaceId="ws-1" />)
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  fireEvent.click(screen.getByRole('button', { name: /Quarterly report/ }))
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(screen.getByLabelText('Attachments')).toBeInTheDocument()
}

describe('MailPanel attachment rows (C4: scenarios 6.1, 6.2, 6.4; D-4)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, markMailSeen, mintMailHtmlPreviewToken]) fn.mockReset()
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(FOLDERS)
    fetchMailMessage.mockResolvedValue(DETAIL)
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
    mintMailHtmlPreviewToken.mockResolvedValue({ token: 'tok-1' })
  })

  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('scenario 6.1 — the paperclip shows ONLY on has_attachments=true rows, with the accessible text "Has attachments"', async () => {
    const MailPanel = await loadPanel()
    fetchMailMessages.mockResolvedValue({
      messages: [
        summary(1, { has_attachments: false }),
        summary(2, { has_attachments: true }),
        summary(3, { has_attachments: false }),
        summary(4, { has_attachments: false }),
        summary(5, { has_attachments: true }),
      ],
      has_more: false, next_cursor: null, view_limit_reached: false, truncated: false, next_before_uid: null,
      metadata: FRESH_META,
    })
    mount(<MailPanel workspaceId="ws-1" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const indicators = screen.getAllByLabelText('Has attachments')
    expect(indicators).toHaveLength(2)
    // The indicator rides exactly the flagged rows (2 and 5), driven by the
    // generated flag — never by an attachments array (counterexample 10).
    const flaggedRows = indicators.map((el) => el.closest('[data-mail-message-row]'))
    for (const row of flaggedRows) {
      expect(within(row as HTMLElement).getByText(/Message (2|5)/)).toBeInTheDocument()
    }
    expect(within(screen.getByRole('button', { name: /Message 3/ })).queryByLabelText('Has attachments')).not.toBeInTheDocument()
  })

  it('scenario 6.2 — attachment rows name filename, size, and three distinct actions (S-25 for unknown size)', async () => {
    await openMessageWith(DETAIL.attachments)
    // Filename + size; a null size renders "Size unknown", never "0 B" (S-25).
    expect(screen.getByText('report.pdf')).toBeInTheDocument()
    expect(screen.getByText('2.1 MB')).toBeInTheDocument()
    expect(screen.getByText('data.bin')).toBeInTheDocument()
    expect(screen.getByText('Size unknown')).toBeInTheDocument()
    expect(screen.queryByText('0 B')).not.toBeInTheDocument()
    // Three actions per row, each with the pinned accessible name.
    expect(screen.getByRole('button', { name: 'Open report.pdf attachment' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save report.pdf to Library' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download report.pdf' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open data.bin attachment' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save data.bin to Library' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download data.bin' })).toBeInTheDocument()
  })

  it('scenario 6.4 / S-13 — an over-cap attachment disables Open and Save with the pinned explanation; Download stays live', async () => {
    await openMessageWith([
      { part_index: 1, filename: 'big.mov', content_type: 'video/quicktime', size_bytes: 25 * MIB + 1 },
    ])
    const open = screen.getByRole('button', { name: 'Open big.mov attachment' })
    const save = screen.getByRole('button', { name: 'Save big.mov to Library' })
    expect(open).toBeDisabled()
    expect(save).toBeDisabled()
    // The explanation is visible in place and associated with the controls —
    // not a tooltip-only hint (S-13's "associated text" requirement).
    expect(screen.getByTestId('mail-attachment-overcap')).toHaveTextContent(
      'This attachment is larger than the 25 MB preview limit. Use Download.',
    )
    expect(open).toHaveAttribute('aria-describedby', expect.stringContaining('mail-attachment-overcap'))
    const download = screen.getByRole('button', { name: 'Download big.mov' })
    expect(download).toBeEnabled()
  })

  it('D-4 boundary — an attachment at exactly 25 MiB is NOT over the cap (only "over the 25 MB cap" is refused)', async () => {
    await openMessageWith([
      { part_index: 1, filename: 'exact.pdf', content_type: 'application/pdf', size_bytes: 25 * MIB },
    ])
    expect(screen.getByRole('button', { name: 'Open exact.pdf attachment' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Save exact.pdf to Library' })).toBeEnabled()
    expect(screen.queryByTestId('mail-attachment-overcap')).not.toBeInTheDocument()
  })
})
