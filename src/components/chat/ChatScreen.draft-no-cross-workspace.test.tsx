// RED — #1853: an unsent workspace-chat draft must not follow the user into
// another workspace's chat.
// Oracle: gh issue view 1853 — in the other workspace's chat the message box
// does not show the draft (not while it is disabled during the session
// restore, not after Send re-enables), and a Send from the other chat can
// never post that text there. The expected values (empty box; no message
// frame carrying the draft into the other session) come from the issue,
// never from the code under test.
//
// Real ChatScreen, real OmnipusRuntimeProvider (the app-wide assistant-ui
// runtime — what holds the composer text), real stores, real entry flow
// (enterWorkspaceChat), real WsConnection + frame parser. The router mirrors
// the real route chain: a param-reading chat route that fires the workspace
// entry on workspaceId change (WorkspaceTabContainer.tsx's entry effect) and
// shows a restoring placeholder while the entry resolves
// (WorkspaceChatTab.tsx's skeleton), with the runtime provider mounted above
// both (AppShell.tsx mounts it above the Outlet). The sidebar move is driven
// exactly as the real sidebar does it (Sidebar.tsx workspace onOpen):
// setActiveWorkspaceId + router.navigate — a route param change, no reload.
// Only the network edge is doubled: fetch (request-recording stub) and the
// browser WebSocket class (frame-recording stub below the real parser).
//
// Brief item 3: what the box holds when the user RETURNS to workspace A is
// not decided (kept or cleared) — no test asserts it; see the RED report.
import { useEffect, act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory, createRootRoute, createRoute, createRouter,
  Outlet, RouterProvider,
} from '@tanstack/react-router'
import type { Session, SessionHistoryResponse, Workspace } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { ChatScreen } from './ChatScreen'
import { OmnipusRuntimeProvider } from './OmnipusRuntimeProvider'
import {
  Network, TestSocket, json, resetRealStores, stopHarness, workspace, NOW,
} from '../../../tests/l1-room-ui/harness'
import { expectValidPage, page, snapshot } from '../../../tests/chat-history-paging/fixtures'

const WS_A = 'ws-alpha'
const WS_B = 'ws-beta'
const SESSION_A = 'main-mia-alpha'
const SESSION_B = 'main-mia-beta'
// Arbitrary user-chosen draft content; only the issue's required behaviour
// (the other chat's box holds none of it) is the oracle.
const DRAFT = 'Draft for the Alpha quarterly numbers'
// Revision 1 (CHECK F1): the sentinel is a second, known send after the
// checked Send click. Its frame must reach workspace B's session, which
// proves the recorded socket log observed activity strictly after the click —
// a clean log is never vacuously true.
const MARKER = 'workspace B marker sent after the checked send'
// Virtual-time windows for the fake-timer proof. The first settles the checked
// Send (a build that merely delays the submit — CHECK mutant M5 added 250 ms —
// fires inside it); the second runs every remaining pending timer before the
// recorded log is read. An unbounded runAllTimersAsync cannot be used because
// the real WsConnection keeps self-rescheduling its heartbeat timer, so the
// timer queue never drains; both windows are generous multiples of the known
// scheduling delays while staying bounded.
const SENTINEL_SETTLE_MS = 1_000
const QUEUED_SUBMIT_FLUSH_MS = 120_000

const originalScrollTo = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTo')
let net: Network
// The workspace entry's FIRST session-list read is held until the hub socket
// is open, so the initial attach cannot race a socket that is still
// connecting (the real entry waits for exactly that through waitForSocketOpen).
// Later entries (the workspace switch) run on an open socket and pass freely.
let entryGates: Array<() => void> = []
let entryGateArmed = true

function workspaceRecord(id: string, name: string, mainSessionId: string): Workspace {
  return workspace({
    id,
    name,
    member_configs: { mia: { main_session_id: mainSessionId } },
  })
}

function sessionRecord(id: string, workspaceId: string): Session {
  return {
    id,
    type: 'main',
    agent_id: 'mia',
    workspace_id: workspaceId,
    title: `${workspaceId} main chat`,
    status: 'active',
    created_at: NOW,
    updated_at: NOW,
    channel: 'web',
    partitions: [],
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
  }
}

function historyFor(sessionId: string): SessionHistoryResponse {
  const history = page([], { sessionId })
  expectValidPage(history, [], 0)
  return history
}

beforeEach(() => {
  entryGates = []
  entryGateArmed = true
  resetRealStores()
  // Plain-list branch (jsdom has no layout), as the shared chat tests do.
  vi.stubGlobal('ResizeObserver', undefined)
  net = new Network()
  net.workspaces = [workspaceRecord(WS_A, 'Alpha', SESSION_A), workspaceRecord(WS_B, 'Beta', SESSION_B)]
  net.respond('GET', `/api/v1/workspaces/${WS_A}`, () => json(workspaceRecord(WS_A, 'Alpha', SESSION_A)))
  net.respond('GET', `/api/v1/workspaces/${WS_B}`, () => json(workspaceRecord(WS_B, 'Beta', SESSION_B)))
  net.respond('GET', '/api/v1/sessions', async () => {
    if (entryGateArmed) await new Promise<void>((resolve) => { entryGates.push(resolve) })
    // SessionPage envelope (contract): {sessions, next_cursor?, partial_errors?}.
    return json({ sessions: [sessionRecord(SESSION_A, WS_A), sessionRecord(SESSION_B, WS_B)] })
  })
  net.respond('GET', `/api/v1/sessions/${SESSION_A}`, () => json({ session: sessionRecord(SESSION_A, WS_A) }))
  net.respond('GET', `/api/v1/sessions/${SESSION_B}`, () => json({ session: sessionRecord(SESSION_B, WS_B) }))
  net.respond('GET', `/api/v1/sessions/${SESSION_A}/messages`, () => json(historyFor(SESSION_A)))
  net.respond('GET', `/api/v1/sessions/${SESSION_B}/messages`, () => json(historyFor(SESSION_B)))
  net.respond('GET', '/api/v1/skills', () => json([]))
  net.respond('GET', '/api/v1/commands', () => json([]))
  net.respond('GET', '/api/v1/providers', () => json([]))
  // Both workspaces remember the chat the user last had there (the store
  // writes these descriptors itself on attach; seeding them is the steady
  // state of the issue's scenario — the user has chatted in both).
  act(() => {
    useSessionStore.setState({
      sessionByWorkspace: {
        [WS_A]: { id: SESSION_A, type: 'main', title: 'Alpha main chat', agentId: 'mia' },
        [WS_B]: { id: SESSION_B, type: 'main', title: 'Beta main chat', agentId: 'mia' },
      },
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
    })
    useWorkspacesStore.setState({ activeWorkspaceId: WS_A })
  })
})

afterEach(() => {
  // A revised send test enables fake timers mid-test; never leak them into
  // the next test (or into stopHarness's waits).
  vi.useRealTimers()
  stopHarness()
  if (originalScrollTo) Object.defineProperty(Element.prototype, 'scrollTo', originalScrollTo)
  else Reflect.deleteProperty(Element.prototype, 'scrollTo')
})

// The root hosts the query provider, as the shared room harness's mountAt does.
const rootRoute = createRootRoute({
  component: () => <QueryClientProvider client={queryClient}><Outlet /></QueryClientProvider>,
})

/**
 * The real chat route shape: the route reads the workspace from the ROUTER,
 * the container fires the workspace entry on workspaceId change
 * (WorkspaceTabContainer.tsx's "Bug 1 fix" effect), the tab shows the
 * restoring skeleton while the entry resolves (WorkspaceChatTab.tsx), and the
 * app-wide runtime provider wraps it all (AppShell.tsx mounts it above the
 * Outlet). Navigating to the other workspace re-renders this chain with the
 * new param and no route remount — exactly what a sidebar workspace click does.
 */
function buildChatRouter(initialEntry: string) {
  const appRoute = createRoute({ getParentRoute: () => rootRoute, id: '_app', component: Outlet })
  const wsRoute = createRoute({ getParentRoute: () => appRoute, path: 'workspaces/$workspaceId', component: Outlet })
  const ChatRoute = createRoute({
    getParentRoute: () => wsRoute,
    path: 'chat',
    component: function ChatRouteComponent() {
      const { workspaceId } = ChatRoute.useParams()
      const isRestoring = useSessionStore((s) => s.resolvingSessionForWorkspace[workspaceId] === true)
      const enterWorkspaceChat = useSessionStore((s) => s.enterWorkspaceChat)
      useEffect(() => {
        if (!workspaceId) return
        void enterWorkspaceChat(workspaceId)
      }, [workspaceId, enterWorkspaceChat])
      return (
        <OmnipusRuntimeProvider>
          {isRestoring ? (
            <div data-testid="workspace-chat-restoring" role="status">Restoring your conversation…</div>
          ) : (
            <ChatScreen />
          )}
        </OmnipusRuntimeProvider>
      )
    },
  })
  return createRouter({
    routeTree: rootRoute.addChildren([appRoute.addChildren([wsRoute.addChildren([ChatRoute])])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
    defaultPendingMinMs: 0,
  })
}

async function mountAndOpen() {
  const router = buildChatRouter(`/workspaces/${WS_A}/chat`)
  TestSocket.instances = []
  vi.stubGlobal('WebSocket', TestSocket)
  await act(async () => { await router.load() })
  render(<RouterProvider router={router} />)
  // OmnipusRuntimeProvider's WsLifecycle owns the connection: open its socket
  // so the entry's attach can actually send.
  await waitFor(() => {
    if (!TestSocket.instances[0]) throw new Error('WsLifecycle did not open its hub socket')
  })
  const socket = TestSocket.instances[0]
  act(() => socket.open())
  // Socket open: the entry's session reads may proceed, so the attach lands
  // on a live connection (as it does in the browser, where the handshake
  // finishes before the entry's REST reads resolve).
  entryGateArmed = false
  entryGates.forEach((release) => release())
  entryGates = []
  await waitFor(() => {
    if (!socket.sent.some((frame) => frame.type === 'attach_session' && frame.session_id === SESSION_A)) {
      throw new Error('workspace A entry must attach its remembered session')
    }
  })
  const emitSessionReplay = (sessionId: string) => {
    act(() => {
      socket.receive({ ...snapshot(historyFor(sessionId)), seq: 1, boot_id: 'draft-cross-ws' })
      socket.receive({
        type: 'session_state', session_id: sessionId, user_id: 'red-draft-test',
        history_state: { status: 'available' }, pending_approvals: [], emitted_at: NOW,
      })
      socket.receive({ type: 'done', session_id: sessionId })
      socket.receive({ type: 'catch_up_complete', session_id: sessionId, seq: 1, boot_id: 'draft-cross-ws', mode: 'snapshot' })
    })
  }
  emitSessionReplay(SESSION_A)
  await waitFor(() => expect(useChatStore.getState().isReplaying, 'workspace A replay finished').toBe(false))
  return { socket, emitSessionReplay, router }
}

const chatInput = () => screen.getByTestId('chat-input')

/** The sidebar move (Sidebar.tsx workspace onOpen): store write, then navigate. */
async function sidebarMoveTo(
  navigate: (opts: {
    to: '/workspaces/$workspaceId/chat'
    params: { workspaceId: string }
  }) => Promise<void>,
  workspaceId: string,
): Promise<void> {
  act(() => { useWorkspacesStore.getState().setActiveWorkspaceId(workspaceId) })
  await act(async () => {
    await navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId } })
  })
}

describe('#1853 an unsent chat draft must not follow the user into another workspace', () => {
  it('control: the draft stays in workspace A\'s chat while the user stays there', async () => {
    await mountAndOpen()
    const user = userEvent.setup()
    await user.type(chatInput(), DRAFT)
    // Brief item 4: staying in the workspace, the box holds exactly what was
    // typed. This proves the harness sees the real box and its draft.
    expect(chatInput()).toHaveValue(DRAFT)
  })

  it('RED: during the restore the other chat\'s disabled box does not show the draft', async () => {
    const { socket, router } = await mountAndOpen()
    const user = userEvent.setup()
    await user.type(chatInput(), DRAFT)
    await sidebarMoveTo((opts) => router.navigate(opts), WS_B)
    // Workspace B's entry attaches its remembered session; until the replay
    // lands the box is disabled (issue #1853: "while the box is disabled
    // during restore").
    await waitFor(() => {
      if (!socket.sent.some((frame) => frame.type === 'attach_session' && frame.session_id === SESSION_B)) {
        throw new Error('workspace B entry must attach its remembered session')
      }
    })
    await screen.findByTestId('chat-input')
    const input = chatInput()
    expect(input, 'instrument: the box is disabled during the restore').toBeDisabled()
    // Issue requirement: the other chat's message box does not show the draft.
    expect(input, '1853: the other chat\'s disabled box must not show the draft').toHaveValue('')
  })

  it('RED: after Send is enabled again the other chat\'s box does not show the draft', async () => {
    const { socket, emitSessionReplay, router } = await mountAndOpen()
    const user = userEvent.setup()
    await user.type(chatInput(), DRAFT)
    await sidebarMoveTo((opts) => router.navigate(opts), WS_B)
    await waitFor(() => {
      if (!socket.sent.some((frame) => frame.type === 'attach_session' && frame.session_id === SESSION_B)) {
        throw new Error('workspace B entry must attach its remembered session')
      }
    })
    await screen.findByTestId('chat-input')
    emitSessionReplay(SESSION_B)
    await waitFor(() => expect(chatInput(), 'instrument: Send is enabled again after the restore').toBeEnabled())
    // Issue requirement: the draft is gone after recovery with Send enabled.
    expect(chatInput(), '1853: the other chat\'s box must not show the draft after Send re-enables').toHaveValue('')
  })

  it('RED: a Send from the other chat never posts the draft text there', async () => {
    const { socket, emitSessionReplay, router } = await mountAndOpen()
    const user = userEvent.setup()
    await user.type(chatInput(), DRAFT)
    await sidebarMoveTo((opts) => router.navigate(opts), WS_B)
    await waitFor(() => {
      if (!socket.sent.some((frame) => frame.type === 'attach_session' && frame.session_id === SESSION_B)) {
        throw new Error('workspace B entry must attach its remembered session')
      }
    })
    await screen.findByTestId('chat-input')
    emitSessionReplay(SESSION_B)
    await waitFor(() => expect(chatInput()).toBeEnabled())
    // Revision 1 (CHECK F1): the submit path runs under fake timers so the
    // proof is not "not within 50 ms". The clicks and the flush run on the
    // fake clock (fireEvent — the suite's established way to act under fake
    // timers, because @testing-library/react's asyncWrapper drains a faked
    // setTimeout that only `jest` would advance), a SECOND known send (the
    // sentinel) proves the log observed activity after the click, and then
    // every remaining pending timer runs before the whole recorded log is
    // read — a build that merely delays the submit (M5: 250 ms) still fires
    // inside the flush and dies.
    vi.useFakeTimers()
    try {
      const sendButton = () => screen.getByRole('button', { name: /^send/i })
      fireEvent.click(sendButton())
      await act(async () => { await vi.advanceTimersByTimeAsync(SENTINEL_SETTLE_MS) })
      // The sentinel: a second, known send after the checked Send.
      fireEvent.change(chatInput(), { target: { value: MARKER } })
      fireEvent.click(sendButton())
      await act(async () => { await vi.advanceTimersByTimeAsync(QUEUED_SUBMIT_FLUSH_MS) })
      // Instrument control: the sentinel reached workspace B's session.
      // Without it, a log holding nothing could also mean the transport
      // recorded nothing at all.
      const markerFrame = (socket.sent.filter((frame) => frame.type === 'message') as Array<{ session_id: string; content: string }>)
        .find((frame) => frame.session_id === SESSION_B && frame.content.includes(MARKER))
      expect(markerFrame, 'instrument: the sentinel send reached workspace B\'s session after the checked Send').toBeDefined()
      // Issue requirement: NO message frame anywhere in the recorded log —
      // before, up to and including the sentinel, or after the full flush —
      // may carry the draft text into workspace B's session (or anywhere
      // else). With a clean box the checked Send is disabled and nothing at
      // all is sent.
      const draftFrames = socket.sent.filter((frame) => frame.type === 'message')
        .map((frame) => frame as unknown as { content?: string })
        .filter((frame) => (frame.content ?? '').includes(DRAFT))
      expect(draftFrames, '1853: no send may post the draft text (every message frame checked, not only the sentinel\'s)').toEqual([])
    } finally {
      vi.useRealTimers()
    }
  })
})
