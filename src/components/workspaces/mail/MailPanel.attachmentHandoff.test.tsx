// W3 RED pack — C5 (spec §8.4; file name per spec §8.4's table).
//
// Oracle source: spec §4 US-6 AS-3/AS-5/AS-7/AS-8, §4 US-7 (AS-1…AS-5),
// §11 states S-15/S-16/S-17/S-18/S-19/S-20/S-26/S-27/S-28/S-29, MC-W3-6,
// MC-W3-8, §7 scenarios 6.3/6.5/6.6/6.7/7.1–7.5, FR-W3-22/FR-W3-23, D-7.
// Expected values derived from the spec BEFORE the panel and handoff module
// were read (receipts/w3-red-derivation.md). Real units: MailPanel and the
// whole mailAttachmentHandoff module (mount seam, save controller,
// announcer, focus resolver). Mocks sit at the network edge only
// (@/lib/api/mail's mint/revoke/save); the W8 Library-side mount is a test
// stub standing in for the consumer of W3's published seam — EXCEPT the
// scenario 7.5 journey, which registers no handler and drives the REAL
// fallback host (MailAttachmentViewer) the panel wires.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor, within } from '@testing-library/react'
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
  mintMailAttachmentPreview,
  revokeMailAttachmentPreview,
  saveMailAttachmentToLibrary,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
  markMailSeen: vi.fn(),
  mintMailAttachmentPreview: vi.fn(),
  revokeMailAttachmentPreview: vi.fn(),
  saveMailAttachmentToLibrary: vi.fn(),
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
    mintMailAttachmentPreview,
    revokeMailAttachmentPreview,
    saveMailAttachmentToLibrary,
  }
})

import {
  hasMailAttachmentMountHandler,
  setMailAttachmentMountHandler,
  type MailAttachmentHandoff,
  type MailHandoffControls,
} from './mailAttachmentHandoff'

const FRESH_META = {
  source: 'memory',
  last_validated_at: new Date(NOW.getTime() - 40_000).toISOString(),
  stale: false,
  refresh_needed: false,
  notice_code: null,
  publication_revision: 'rev-1',
}

const MINT_RESPONSE = {
  kind: 'mail_attachment' as const,
  preview_id: 'pvw-1',
  subject: 'Quarterly report',
  attachment: { part_index: 1, filename: 'report.pdf', content_type: 'application/pdf', size_bytes: 2202009 },
  text_readable: true,
  content_source: { byte_url: '/mail-preview/part/pvw-1', token: 'tok-pvw-1' },
  read_only: true as const,
}

const SAVE_RESPONSE = {
  saved: true as const,
  entry: { name: 'report.pdf', path: 'mail/mia@example.test/2026-09/report.pdf' },
  path: 'mail/mia@example.test/2026-09/report.pdf',
  absolute_path: '/data/library/mail/mia@example.test/2026-09/report.pdf',
  size_bytes: 2202009,
  audit_status: 'recorded' as const,
  warning_code: null,
}

function summary() {
  return {
    uid: 42, uidvalidity: 777, message_id: '<quarterly@example.test>', folder: 'inbox',
    subject: 'Quarterly report', from: 'ada@example.test', from_name: 'Ada',
    to: ['mia@example.test'], cc: [], date: '2026-09-28T10:00:00Z', seen: true,
    is_draft: false, is_omnipus_draft: false, read_by_agent: false,
    has_attachments: true, message_ref: 'uid:777:42',
  }
}

const DETAIL = {
  uid: 42, uidvalidity: 777, message_id: '<quarterly@example.test>', folder: 'inbox',
  subject: 'Quarterly report', from: 'ada@example.test', from_name: 'Ada',
  to: ['mia@example.test'], cc: [], bcc: [], date: '2026-09-28T10:00:00Z',
  seen: true, is_draft: false, is_omnipus_draft: false, read_by_agent: false,
  has_html: false, body_markdown: 'See attached.', body_text: 'See attached.',
  has_attachments: true, message_ref: 'uid:777:42',
  attachments: [{ part_index: 1, filename: 'report.pdf', content_type: 'application/pdf', size_bytes: 2202009 }],
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
    throw new Error('BLOCKED: MailPanel not implemented — required by W3 spec §8.4 C5. ' + detail, { cause: err })
  }
}

function mount(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

interface MountedHandoff { handoff: MailAttachmentHandoff | null; controls: MailHandoffControls | null }

/** Mount the panel with the message open and a RECORDING mount seam — the
 * tests click Open themselves, so failed-Open flows can assert the seam was
 * never handed a descriptor. */
async function openMessagePane(): Promise<{ mounted: MountedHandoff }> {
  const MailPanel = await loadPanel()
  fetchMailMessages.mockResolvedValue({
    messages: [summary()],
    has_more: false, next_cursor: null, view_limit_reached: false, truncated: false, next_before_uid: null,
    metadata: FRESH_META,
  })
  fetchMailMessage.mockResolvedValue(DETAIL)
  const mounted: MountedHandoff = { handoff: null, controls: null }
  setMailAttachmentMountHandler((handoff, controls) => {
    mounted.handoff = handoff
    mounted.controls = controls
  })
  mount(<MailPanel workspaceId="ws-1" />)
  // Real timers: findBy* drives waitFor's own polling across React Query's
  // folders→list enable cascade.
  fireEvent.click(await screen.findByRole('button', { name: /Quarterly report/ }))
  await waitFor(() => expect(screen.getByLabelText('Attachments')).toBeInTheDocument())
  return { mounted }
}

async function openReportPdf(): Promise<{ mounted: MountedHandoff }> {
  const { mounted } = await openMessagePane()
  fireEvent.click(screen.getByRole('button', { name: 'Open report.pdf attachment' }))
  await waitFor(() => expect(mounted.handoff).not.toBeNull())
  return { mounted }
}

function announcerText(): string {
  const region = document.querySelector('[data-testid="mail-handoff-announcer"]')
  return region?.textContent ?? ''
}

beforeEach(() => {
  for (const fn of [fetchAgents, fetchMailboxes, fetchMailFolders, fetchMailMessages, fetchMailMessage, fetchMailSummary, markMailSeen, mintMailAttachmentPreview, revokeMailAttachmentPreview, saveMailAttachmentToLibrary]) fn.mockReset()
  fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
  fetchMailboxes.mockResolvedValue([
    { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
  ])
  fetchMailFolders.mockResolvedValue(FOLDERS)
  fetchMailSummary.mockResolvedValue({ items: [] })
  markMailSeen.mockResolvedValue(undefined)
  mintMailAttachmentPreview.mockResolvedValue(MINT_RESPONSE)
  revokeMailAttachmentPreview.mockResolvedValue(undefined)
  saveMailAttachmentToLibrary.mockResolvedValue(SAVE_RESPONSE)
})

afterEach(() => {
  setMailAttachmentMountHandler(null)
  cleanup()
  // The panel persists its folder/message intent in sessionStorage — clear it
  // so every test starts from a clean navigation state and no test depends
  // on the order it ran in.
  sessionStorage.clear()
})

describe('MailPanel attachment handoff — Open (C5: scenarios 6.3, 7.1–7.2; MC-W3-6/8)', () => {
  it('scenario 6.3 / MC-W3-6 — Open mints and hands ONLY the generated descriptor; no path, no LibraryEntry', async () => {
    const { mounted } = await openReportPdf()
    const handoff = mounted.handoff!
    // The mint request is the generated request shape, assembled from the
    // read result's message_ref — never constructed client-side.
    expect(mintMailAttachmentPreview).toHaveBeenCalledTimes(1)
    const mintArgs = mintMailAttachmentPreview.mock.calls[0][0] as Record<string, unknown>
    expect(mintArgs).toMatchObject({
      workspace_id: 'ws-1',
      agent_id: 'mia',
      folder: 'inbox',
      message_ref: 'uid:777:42',
      part_index: 1,
    })
    // The handoff payload is the generated response plus the context —
    // nothing else: no workspace path, no LibraryEntry (spec §6's prohibition).
    expect(handoff.source).toMatchObject({ kind: 'mail_attachment', preview_id: 'pvw-1', read_only: true })
    expect(Object.keys(handoff.source)).not.toContain('path')
    expect(Object.keys(handoff.source)).not.toContain('entry')
    expect(handoff.context.subject).toBe('Quarterly report')
    expect(handoff.context.returnFocus).toEqual({
      kind: 'attachment-action',
      id: 'inbox:uid:777:42:1:open',
    })
  })

  it('scenario 7.1 / S-18 — Open announces "Opening <filename> from mail." without stealing focus', async () => {
    const { mounted } = await openMessagePane()
    const openButton = screen.getByRole('button', { name: 'Open report.pdf attachment' })
    openButton.focus() // jsdom does not focus on click; pin the pre-click focus.
    fireEvent.click(openButton)
    await waitFor(() => expect(mounted.handoff).not.toBeNull())
    await waitFor(() => expect(announcerText()).toBe('Opening report.pdf from mail.'))
    // Mail's own surface never moves focus into a partial preview — the
    // focus-on-heading leg belongs to the W8 viewer's context bar.
    expect(document.activeElement).toBe(openButton)
  })

  it('S-29 — the in-flight Open disables the control reading "Opening <filename>…" with aria-busy; no second mint starts', async () => {
    const MailPanel = await loadPanel()
    fetchMailMessages.mockResolvedValue({
      messages: [summary()],
      has_more: false, next_cursor: null, view_limit_reached: false, truncated: false, next_before_uid: null,
      metadata: FRESH_META,
    })
    fetchMailMessage.mockResolvedValue(DETAIL)
    setMailAttachmentMountHandler(() => undefined)
    let releaseMint: (value: unknown) => void = () => undefined
    mintMailAttachmentPreview.mockImplementation(() => new Promise((resolve) => { releaseMint = resolve }))
    mount(<MailPanel workspaceId="ws-1" />)
    fireEvent.click(await screen.findByRole('button', { name: /Quarterly report/ }))
    await waitFor(() => expect(screen.getByLabelText('Attachments')).toBeInTheDocument())
    // The stable handle is the data attribute: S-29 CHANGES the accessible
    // name while opening ("Opening <filename>…"), so a name lookup cannot
    // observe the in-flight state.
    const open = () =>
      document.querySelector<HTMLButtonElement>('[data-mail-attachment-action="inbox:uid:777:42:1:open"]')!
    expect(open()).not.toBeDisabled()
    fireEvent.click(open())
    // S-29: the row's Open control is disabled reading "Opening report.pdf…"
    // with aria-busy, and a second mint cannot start from the same row.
    await waitFor(() => expect(open()).toBeDisabled())
    expect(open()).toHaveAttribute('aria-busy', 'true')
    expect(open()).toHaveTextContent('Opening report.pdf…')
    fireEvent.click(open())
    await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
    expect(mintMailAttachmentPreview).toHaveBeenCalledTimes(1)
    await act(async () => {
      releaseMint(MINT_RESPONSE)
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(open()).not.toBeDisabled()
  })

  it('scenario 7.2 / S-19 — Back restores focus to the originating action with its announcement', async () => {
    const { mounted } = await openReportPdf()
    const openButton = screen.getByRole('button', { name: 'Open report.pdf attachment' })
    mounted.controls!.onBack()
    await waitFor(() => expect(document.activeElement).toBe(openButton))
    await waitFor(() => expect(announcerText()).toBe('Returned to report.pdf in INBOX.'))
    // The descriptor is dropped on exit: the mount handler's grant is revoked.
    await waitFor(() => expect(revokeMailAttachmentPreview).toHaveBeenCalledWith('pvw-1'))
  })

  it('MC-W3-8 — Back falls back to the message list row when the originating action is gone', async () => {
    const { mounted } = await openReportPdf()
    // Leave the message: switch folders — the action unmounts, list rows remain.
    fireEvent.click(screen.getByRole('tab', { name: 'Sent' }))
    await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
    mounted.controls!.onBack()
    await waitFor(() => {
      const active = document.activeElement as HTMLElement | null
      expect(active?.getAttribute('data-mail-message-row')).toBe('true')
    })
    await waitFor(() => expect(announcerText()).toBe("report.pdf's message is no longer in this folder. Focus moved to the message list."))
  })

  it('MC-W3-8 — Back falls back to the folder tab when no message row exists either', async () => {
    const MailPanel = await loadPanel()
    fetchMailMessages.mockResolvedValue({
      messages: [summary()],
      has_more: false, next_cursor: null, view_limit_reached: false, truncated: false, next_before_uid: null,
      metadata: FRESH_META,
    })
    fetchMailMessage.mockResolvedValue(DETAIL)
    const mounted: MountedHandoff = { handoff: null, controls: null }
    setMailAttachmentMountHandler((handoff, controls) => { mounted.handoff = handoff; mounted.controls = controls })
    // The Sent folder is confirmed absent: its view has no message rows.
    fetchMailFolders.mockResolvedValue({
      folders: [
        FOLDERS.folders[0],
        { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'absent', uidvalidity: 777, mapping_source: 'fallback' },
        FOLDERS.folders[2],
      ],
      metadata: FRESH_META,
    })
    mount(<MailPanel workspaceId="ws-1" />)
    fireEvent.click(await screen.findByRole('button', { name: /Quarterly report/ }))
    await waitFor(() => expect(screen.getByLabelText('Attachments')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: 'Open report.pdf attachment' }))
    await waitFor(() => expect(mounted.handoff).not.toBeNull())
    fireEvent.click(screen.getByRole('tab', { name: 'Sent' }))
    await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
    mounted.controls!.onBack()
    await waitFor(() => {
      const active = document.activeElement as HTMLElement | null
      expect(active?.getAttribute('data-mail-folder-tab')).toBe('true')
    })
    await waitFor(() => expect(announcerText()).toBe("report.pdf's message is no longer in this folder. Focus moved to the folder tab."))
  })
})

// The handoff describe is split at the Open/Save boundary to stay under the
// function-size budget (a describe callback is a function to that guard);
// every assertion below is unchanged from the single-describe original.
describe('MailPanel attachment handoff — Save to Library and open outcomes (C5: scenarios 6.5–6.7, 7.5)', () => {
  it('scenario 6.5 / S-15 — Save success announces "Saved to Library as <name>." and enables Open in Library, without moving focus', async () => {
    await openReportPdf()
    const saveButton = screen.getByRole('button', { name: 'Save report.pdf to Library' })
    saveButton.focus() // jsdom does not focus on click; pin the pre-click focus.
    fireEvent.click(saveButton)
    await waitFor(() => expect(saveMailAttachmentToLibrary).toHaveBeenCalledTimes(1))
    const saveArgs = saveMailAttachmentToLibrary.mock.calls[0]
    // M-02: a client-generated save_operation_token rides the request.
    const opts = saveArgs[5] as { save_operation_token: string }
    expect(typeof opts.save_operation_token).toBe('string')
    expect(opts.save_operation_token.length).toBeGreaterThan(0)
    await waitFor(() => expect(announcerText()).toBe('Saved to Library as report.pdf.'))
    // The outcome was announced WITHOUT moving focus (US-7 AS-3) — success
    // does not steal focus to a status region or a Library panel.
    expect(document.activeElement).toBe(saveButton)
    const openInLibrary = screen.getByRole('button', { name: 'Open in Library' })
    expect(openInLibrary).toBeEnabled()
  })

  it('S-15 audit-warning variant — the response’s warning code is prepended', async () => {
    saveMailAttachmentToLibrary.mockResolvedValue({ ...SAVE_RESPONSE, warning_code: 'audit_disabled' })
    await openReportPdf()
    fireEvent.click(screen.getByRole('button', { name: 'Save report.pdf to Library' }))
    await waitFor(() => expect(announcerText()).toBe('audit_disabled: Saved to Library as report.pdf.'))
  })

  it('S-16 — a refused save shows the failure with Retry, and no Open in Library appears', async () => {
    saveMailAttachmentToLibrary.mockRejectedValue(new ApiError(403, 'not permitted'))
    await openReportPdf()
    fireEvent.click(screen.getByRole('button', { name: 'Save report.pdf to Library' }))
    await waitFor(() => expect(announcerText()).toContain('Could not save to Library.'))
    expect(screen.getByTestId('mail-attachment-save-failed')).toHaveTextContent('Could not save to Library.')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open in Library' })).not.toBeInTheDocument()
  })

  it('scenario 6.6 / S-17 / FR-W3-22 — a lost save response shows "Save result unknown"; the explicit retry carries the SAME token; no automatic resend', async () => {
    saveMailAttachmentToLibrary.mockRejectedValueOnce(new ApiError(0, 'Network unavailable. Check your connection.'))
    await openReportPdf()
    fireEvent.click(screen.getByRole('button', { name: 'Save report.pdf to Library' }))
    await waitFor(() => expect(screen.getByTestId('mail-attachment-save-unknown')).toHaveTextContent(
      'Save result unknown — checking whether it saved.',
    ))
    expect(screen.getByRole('button', { name: 'Retry save' })).toBeInTheDocument()
    const firstToken = (saveMailAttachmentToLibrary.mock.calls[0][5] as { save_operation_token: string }).save_operation_token
    // No automatic resend ever fires.
    await act(async () => { await new Promise((r) => setTimeout(r, 50)) })
    expect(saveMailAttachmentToLibrary).toHaveBeenCalledTimes(1)
    // The explicit retry resolves from the prior receipt with the SAME token.
    saveMailAttachmentToLibrary.mockResolvedValueOnce(SAVE_RESPONSE)
    fireEvent.click(screen.getByRole('button', { name: 'Retry save' }))
    await waitFor(() => expect(announcerText()).toBe('Saved to Library as report.pdf.'))
    expect(saveMailAttachmentToLibrary).toHaveBeenCalledTimes(2)
    const retryToken = (saveMailAttachmentToLibrary.mock.calls[1][5] as { save_operation_token: string }).save_operation_token
    expect(retryToken).toBe(firstToken)
  })

  it('scenario 6.7 / S-26 — an Open whose transfer finds real bytes over the cap shows the cap text on the row and re-enables Open', async () => {
    mintMailAttachmentPreview.mockRejectedValue(new ApiError(413, 'attachment too large'))
    const { mounted } = await openMessagePane()
    fireEvent.click(screen.getByRole('button', { name: 'Open report.pdf attachment' }))
    await waitFor(() => expect(screen.getByTestId('mail-attachment-open-failed')).toHaveTextContent(
      'This attachment is larger than the 25 MB preview limit. Use Download.',
    ))
    const open = screen.getByRole('button', { name: 'Open report.pdf attachment' })
    expect(open).toBeEnabled() // retry or Download stays possible (US-6 AS-8)
    // No partial preview mounted: the seam never received a descriptor.
    expect(mounted.handoff).toBeNull()
    expect(screen.getByRole('button', { name: 'Download report.pdf' })).toBeEnabled()
  })

  it('scenario 6.7 / S-27 — a stale-reference Open shows S-11’s text on the row plus the pinned "Refresh list" control', async () => {
    mintMailAttachmentPreview.mockRejectedValue(new ApiError(409, 'stale reference', { body: JSON.stringify({ code: 'stale_reference' }) }))
    const { mounted } = await openMessagePane()
    fireEvent.click(screen.getByRole('button', { name: 'Open report.pdf attachment' }))
    await waitFor(() => expect(screen.getByTestId('mail-attachment-open-failed')).toHaveTextContent(
      'This message changed or was deleted. Refresh the list.',
    ))
    // S-27 pins a row-scoped "Refresh list" control beside the text.
    expect(screen.getByRole('button', { name: 'Refresh list' })).toBeInTheDocument()
    expect(mounted.handoff).toBeNull()
  })

  it('scenario 6.7 / S-28 — a busy Open shows the reason-specific copy with Retry; no preview mounts', async () => {
    mintMailAttachmentPreview.mockRejectedValue(new ApiError(503, 'busy', {
      body: JSON.stringify({ code: 'busy', reason: 'server_connection_limit' }),
    }))
    const { mounted } = await openMessagePane()
    fireEvent.click(screen.getByRole('button', { name: 'Open report.pdf attachment' }))
    await waitFor(() => expect(screen.getByTestId('mail-attachment-open-failed')).toHaveTextContent(
      'The mail server reached its connection limit. Try again shortly.',
    ))
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(mounted.handoff).toBeNull()
  })

  it('scenario 7.5 / S-20 journey — the REAL temporary viewer hosts the Open handoff: exact context bar, trusted-heading focus, in-place "Save to Library first." actions, Save → Open in Library, Back returns to mail', async () => {
    // Precondition: no test stand-in owns the seam — the REAL fallback host
    // (MailAttachmentViewer, wired into the panel) must be the one that
    // registers and renders. A leaked handler from an earlier test would
    // make this journey pass over a host that is not the production one.
    expect(hasMailAttachmentMountHandler()).toBe(false)

    const MailPanel = await loadPanel()
    fetchMailMessages.mockResolvedValue({
      messages: [summary()],
      has_more: false, next_cursor: null, view_limit_reached: false, truncated: false, next_before_uid: null,
      metadata: FRESH_META,
    })
    fetchMailMessage.mockResolvedValue(DETAIL)
    mount(<MailPanel workspaceId="ws-1" />)
    fireEvent.click(await screen.findByRole('button', { name: /Quarterly report/ }))
    await waitFor(() => expect(screen.getByLabelText('Attachments')).toBeInTheDocument())

    // ── The attachment row offers Open (US-6 AS-2) ──
    const openButton = screen.getByRole('button', { name: 'Open report.pdf attachment' })
    fireEvent.click(openButton)

    // ── Open hands off to the REAL viewer (US-6 AS-3): the fallback host
    //    mounted the temporary preview — never the mount-less
    //    preview_unavailable failure row. ──
    const viewer = await screen.findByRole('region', { name: 'Attachment preview' })
    expect(screen.queryByTestId('mail-attachment-open-failed')).not.toBeInTheDocument()

    // ── US-7 AS-1: focus moves to the context bar's trusted heading —
    //    never into the rendered content. ──
    const heading = within(viewer).getByRole('heading', { name: 'From mail: Quarterly report' })
    expect(document.activeElement).toBe(heading)

    // ── US-6 AS-3: the context bar reads EXACTLY the pinned text (§16
    //    founder F1 format, US-1.AC-1's "reads exactly") — heading,
    //    Back to mail, Save to Library, with the "·" separators — outside
    //    the rendered content. ──
    const bar = heading.closest('div') as HTMLElement
    expect(bar.textContent).toBe('From mail: Quarterly report · Back to mail · Save to Library')

    // ── The temporary content renders the preview's own MINTED byte
    //    resource (US-8 AS-3) — never the authenticated download endpoint
    //    (grill I-05: the preview role and the download role are distinct). ──
    const content = within(viewer).getByTitle('report.pdf')
    expect(content.tagName).toBe('EMBED')
    expect(content).toHaveAttribute('src', '/mail-preview/part/pvw-1')
    expect(content.getAttribute('src')).not.toContain('/library/download')

    // ── S-18: the live region announced the open with the attachment's name. ──
    await waitFor(() => expect(announcerText()).toBe('Opening report.pdf from mail.'))

    // ── US-7 AS-4 / S-20 / scenario 7.5: the five stored-file actions are
    //    discoverable with their explanation readable IN PLACE — associated
    //    text (aria-describedby → a rendered paragraph), visible without
    //    hover, screen-reader exposed, never a tooltip-only hint. ──
    const explanation = within(viewer).getByTestId('mail-attachment-save-first-note')
    expect(explanation).toBeVisible()
    expect(explanation).toHaveTextContent('Save to Library first.')
    for (const action of ['Edit', 'Rename', 'Move', 'Download', 'Fill & sign']) {
      const control = within(viewer).getByRole('button', { name: action })
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).toHaveAccessibleDescription('Save to Library first.')
      // Scenario 7.5 says a keyboard user TABS to the disabled control, so
      // it must stay in the tab order: aria-disabled, never the disabled
      // attribute (which removes it), and focusable.
      expect(control).not.toBeDisabled()
      control.focus()
      expect(document.activeElement).toBe(control)
    }

    // ── US-6 AS-5 / S-15 / US-7 AS-3: Save announces success without
    //    moving focus, and "Open in Library" becomes available. ──
    const saveButton = within(viewer).getByRole('button', { name: 'Save to Library' })
    saveButton.focus() // jsdom does not focus on click; pin the pre-click focus.
    fireEvent.click(saveButton)
    await waitFor(() => expect(saveMailAttachmentToLibrary).toHaveBeenCalledTimes(1))
    // The save carried the OPEN PANEL's route identity (the frozen descriptor
    // carries none — §3.1) plus the client-generated M-02 token.
    expect(saveMailAttachmentToLibrary).toHaveBeenCalledWith(
      'ws-1', 'mia', 'inbox', 'uid:777:42', 1,
      { save_operation_token: expect.any(String) },
    )
    await waitFor(() => expect(announcerText()).toBe('Saved to Library as report.pdf.'))
    expect(document.activeElement).toBe(saveButton)
    const openInLibrary = within(viewer).getByRole('button', { name: 'Open in Library' })
    expect(openInLibrary).toBeEnabled()
    // The temporary view never pretends it was already saved (§6): the
    // stored-file actions still explain in place after the save — the saved
    // copy is reached through Open in Library.
    expect(within(viewer).getByRole('button', { name: 'Edit' })).toHaveAttribute('aria-disabled', 'true')

    // ── US-6 AS-4 / US-7 AS-2 / S-19: Back disposes the temporary source
    //    (the grant is revoked) and returns to the mail context — focus on
    //    the originating action with its announcement. ──
    fireEvent.click(within(viewer).getByRole('button', { name: 'Back to mail' }))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Attachment preview' })).not.toBeInTheDocument())
    await waitFor(() => expect(revokeMailAttachmentPreview).toHaveBeenCalledWith('pvw-1'))
    expect(document.activeElement).toBe(openButton)
    await waitFor(() => expect(announcerText()).toBe('Returned to report.pdf in INBOX.'))
  })
})
