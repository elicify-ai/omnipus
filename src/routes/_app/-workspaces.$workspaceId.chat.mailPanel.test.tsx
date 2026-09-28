// workspaces.$workspaceId.chat.mailPanel.test.tsx — wave-2 RED pack for
// side-panel-shell-spec.md §10 "Wave 2 — Mail adopts the shell", tested at
// the runtime level through the REAL routeTree + a real memory-history
// router (same technique as -workspaces.$workspaceId.chat.deepLink.test.tsx,
// whose harness this file mirrors). ChatScreen is stubbed to `() => null` —
// this file is about panel/URL wiring, not the chat UI.
//
// Oracles:
//   §8.2 — "Valid `panel` values are the REGISTERED panel ids" + the Mail
//   bullet: "`panel=mail` with no agent/mailbox selected opens the Mail
//   panel on its 'choose a mailbox' state (Mail starts nothing costly)"; US-7
//   AS-6 (same, as an acceptance scenario); §12 dataset row 8. Wave 2
//   (§10 + FR-014) registers mail, so `?panel=mail` is a VALID value — the
//   wave-1 "drop unregistered ids" path must NOT swallow it.
//   §8.2 two-direction rule — the open panel is the store's activePanel AND
//   its addressable projection on the chat URL (canonical form
//   `/#/workspaces/{workspaceId}/chat?panel=<registered id>`).
//   §15 item 4 — "Mail (wave 2): the chat draft link opens the Mail panel
//   (email spec's link)" through the same SINGLE-PANEL state (SP-7: opening
//   Mail replaces an open Library).
//   email-mail-view-spec.md §17 — the draft link scheme:
//   `/#/workspaces/{wsId}/mail?mailbox={agentId}&folder=drafts&message={ref}`.
//
// RED evidence (2026-09-28, read in full): the production registry
// (src/components/panel-shell/registry.tsx) registers only library+browser;
// usePanelDeepLink adopts/projects ONLY 'library' — a `?panel=mail` deep
// link hits its "named non-library param" branch: URL replaced to drop the
// param and any open panel closed. The store never holds
// { id: 'mail', ... } from a deep link, and the mail route stub navigates
// back to chat with NO `panel` search, which the hook's projection
// (library-only) never writes for mail. Every assertion below fails on
// HEAD for exactly those reasons.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => null,
}))

vi.mock('framer-motion', () => ({
  motion: new Proxy(
    {},
    {
      get:
        (_: object, prop: string) =>
        React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: React.Ref<unknown>) =>
          React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)),
    },
  ),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
    // Mail reads (gateway edge — the one boundary this file fakes):
    fetchMailboxes: vi.fn(async () => [
      { agent_id: 'agent-a', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ]),
  }
})

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return {
    ...actual,
    fetchMailFolders: vi.fn(async () => ({
      folders: [
        { slug: 'inbox', display_name: 'INBOX', total: 0, unread_count: 0 },
        { slug: 'sent', display_name: 'Sent', total: 0, unread_count: 0 },
        { slug: 'drafts', display_name: 'Drafts', total: 1, unread_count: 0 },
      ],
    })),
    fetchMailMessages: vi.fn(async () => ({ messages: [], truncated: false, next_before_uid: null })),
    fetchMailMessage: vi.fn(async () => ({}) as never),
    fetchMailSummary: vi.fn(async () => ({ items: [] })),
    markMailSeen: vi.fn(async () => undefined),
    mintMailHtmlPreviewToken: vi.fn(async () => ({ token: 't'.repeat(43), expires_in_seconds: 900 })),
  }
})

import { routeTree } from '@/routeTree.gen'
import { useUiStore } from '@/store/ui'
import {
  fetchMailFolders,
  fetchMailMessages,
} from '@/lib/api/mail'
import { readMailPanelIntent } from '@/components/workspaces/mail/mailPanelIntent'

const originalMatchMedia = window.matchMedia
const INTENT_KEY = 'omnipus.mail-panel.intent.ws-1'

beforeEach(() => {
  localStorage.setItem('omnipus_auth_username', 'uat-tester')
  sessionStorage.removeItem(INTENT_KEY)
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

afterEach(() => {
  localStorage.removeItem('omnipus_auth_username')
  sessionStorage.removeItem(INTENT_KEY)
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: originalMatchMedia,
  })
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function activePanelId(): string | null {
  const state = useUiStore.getState() as unknown as { activePanel?: { id: string } | null }
  return state.activePanel?.id ?? null
}

async function mountAt(initialEntry: string) {
  const client = makeClient()
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 10_000 })
  return { client, router }
}

describe('workspace Chat route — ?panel=mail deep link (§8.2 Mail bullet, §12 dataset row 8, wave 2)', () => {
  beforeEach(() => {
    useUiStore.setState({ activePanel: null } as never)
  })

  it('a cold ?panel=mail deep link opens the Mail panel and the URL KEEPS panel=mail (registered id — §8.2; US-7 AS-6)', async () => {
    const { client, router } = await mountAt('/workspaces/ws-1/chat?panel=mail')

    await waitFor(() => expect(activePanelId()).toBe('mail'), { timeout: 10_000 })
    // The open panel is the store state AND its addressable projection: mail
    // is a REGISTERED id at wave 2, so the address keeps it — exactly like
    // ?panel=library, unlike the unknown/unregistered drop-to-replace path.
    const search = router.state.location.search as Record<string, unknown>
    expect(search.panel).toBe('mail')

    client.clear()
  })

  it('?panel=mail with no mailbox selected opens the CHOOSE-A-MAILBOX state — the picker faces the user and Mail starts nothing costly (SP-23; §8.2; §11 BDD "Mail deep link without a mailbox")', async () => {
    const { client } = await mountAt('/workspaces/ws-1/chat?panel=mail')

    await waitFor(() => expect(activePanelId()).toBe('mail'), { timeout: 10_000 })

    // The mailbox picker is what the user faces — the "Choose a mailbox"
    // state, not a mailbox auto-landed-for-them.
    const picker = await screen.findByRole('combobox', { name: 'Mailbox' }, { timeout: 10_000 })
    expect(within(picker).getByText('Choose a mailbox')).toBeInTheDocument()

    // "Mail starts nothing costly": with no mailbox selected, no IMAP dial
    // happens — folders/messages fetches are the panel's only dialing reads
    // (D29/R2-5: the summary GET reads saved watcher state, not a dial).
    expect(fetchMailFolders).not.toHaveBeenCalled()
    expect(fetchMailMessages).not.toHaveBeenCalled()

    client.clear()
  })

  it('the create_email_draft chat draft link opens Mail through the single-panel state: Library closes, Mail opens, URL lands on chat?panel=mail (§15 item 4; SP-7; email spec §17 chat_link scheme)', async () => {
    // Library is open — the draft link must REPLACE it (one panel at a
    // time), not stack beside it and not be blocked by it.
    useUiStore.setState({
      activePanel: { id: 'library', context: { workspaceId: 'ws-1' } },
    } as never)

    const { client, router } = await mountAt(
      `/workspaces/ws-1/mail?mailbox=agent-a&folder=drafts&message=${encodeURIComponent('mid:<draft-1@test>')}`,
    )

    await waitFor(() => expect(activePanelId()).toBe('mail'), { timeout: 10_000 })
    // The draft landed with its context: the intent carries the mailbox and
    // folder (email spec MC-31a). The consume-once messageRef is deliberately
    // NOT asserted here — once the panel mounts it lifts the ref and its
    // persist effect rewrites messageRef to null, so racing that from outside
    // would flake; ref consumption belongs to the mail panel's own pack.
    const intent = readMailPanelIntent('ws-1')
    expect(intent.agentId).toBe('agent-a')
    expect(intent.folder).toBe('drafts')

    // The URL never dead-ends on the /mail stub — it lands on the chat route
    // (the panel-host page), carrying the registered panel param.
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    const search = router.state.location.search as Record<string, unknown>
    expect(search.panel).toBe('mail')

    client.clear()
  })
})
