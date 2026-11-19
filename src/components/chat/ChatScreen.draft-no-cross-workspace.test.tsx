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
// browser WebSocket class (frame-recording stub below the real parser), with
// the stub playing the gateway's own first frame (server_capabilities — see
// mountAndOpen).
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
// fires inside it).
//
// Revision 3 (CHECK r3 F1): the flush after the sentinel runs ONE HOUR of
// virtual time — the revision brief's option (a) — so a first submit delayed
// by any realistic interval (CHECK's first-submit-only 10-minute mutant, up
// to a full hour) still fires inside the observed window and dies at the
// exact oracle; the previous 121-second horizon let such a submit settle
// unseen after the test ended, and useRealTimers discards scheduled fake
// timers (vitest VitestUtils.useRealTimers docs), so the discarded timer was
// not proof the send never happened. Correction (Revision 4, from the
// red/rev4 observation runs): this paragraph previously credited the
// heartbeat's unanswered-ping force-close and the bounded reconnect schedule
// for keeping the window finite — wrong: the heartbeat interval is created
// when the socket opens, BEFORE useFakeTimers, so it stays a real timer and
// never fires inside the virtual window (the observation logs record zero
// pings after the click). The window is finite for the simpler reason that
// advanceTimersByTimeAsync advances exactly the fixed virtual amount and
// then stops. A submit that stays pending beyond the hour is recorded as the
// proof's stated bound in the RED report, not claimed impossible. An
// unbounded runAllTimersAsync still cannot be used: a self-rescheduling
// timer never drains the queue.
const SENTINEL_SETTLE_MS = 1_000
const QUEUED_SUBMIT_FLUSH_MS = 3_600_000

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
  // The gateway's first frame on every connection (#1428 spec 4.10,
  // contracts/asyncapi.yaml ServerCapabilitiesFrame: "First frame on every
  // connection"). The test socket plays that server part so the #1428 send
  // gate (bound-send.ts::passGate reading ws.ts::WsConnection.sendCapability,
  // decided by ws.ts::classifyFirstFrame from this first frame) opens here as
  // it does against the real gateway — without it every composer send is kept
  // in its draft and no frame ever reaches the socket. Delivered through the
  // raw onmessage (what a real socket delivers), not TestSocket.receive:
  // receive's fixture guard rejects frames the CURRENT head's parser does not
  // know, and this frame is deliberately cross-version — on the release head
  // it is not in the contract yet, the parser drops it as unknown and nothing
  // reads capabilities.
  act(() => {
    socket.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type: 'server_capabilities', send_origin: true }) }))
  })
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
    // sentinel) proves the log observed activity after the click, and then a
    // bounded one-hour virtual window runs every submit the build may have
    // deferred before the recorded log is read (see QUEUED_SUBMIT_FLUSH_MS —
    // Revision 3, CHECK r3 F1).
    // Revision 2: the #1428 head's bound-send gate (bound-send.ts::passGate)
    // holds every send while the connection capability is not `supported`, so
    // the stub now plays the gateway's first frame (mountAndOpen) and the
    // proof additionally covers the outbound queue — a send parked by the
    // gate would otherwise only look like a pass.
    vi.useFakeTimers()
    try {
      const sendButton = () => screen.getByRole('button', { name: /^send/i })
      // Everything the transport records from this instant on sits inside the
      // checked window: the checked Send, the sentinel and the whole flush.
      const sentBeforeCheckedSend = socket.sent.length
      fireEvent.click(sendButton())
      await act(async () => { await vi.advanceTimersByTimeAsync(SENTINEL_SETTLE_MS) })
      // The sentinel: a second, known send after the checked Send.
      fireEvent.change(chatInput(), { target: { value: MARKER } })
      fireEvent.click(sendButton())
      await act(async () => { await vi.advanceTimersByTimeAsync(QUEUED_SUBMIT_FLUSH_MS) })
      // Revision 3 (CHECK r3 F3): the oracle is EXACT. The complete list of
      // message frames recorded after the checked Send click must equal the
      // single expected sentinel record — exactly one frame, compared field
      // by field below. No other message frame, with ANY content, to ANY
      // session, may exist. The revision-2 filter (frames whose content
      // includes the whole draft text) passed a build that sent only a prefix
      // of the draft ("Draft for th") into workspace B; a length-plus-fields
      // oracle cannot.
      //
      // Revision 4 (CHECK r4): that r3 oracle still compared only frames
      // whose type is 'message', so a schema-valid `redirect` frame carrying
      // the whole draft in its `instruction` field sailed through —
      // check/r4/chat-redirect.log records exit 0 while the draft actually
      // left for workspace B's session, and the frame is contract-valid
      // (contracts/components/schemas/RedirectFrame.yaml: the instruction
      // becomes the continued chat's next user message). The oracle is now
      // the COMPLETE outbound frame log: every frame recorded after the
      // checked Send click must be either the single sentinel `message`
      // frame, compared field by field below, or a frame whose type is on
      // the named allow-list. No substring or containment filter anywhere
      // (CHECK r3 scored those as weakening).
      //
      // The named allow-list — outbound frame types that can legitimately
      // happen in this window, cited to its contract:
      // - 'ping': the SPA heartbeat, "sent every 30 seconds by the SPA to
      //   keep the connection alive through proxies" (contracts/asyncapi.yaml
      //   heartbeat ping; generated ws-schemas.ts WsFrameType 'ping'; sent
      //   by src/lib/ws.ts::_startHeartbeat). An allowed ping is asserted
      //   below to carry ONLY its type — its contract shape has no other
      //   field — so nothing, draft or otherwise, can ride inside it.
      // There is no outbound handshake frame to allow: auth rides the HTTP
      // cookie on the WS handshake itself (src/lib/ws.ts onopen, US-5/
      // FR-010 — "no client-sent {type:'auth'} frame is needed or
      // possible"), and the entry's attach_session frames all precede the
      // checked Send, outside the sliced window (red/rev4/
      // chat-observe-rev4.log — the fixed baseline's complete in-window
      // log is exactly the sentinel and nothing else; the heartbeat
      // interval is created before useFakeTimers and stays a real timer,
      // so zero pings actually land in-window today; the allowance exists
      // because the heartbeat is the one frame type the protocol
      // legitimately emits mid-session, and the shape assertion keeps it
      // text-proof).
      const ALLOWED_NON_SENTINEL_FRAME_TYPES = ['ping']
      const framesAfterCheckedSend = socket.sent.slice(sentBeforeCheckedSend)
      const nonSentinelFrames = framesAfterCheckedSend.filter((frame) => {
        const type = (frame as { type?: string }).type ?? ''
        if (!ALLOWED_NON_SENTINEL_FRAME_TYPES.includes(type)) return true
        expect(Object.keys(frame).sort(), '1853: an allowed ping frame must carry only its contract shape (type only) — any extra field could carry the draft').toEqual(['type'])
        return false
      })
      expect(nonSentinelFrames, '1853: exactly one non-heartbeat frame may follow the checked Send — the sentinel; any other frame, of ANY type, with any content, to any session, fails').toHaveLength(1)
      const sentinel = nonSentinelFrames[0] as unknown as {
        type?: string; session_id?: string; content?: string; agent_id?: string
        metadata?: { workspace_id?: string }; client_message_id?: string
      }
      expect(sentinel.type, '1853: the one permitted frame must be a message frame — any other frame type carrying user text toward chat B fails').toBe('message')
      expect(sentinel.session_id, '1853: the one permitted send must target workspace B\'s session').toBe(SESSION_B)
      expect(sentinel.content, '1853: the one permitted send must carry exactly the sentinel text — none of the draft may ride along, prefix or otherwise').toBe(MARKER)
      expect(sentinel.agent_id, '1853: the one permitted send must keep the session\'s agent').toBe('mia')
      expect(sentinel.metadata?.workspace_id, '1853: the one permitted send must carry workspace B\'s id').toBe(WS_B)
      expect(sentinel.client_message_id, '1853: the one permitted send must be a fresh message carrying its own client id').toEqual(expect.any(String))
      // Contract fields only one head adds (e.g. #1428's `origin`) are
      // tolerated; the count, the target and every content field above are
      // exact.
      // Revision 2 addendum, exact since Revision 3 (CHECK r3 F3): NO queued
      // item may exist at all — nothing queued, with any content. On #1428 a
      // send parked by the capability gate waits in `outboundQueue`
      // (enqueueOutboundMessage; drain moves items through
      // `pendingDrainQueue`) — a parked draft is not "never sent", it goes
      // out the moment the gate opens; a delayed submit that lands after the
      // flush horizon (connection down) parks here too. Both fields and the
      // item shape (string | QueuedOutboundMessage with .content) are
      // identical on the release head and on #1428, so one assertion serves
      // both heads.
      const queuedTexts = [...useChatStore.getState().outboundQueue, ...useChatStore.getState().pendingDrainQueue]
        .map((item) => (typeof item === 'string' ? item : item.content))
      expect(queuedTexts, '1853: nothing may sit in either outbound queue — a parked or late-arriving draft is still a cross-chat send').toEqual([])
    } finally {
      vi.useRealTimers()
    }
  })
})
