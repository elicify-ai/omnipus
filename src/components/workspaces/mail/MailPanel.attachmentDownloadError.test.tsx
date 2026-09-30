/**
 * RED — IMPORTANT: the attachment download button fails silently on error.
 *
 * Spec source: MailPanel.tsx's OWN established error-surface pattern. Every
 * other mutation in this file — draftSave, draftSend, draftDiscard, and
 * composeSend — has an explicit
 *   `onError: (err) => addToast({ message: mailErrorCode(err), variant: 'error' })`
 * (MailPanel.tsx lines 338, 364, 375, 423), and `mailErrorCode` (line 76)
 * documents its own contract: "Extract the error CLASS for display (US-3
 * AS-4) ... Never a generic string alone — the class IS the diagnosis."
 * AttachmentList's Download button instead calls
 *   `void downloadMailAttachment({...})`
 * (line 843) — an async function with no try/catch anywhere in its chain
 * (downloadMailAttachment, lines 857-873) and no `.catch` at the call site.
 * It is the one uncovered path in a file where every other failure surfaces
 * a toast.
 *
 * Oracle: when `fetchMailAttachment` rejects, a toast with the SAME shape
 * every other error path in this file produces must appear —
 * `role="alert"` (ToastContainer.tsx's error variant) carrying the
 * rejection's error class via `mailErrorCode` — never a click that does
 * nothing visible. This is derived from the file's own established pattern
 * (which the brief explicitly directs matching), not from what
 * AttachmentList currently does.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AppShell } from '@/components/layout/AppShell'
import { useUiStore } from '@/store/ui'

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailMessage,
  fetchMailSummary,
  markMailSeen,
  fetchMailAttachment,
  fetchAppState,
  fetchNotifications,
  fetchTasks,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailMessage: vi.fn(),
  fetchMailSummary: vi.fn(),
  markMailSeen: vi.fn(),
  fetchMailAttachment: vi.fn(),
  fetchAppState: vi.fn(),
  fetchNotifications: vi.fn(),
  fetchTasks: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents, fetchMailboxes, fetchAppState, fetchNotifications, fetchTasks }
})

// Mount the production shell so its real toast host renders the alert. Only
// unrelated shell children (router, overlays, and panels) are stubbed; the
// MailPanel, its API calls, the UI store, and the toast renderer stay real.
vi.mock('@/components/layout/Sidebar', () => ({ Sidebar: () => null }))
vi.mock('@/components/layout/NotificationPanel', () => ({ NotificationPanel: () => null }))
vi.mock('@/components/agents/ToolApprovalModal', () => ({ ToolApprovalModal: () => null }))
vi.mock('@/components/chat/MediaLightbox', () => ({ MediaLightbox: () => null }))
vi.mock('@/components/search/SearchModal', () => ({ SearchModal: () => null }))
vi.mock('@/components/layout/CrossWorkspaceApprovalBanner', () => ({ CrossWorkspaceApprovalBanner: () => null }))
vi.mock('@/components/layout/GodModeIndicators', () => ({ GodModeCornerDot: () => null }))
vi.mock('@/components/panel-shell/PanelTabPresenceBridge', () => ({ PanelTabPresenceBridge: () => null }))
vi.mock('@/components/panel-shell/SidePanelShell', () => ({ SidePanelShell: () => null }))
vi.mock('@/hooks/useVersionCheck', () => ({ useVersionCheck: () => undefined }))
vi.mock('@/hooks/useMediaQuery', () => ({ useMediaQuery: () => false }))

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return {
    ...actual,
    fetchMailFolders,
    fetchMailMessages,
    fetchMailMessage,
    fetchMailSummary,
    markMailSeen,
    fetchMailAttachment,
  }
})

async function loadPanel(): Promise<React.ComponentType<{ workspaceId: string }>> {
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
  // The real AppShell mounts the real ToastContainer, both sharing MailPanel's
  // unmocked useUiStore. The assertion below checks a rendered alert, not
  // merely a mocked addToast call or an entry in the store.
  return render(
    <QueryClientProvider client={client}>
      {node}
      <AppShell />
    </QueryClientProvider>,
  )
}

const folders = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 1, unread_count: 1 },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null },
  ],
}

const listedMessage = {
  message_id: '<quarterly@example.test>',
  uid: 42,
  uidvalidity: 777,
  folder: 'inbox' as const,
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
}

const detailWithAttachment = {
  ...listedMessage,
  reply_to: null,
  in_reply_to: null,
  references: null,
  body_text: 'See attached.',
  has_html: false,
  bcc: null,
  attachments: [
    { part_index: 1, filename: 'report-q3.pdf', content_type: 'application/pdf', size_bytes: 2048 },
  ],
  body_markdown: null,
  markdown_lossy: false,
}

describe('MailPanel — attachment download error surfacing (silent-failure fix)', () => {
  beforeEach(() => {
    fetchAgents.mockReset()
    fetchMailboxes.mockReset()
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset()
    fetchMailMessage.mockReset()
    fetchMailSummary.mockReset()
    markMailSeen.mockReset()
    fetchMailAttachment.mockReset()
    fetchAppState.mockReset()
    fetchNotifications.mockReset()
    fetchTasks.mockReset()
    fetchAppState.mockResolvedValue({ dev_mode_bypass: false })
    fetchNotifications.mockResolvedValue({ notifications: [], unread_count: 0 })
    fetchTasks.mockResolvedValue([])
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(folders)
    fetchMailMessages.mockResolvedValue({ messages: [listedMessage], truncated: false, next_before_uid: null })
    fetchMailMessage.mockResolvedValue(detailWithAttachment)
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
    // Toasts are a real, shared, module-level store — start every test with
    // none left over from a previous one in this file.
    for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  })

  afterEach(() => {
    cleanup()
    for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  })

  it('surfaces a visible error toast when the attachment download rejects', async () => {
    fetchMailAttachment.mockRejectedValue(Object.assign(new Error('download failed'), { code: 'attachment_not_found' }))

    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)

    fireEvent.click(await screen.findByRole('button', { name: /Quarterly/ }))
    const downloadButton = await screen.findByRole('button', { name: /download/i })
    fireEvent.click(downloadButton)

    await waitFor(() => {
      expect(fetchMailAttachment).toHaveBeenCalled()
    })

    // The SAME toast shape draftSave/draftSend/draftDiscard/composeSend use
    // on error: an alert-role toast whose visible text names the error
    // class mailErrorCode extracts (here, the ApiError-shaped rejection's
    // `code`), not a click that does nothing.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('attachment_not_found')
  })
})
