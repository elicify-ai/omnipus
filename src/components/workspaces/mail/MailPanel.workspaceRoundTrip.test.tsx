// RED — mailbox-config query never re-fires after a workspace round trip.
//
// Repro (escalated by a backend-lead dispatch, confirmed reproducible 6/6):
// after navigating workspace A (a real, working, configured mailbox) -> B
// (no mailbox configured) -> C (a configured mailbox whose IMAP connection
// is broken) -> back to A, the Mail panel shows "Choose a mailbox" for A
// despite GET /api/v1/mailboxes correctly listing A's mailbox the whole
// time (confirmed via the actual response body — this test asserts the
// SAME oracle: mailboxesQuery never loses A's row).
//
// Root cause traced via the real state machine (usePanelDeepLink.ts +
// MailPanel.tsx), NOT the mailboxesQuery/invalidateQueries path F3
// targeted: usePanelDeepLink's adoption effect fires on every workspaceId
// change while Mail stays mounted (F3's own "stays mounted" case). Its
// isAlreadyAdopted correctly detects the workspace changed (F3), but the
// follow-up `adoptionContext` call UNCONDITIONALLY derives a fresh
// mailboxId directive from the URL's `agent` search param — which is
// simply never set for an ordinary workspace-tab switch (no deep link
// involved) — collapsing every workspace-follow re-adoption to the
// EXPLICIT "choose a mailbox" `null` directive (MailPanelProps' own
// documented null semantics, `MailPanel.tsx` lines 110-119). That
// `mailboxId: null` directive short-circuits MailPanel's `agentId` memo
// at `if (mailboxId === null) return null` BEFORE it ever reaches the
// intent/auto-select fallback that would have found A's real, still-cached
// mailbox. `mailboxesQuery` itself is never wrong — the oracle below
// confirms fetchMailboxes is called at most once and always answers with
// A's own mailbox in its list; the panel just never asks it because the
// directive says "show the picker," not "auto-select."
//
// Oracle: MailPanelProps' own doc comment on `mailboxId` (undefined = "no
// directive... the panel keeps its own posture... else a single configured
// mailbox opens live") is the spec this test derives its expectation from,
// independent of the implementation under test.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Router mocks (usePanelDeepLink needs these; same shape as the sibling
// usePanelDeepLink.mailboxSwitch.test.tsx harness) ─────────────────────────
let mockSearch: Record<string, unknown> = { panel: 'mail' }
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: <T,>({ select }: { select: (s: { location: { search: Record<string, unknown> } }) => T }) =>
    select({ location: { search: mockSearch } }),
  useBlocker: () => undefined,
}))

vi.mock('@/components/library/preview/unsavedGuard', () => ({
  confirmDiscardLibraryEdits: vi.fn(async () => true),
  isLibraryEditorDirty: () => false,
}))

// ── Mail data mocks (MailPanel.states.test.tsx's own pattern) ─────────────
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

// ── Real hooks/store under test ────────────────────────────────────────────
import { usePanelDeepLink } from '@/components/panel-shell/usePanelDeepLink'
import { useUiStore } from '@/store/ui'
import type { WorkspacePanelContext } from '@/components/panel-shell/types'
import { MailPanel } from './MailPanel'

const folders = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 0, unread_count: null },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null },
  ],
}

// A: the real, working, configured mailbox the bug report says never comes
// back. B: no row at all for this workspace. C: a configured mailbox with
// a broken connection (still present in the app-wide list — GET /mailboxes
// never drops it, it just fails to connect).
const MAILBOXES = [
  { agent_id: 'mia', workspace_id: 'ws-A', enabled: true, configured: true, username: 'mia@a.test' },
  { agent_id: 'cleo', workspace_id: 'ws-C', enabled: true, configured: true, username: 'cleo@c.test' },
]

function Harness({ workspaceId }: { workspaceId: string }) {
  usePanelDeepLink(workspaceId, 'mail')
  const activePanel = useUiStore((s) => s.activePanel)
  const mailboxId =
    activePanel !== null && activePanel.id === 'mail'
      ? (activePanel.context as WorkspacePanelContext).mailboxId
      : undefined
  return <MailPanel workspaceId={workspaceId} mailboxId={mailboxId} />
}

function renderRoundTrip() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <Harness workspaceId="ws-A" />
    </QueryClientProvider>,
  )
  return { client, ...view }
}

describe('Mail panel — workspace round trip keeps the mailbox directive correct (F3 follow-up)', () => {
  beforeEach(() => {
    mockSearch = { panel: 'mail' }
    fetchAgents.mockReset()
    fetchMailboxes.mockReset()
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset()
    fetchMailMessage.mockReset()
    fetchMailSummary.mockReset()
    markMailSeen.mockReset()
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }, { id: 'cleo', name: 'Cleo' }])
    fetchMailboxes.mockResolvedValue(MAILBOXES)
    fetchMailFolders.mockResolvedValue(folders)
    fetchMailMessages.mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
    fetchMailMessage.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
    markMailSeen.mockResolvedValue(undefined)

    // Seed the panel as already open for workspace A via the tab-strip
    // toggle (MailPanelProps: "undefined = no directive... tab-strip
    // toggle, plain opens"), NOT via a deep link — matching how the real
    // repro is driven (a plain workspace switch, no `?agent=` URL param
    // ever set). This is the F3 "stays mounted" starting condition.
    useUiStore.setState({ activePanel: null })
    useUiStore.getState().openPanel('mail', { workspaceId: 'ws-A' })
  })

  afterEach(() => {
    cleanup()
    useUiStore.setState({ activePanel: null })
  })

  it('re-selects workspace A\'s own configured mailbox after an A -> B -> C -> A round trip', async () => {
    const { rerender, client } = renderRoundTrip()

    // Sanity: A starts correctly selected (auto-selected single mailbox).
    await waitFor(() => expect(screen.queryByTestId('mail-choose-mailbox')).not.toBeInTheDocument())
    expect(await screen.findByText('INBOX')).toBeInTheDocument()

    const rerenderAt = (workspaceId: string) => {
      act(() => {
        rerender(
          <QueryClientProvider client={client}>
            <Harness workspaceId={workspaceId} />
          </QueryClientProvider>,
        )
      })
    }

    // A -> B (no mailbox at all for B).
    rerenderAt('ws-B')
    await waitFor(() => expect(screen.getByTestId('mail-choose-mailbox')).toBeInTheDocument())

    // B -> C (a configured mailbox, connection broken — still a real row).
    rerenderAt('ws-C')
    await act(async () => { await Promise.resolve() })

    // C -> back to A.
    rerenderAt('ws-A')

    // Oracle (independent of the buggy code path): the app-wide mailbox
    // list, served by the fetchMailboxes mock, has ALWAYS included A's own
    // mailbox — GET /api/v1/mailboxes never dropped it (the finding's own
    // confirmed fact) — and every fetch resolved with that same fixture.
    expect(fetchMailboxes).toHaveBeenCalled()
    for (const result of fetchMailboxes.mock.results) {
      expect(result.type).toBe('return')
      await expect(result.value as Promise<typeof MAILBOXES>).resolves.toEqual(MAILBOXES)
    }

    await waitFor(() => {
      expect(screen.queryByTestId('mail-choose-mailbox')).not.toBeInTheDocument()
    })
    expect(await screen.findByText('INBOX')).toBeInTheDocument()
  })
})
