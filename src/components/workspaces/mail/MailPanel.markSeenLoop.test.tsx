/**
 * RED — IMPORTANT: the mark-seen effect has no dependency array and
 * re-fires the mutation on every re-render.
 *
 * Spec source: MailPanel.tsx's own comment on the effect itself
 * (lines 378-380): "Mark seen ON OPEN (B-27, US-6): after a successful
 * detail fetch of an unseen message outside Drafts." — "on open" is a
 * once-per-open action, not a per-render one. The effect (lines 381-385):
 *
 *   useEffect(() => {
 *     if (detailQuery.isSuccess && detail !== null && detail.seen === false
 *         && folder !== 'drafts' && selectedRef !== null
 *         && !seenMutation.isPending) {
 *       seenMutation.mutate(selectedRef)
 *     }
 *   })
 *
 * has NO second argument — it runs after EVERY render, not just when
 * `selectedRef`/`detail` change. `seenMutation`'s `onSuccess` (lines 301-304)
 * invalidates FOLDERS_KEY and MESSAGES_KEY — which the panel already
 * subscribes to via `foldersQuery`/`messagesQuery` — but never DETAIL_KEY, so
 * `detail.seen` stays `false` forever. Each of those two invalidations
 * triggers its own refetch-driven re-render (the panel's OWN existing
 * behavior, not anything this test adds), the effect re-runs with no guard
 * against re-running, `seenMutation.isPending` is back to `false` by then,
 * and the condition is true again — `markMailSeen` fires again, which
 * invalidates FOLDERS_KEY/MESSAGES_KEY again, and so on for as long as the
 * message stays open.
 *
 * Oracle: "mark seen on open" means the mutation fires exactly once per
 * message-open, derived directly from the effect's own doc comment — never
 * from counting however many times the current code happens to call it.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react'
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
  const specifier = './' + 'MailPanel'
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
  seen: false,
  is_draft: false,
  is_omnipus_draft: false,
  read_by_agent: false,
}

// The detail fetch never gets a chance to reflect the seen mutation
// succeeding: seenMutation.onSuccess invalidates FOLDERS_KEY/MESSAGES_KEY
// only, never DETAIL_KEY (MailPanel.tsx lines 301-304), so every refetch of
// this same detail continues to answer `seen: false`.
const unseenDetail = {
  ...listedMessage,
  reply_to: null,
  in_reply_to: null,
  references: null,
  body_text: 'Body.',
  has_html: false,
  bcc: null,
  attachments: [],
  body_markdown: null,
  markdown_lossy: false,
}

describe('MailPanel — mark-seen effect fires exactly once per open message', () => {
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
    fetchMailMessages.mockResolvedValue({ messages: [listedMessage], truncated: false, next_before_uid: null })
    fetchMailMessage.mockResolvedValue(unseenDetail)
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)
  })

  afterEach(() => cleanup())

  it('calls markMailSeen exactly once, even after the folders/messages invalidation it triggers causes a re-render', async () => {
    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)

    fireEvent.click(await screen.findByRole('button', { name: /Quarterly/ }))

    // First mark-seen fires on open, as intended.
    await waitFor(() => {
      expect(markMailSeen).toHaveBeenCalled()
    })

    // Give the mutation's OWN onSuccess invalidation (FOLDERS_KEY,
    // MESSAGES_KEY) time to refetch and re-render the panel — the exact
    // re-render this bug's effect (no dependency array) reacts to by
    // firing the mutation again, since detail.seen is still false.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 100))
    })

    // "Mark seen ON OPEN" (the effect's own doc comment) means exactly once
    // per open message — not once per render for as long as it stays open.
    expect(markMailSeen).toHaveBeenCalledTimes(1)
  })
})
