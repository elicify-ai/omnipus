// T39 / architect S / approved spec78b7f593 D16, B52–B56, D-P3/D-U2.
// D16 canonical/generated be95728784 is normal-merged. All REST/WS inputs
// use those contracts; no placeholder, wire cast, invented counter or skip.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import type { Message, Session, SessionPage, SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { SessionDetail as DetailSchema, SessionHistoryResponse as PageSchema } from '@/lib/api/generated/schemas'
import { DoneStats as StatsSchema, SessionStateFrame as StateSchema, ToolCallResultFrame as ResultSchema } from '@/lib/api/generated/ws-schemas'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useConnectionStore } from '@/store/connection'
import { AssistantMessage, UserMessage, VirtualAssistantMessageRow } from '@/components/chat/ChatScreen'
import { SystemMessage } from '@/components/chat/SystemMessageRows'
import { VirtualizedMessageList } from '@/components/chat/ChatMessageList'
import { TokenCounter } from '@/components/chat/composer/TokenCounter'
import { BashOutputUI } from '@/components/chat/tools/BashOutput'
import { MAIN_ID, WS, TestSocket, json } from '../../tests/l1-room-ui/harness'
import { MAIN_PATH, mountStage1App, resetStage1App, Stage1Network, stopStage1App } from '../../tests/l1-room-ui/stage1-harness'
import { availableDetail, deferred, entry, expectValidPage, page, pos, replay, session, snapshot, STAMP } from '../../tests/chat-history-paging/fixtures'
import { sendFrames, sentMessages } from '../../tests/chat-history-paging/application'
import { historyTransport } from '../../tests/chat-history-paging/transport'

// Frozen contracted display evidence, NOT a native-engine/durable-write proof.
// Three narration sources plus three logical tool rows => D4 row_count6.
const sources: Message[] = [
  entry(301, { id: 'saved-completed', role: 'assistant', turn_id: 'completed-turn', content: 'Completed saved reply.', tool_calls: [
    { id: 'completed-call', tool: 'bash', status: 'success', parameters: { command: 'printf completed-control' }, result: { stdout: 'completed-control' }, duration_ms: 17 },
  ] }),
  entry(302, { id: 'saved-cancelled', role: 'assistant', turn_id: 'cancelled-turn', content: 'Interrupted saved reply.', tool_calls: [
    { id: 'cancelled-call', tool: 'bash', status: 'cancelled', parameters: { command: 'printf cancellation-fixture' }, duration_ms: 731 },
  ] }),
  entry(303, { id: 'saved-pending', role: 'assistant', turn_id: 'different-pending-turn', content: 'A separate unstarted request.', tool_calls: [
    { id: 'pending-call', tool: 'bash', status: 'pending', parameters: { command: 'printf must-not-execute' } },
  ] }),
]
const frozenPage = page(sources, { sessionId: MAIN_ID, rows: 6 })
let network: Stage1Network
let transport: ReturnType<typeof historyTransport> | undefined

beforeEach(() => {
  resetStage1App()
  __resetFinishedTurnIdsForTests() // each case is a new browser lifetime, not a reconnect
  network = new Stage1Network()
})
afterEach(async () => {
  transport?.connection.disconnect()
  transport = undefined
  await stopStage1App()
  expect(network.unexpected, 'every HTTP request is declared at the network edge').toStrictEqual([])
})

function expectFrozenCalls() {
  const bucket = useChatStore.getState().sessionsById[MAIN_ID]
  expect(bucket.messageOrder, 'three original source IDs, not duplicated or synthesized cancellation rows').toStrictEqual(['saved-completed', 'saved-cancelled', 'saved-pending'])
  expect(bucket.history.readerPositions).toStrictEqual([pos(301), pos(302), pos(303)])
  expect(bucket.messagesById['saved-completed']).toMatchObject({ id: 'saved-completed', turnId: 'completed-turn', agentId: 'mia', tool_calls: [
    { id: 'completed-call', tool: 'bash', status: 'success', params: { command: 'printf completed-control' }, result: { stdout: 'completed-control' }, duration_ms: 17 },
  ] })
  expect(bucket.messagesById['saved-cancelled']).toMatchObject({ id: 'saved-cancelled', turnId: 'cancelled-turn', agentId: 'mia', tool_calls: [
    { id: 'cancelled-call', tool: 'bash', status: 'cancelled', params: { command: 'printf cancellation-fixture' }, duration_ms: 731 },
  ] })
  expect(bucket.messagesById['saved-cancelled'].tool_calls?.[0].result, 'missing cancelled output is not fabricated as success').toBeUndefined()
  // Pre-RED oracle correction, team-lead Q1=B: wire pending is not an
  // internal-status literal. Keep its wire fixture/identity/arguments exact;
  // assert no fabricated result, visible cancellation or execution instead.
  expect(bucket.messagesById['saved-pending']).toMatchObject({ id: 'saved-pending', turnId: 'different-pending-turn', tool_calls: [
    { id: 'pending-call', tool: 'bash', params: { command: 'printf must-not-execute' } },
  ] })
  expect(bucket.messagesById['saved-pending'].tool_calls?.[0].result, 'an unstarted call gains no fabricated output').toBeUndefined()
}

async function loadSavedPage() {
  expectValidPage(frozenPage, [pos(301), pos(302), pos(303)], 6)
  expect(PageSchema.safeParse(frozenPage).success, 'positive control: existing strict REST schema supports cancelled ToolCall').toBe(true)
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(frozenPage))
  await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID) })
  expect(useChatStore.getState().history.historyLoaded, 'actual adapter/history owner accepted the page').toBe(true)
  expectFrozenCalls()
}

type Surface = 'normal' | 'virtual-row' | 'plain'
function RealRows({ surface }: { surface: Surface }) {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    {/* Same real tool registration as OmnipusRuntimeProvider, not a fallback stand-in. */}
    <BashOutputUI />
    {surface === 'normal'
      ? <ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage, SystemMessage }} />
      : surface === 'plain'
        ? <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
        : messages.map(message => <VirtualAssistantMessageRow key={message.id} message={message} liteMode={false} delegationEvents={[]} pairedReplyToIds={new Set()} />)}
    <TokenCounter />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}

async function showRows(surface: Surface, hasPendingFixture = true) {
  if (surface === 'plain') vi.stubGlobal('ResizeObserver', undefined)
  render(<QueryClientProvider client={queryClient}><RealRows surface={surface} /></QueryClientProvider>)
  await screen.findByText('Completed saved reply.', { exact: true })
  expect(screen.getByText('Interrupted saved reply.', { exact: true })).toBeVisible()
  // AssistantUI registers its real tool UI asynchronously; prose alone is
  // not a tool-render barrier. Use the actual expected label, no sleep/timeout.
  await screen.findByText('Cancelled', { exact: true })
  if (hasPendingFixture) expectPendingNotExecuted()
}

function expectPendingNotExecuted() {
  const pendingRows = screen.getAllByTestId('assistant-message').filter(row => row.getAttribute('data-message-id') === 'saved-pending')
  expect(pendingRows, 'pending owner remains one original visible row').toHaveLength(1)
  expect(within(pendingRows[0]).queryByText('Cancelled', { exact: true }), 'the unrelated unstarted call is not visually cancelled').not.toBeInTheDocument()
  expect(within(pendingRows[0]).queryByRole('region', { name: 'Tool output' }), 'unstarted call has no result row').not.toBeInTheDocument()
  expect(TestSocket.instances, 'rendering history opens no execution WebSocket').toStrictEqual([])
  expect(network.requests.filter(request => request.method !== 'GET'), 'rendering history invokes no tool/input endpoint').toStrictEqual([])
}

describe.each(['normal', 'virtual-row', 'plain'] as const)('T39 real generated REST -> history owner -> %s renderer', surface => {
  it('retains original cancelled/completed/pending identities and one exact Cancelled label, without executing anything', async () => {
    await loadSavedPage()
    await showRows(surface)
    expect(screen.queryAllByText('Cancelled', { exact: true }), 'D16 confirmed cancellation remains a visible tool outcome, not generic error or interruption alone').toHaveLength(1)
    expectFrozenCalls()
    expect(network.requests.filter(request => request.method !== 'GET'), 'reload/render never invokes a tool or sends input').toStrictEqual([])
  })

  it('reloading the same legal page does not duplicate or reclassify tool rows, including the unrelated unstarted call', async () => {
    await loadSavedPage()
    await showRows(surface)
    await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID) })
    expectFrozenCalls()
    expectPendingNotExecuted()
    expect(screen.queryAllByText('Cancelled', { exact: true })).toHaveLength(1)
    expect(network.requests.filter(request => request.path === `/api/v1/sessions/${MAIN_ID}/messages`)).toHaveLength(2)
    expect(network.requests.filter(request => request.method !== 'GET')).toStrictEqual([])
  })
})

async function openRecordedUsage(route: 'workspace' | 'standalone', total: number) {
  const empty = page([], { sessionId: MAIN_ID })
  const known = session(MAIN_ID)
  // Only navigation metadata is prepared; no local token counter is seeded.
  const identity: Session = { ...known, type: route === 'workspace' ? 'main' : 'chat', title: 'Recorded usage fixture',
    stats: { ...known.stats!, tokens_total: total } }
  if (route === 'standalone') delete identity.workspace_id
  const detail = availableDetail(empty, identity)
  expect(DetailSchema.safeParse(detail).success, 'existing legal SessionDetail carries recorded SessionStats.tokens_total').toBe(true)
  const list = deferred<Response>()
  // Sidebar and automatic entry each make a real request. A Response body
  // is one-shot: share the barrier, never the already-consumed response body.
  network.respond('GET', '/api/v1/sessions', () => list.promise.then(response => response.clone()))
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}`, () => json(detail))
  network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(empty))
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.getState().setWorkspaceSessionDescriptor(WS, { id: MAIN_ID, type: route === 'workspace' ? 'main' : 'chat', title: identity.title ?? null, agentId: 'mia' })
  })
  const mounted = await mountStage1App(route === 'workspace' ? MAIN_PATH : `/sessions/${MAIN_ID}`, { initialAttachResponse: 'caller' })
  // Release the automatic entry's real metadata read only once the actual
  // socket is open. No member click/useSelectSession seed masks the cold path.
  expect(document.hidden, 'fixture: automatic cold entry is a foreground view').toBe(false)
  expect(useConnectionStore.getState().connection?.isConnected, 'fixture: actual WsConnection is open before resolving entry metadata').toBe(true)
  const sessionPage: SessionPage = { sessions: [identity] }
  await act(async () => list.resolve(json(sessionPage)))
  await waitFor(() => expect(mounted.socket.sent.some(frame => frame.type === 'attach_session' && frame.session_id === MAIN_ID), JSON.stringify({ fixture: 'automatic entry attach', route, active: useSessionStore.getState().activeSessionId, entry: useSessionStore.getState().workspaceEntry, workspace: useWorkspacesStore.getState().activeWorkspaceId, connected: useConnectionStore.getState().isConnected, connectionOpen: useConnectionStore.getState().connection?.isConnected, connectionError: useConnectionStore.getState().connectionError, documentHidden: document.hidden, socketReady: mounted.socket.readyState, sent: mounted.socket.sent, requests: network.requests.map(request => request.path) })).toBe(true))
  await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID))
  await sendFrames(mounted.socket, emptyAttachFrames(MAIN_ID, total))
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID].cursor).toStrictEqual({ bootId: 'history-boot', seq: 100 }))
  const counter = await screen.findByTestId('session-token-counter')
  expect(network.requests.some(request => request.path === '/api/v1/sessions'), 'positive control: real automatic route read recorded session metadata').toBe(true)
  if (route === 'standalone') expect(network.requests.some(request => request.path === `/api/v1/sessions/${MAIN_ID}`), 'standalone loader used the actual generated detail adapter').toBe(true)
  expect(sentMessages(mounted.socket)).toStrictEqual([])
  return { mounted, counter }
}

describe.each(['workspace', 'standalone'] as const)('T39 fresh %s entry -> actual token footer', route => {
  it('hydrates the recorded46323 session total independently of an empty selected page, not last-turn45123 or zero', async () => {
    // D-U2 provider-accounted synthetic oracle:1200 +45123 =46323. These
    // exact values are specified, not guessed from the rounded UAT screenshot.
    const { counter } = await openRecordedUsage(route, 46323)
    expect.soft(useChatStore.getState().sessionTokens, 'D16 existing recorded session authority hydrates the fresh store').toBe(46323)
    expect(counter, 'T39 raw accessible footer value, independent of display rounding').toHaveAttribute('aria-label', '46323 tokens used')
  })

  it('CONTROL: an actually unused recorded session keeps the exact zero counter', async () => {
    const { counter } = await openRecordedUsage(route, 0)
    expect(useChatStore.getState().sessionTokens).toBe(0)
    expect(counter).toHaveAttribute('aria-label', '0 tokens used')
    expect(screen.getByTestId('session-token-value')).toHaveTextContent(/^0 tokens$/)
  })
})

function currentState(sid: string, total?: number): Extract<ServerFrame, { type: 'session_state' }> {
  return { type: 'session_state', session_id: sid, user_id: 'reload-reader', emitted_at: STAMP, pending_approvals: [], history_state: { status: 'available' }, ...(total === undefined ? {} : { session_tokens_total: total }) }
}
function emptyAttachFrames(sid: string, total: number): ServerFrame[] {
  return [snapshot(page([], { sessionId: sid })), currentState(sid, total),
    { type: 'done', session_id: sid, stats: { frames_emitted: 0 } },
    { type: 'catch_up_complete', session_id: sid, boot_id: 'history-boot', seq: 100, mode: 'snapshot' }]
}
function accountedDone(sid: string, seq: number, delta: number, absolute: number): ServerFrame {
  return { type: 'done', session_id: sid, turn_id: `accounted-turn-${seq}`, seq, stats: { tokens: delta, session_tokens_total: absolute } }
}
function startUsageTransport(total: number) {
  transport = historyTransport()
  transport.attach(MAIN_ID)
  transport.deliver(...emptyAttachFrames(MAIN_ID, total))
  render(<TokenCounter />)
  return transport
}
function expectCounter(total: number) {
  // Accumulate genuine state/DOM failures without hiding them, so one missing
  // hydration does not prevent the later race and next-turn assertions running.
  expect.soft(useChatStore.getState().sessionTokens, 'D16 exact authoritative session total').toBe(total)
  expect.soft(screen.getByTestId('session-token-counter'), 'actual footer raw value, not a formatting helper').toHaveAttribute('aria-label', `${total} tokens used`)
}

describe.each(['normal', 'virtual-row', 'plain'] as const)('T39 generated cancelled WS snapshot -> %s renderer', surface => {
  it('bakes the original cancelled status/identity beside a completed call, not a generic error or lost row', async () => {
    transport = historyTransport()
    transport.attach(MAIN_ID)
    const selected = sources.slice(0, 2)
    const packet = page(selected, { sessionId: MAIN_ID, rows: 4 })
    const frames: ServerFrame[] = [snapshot(packet), currentState(MAIN_ID, 46323)]
    for (const source of selected) {
      const call = source.tool_calls![0]
      const origin = { position: source.history_position!, entry_id: source.id }
      frames.push(replay(source, MAIN_ID),
        { type: 'tool_call_start', session_id: MAIN_ID, tool: call.tool, call_id: call.id, params: call.parameters ?? {}, agent_id: 'mia', history_origin: origin },
        { type: 'tool_call_result', session_id: MAIN_ID, tool: call.tool, call_id: call.id, status: call.status === 'cancelled' ? 'cancelled' : 'success', result: call.result ?? null, duration_ms: call.duration_ms, agent_id: 'mia', history_origin: origin })
    }
    frames.push({ type: 'done', session_id: MAIN_ID, stats: { frames_emitted: 6 } },
      { type: 'catch_up_complete', session_id: MAIN_ID, boot_id: 'history-boot', seq: 100, mode: 'snapshot' })
    transport.deliver(...frames)
    const bucket = useChatStore.getState().sessionsById[MAIN_ID]
    expect(bucket.messageOrder).toStrictEqual(['saved-completed', 'saved-cancelled'])
    expect.soft(bucket.messagesById['saved-cancelled'].tool_calls, 'D16 new cancelled enum is not clamped to error').toMatchObject([{ id: 'cancelled-call', tool: 'bash', status: 'cancelled', params: { command: 'printf cancellation-fixture' }, result: null, duration_ms: 731 }])
    expect(bucket.messagesById['saved-completed'].tool_calls).toMatchObject([{ id: 'completed-call', status: 'success', result: { stdout: 'completed-control' } }])
    await showRows(surface, false)
    expect(screen.queryAllByText('Cancelled', { exact: true })).toHaveLength(1)
    expect(transport.socket.sent.filter(frame => frame.type === 'message' || frame.type === 'cancel')).toStrictEqual([])
    expect(network.requests.filter(request => request.method !== 'GET')).toStrictEqual([])
  })
})

describe('T39 generated absolute carriers and real counter ownership', () => {
  it('accepts zero/positive/omitted absolute values, rejects null/negative/fraction/string in all three generated carriers', () => {
    const empty = page([], { sessionId: MAIN_ID })
    for (const total of [0, 46323]) {
      expect(PageSchema.safeParse({ ...empty, session_tokens_total: total }).success).toBe(true)
      expect(StateSchema.safeParse(currentState(MAIN_ID, total)).success).toBe(true)
      expect(StatsSchema.safeParse({ session_tokens_total: total }).success).toBe(true)
    }
    expect(PageSchema.safeParse(empty).success).toBe(true)
    expect(StateSchema.safeParse(currentState(MAIN_ID)).success).toBe(true)
    expect(StatsSchema.safeParse({ frames_emitted: 9999 }).success).toBe(true)
    for (const invalid of [null, -1, 0.5, '46323']) {
      expect(PageSchema.safeParse({ ...empty, session_tokens_total: invalid }).success, `REST rejects ${JSON.stringify(invalid)}`).toBe(false)
      expect(StateSchema.safeParse({ ...currentState(MAIN_ID), session_tokens_total: invalid }).success, `state rejects ${JSON.stringify(invalid)}`).toBe(false)
      expect(StatsSchema.safeParse({ session_tokens_total: invalid }).success, `done stats reject ${JSON.stringify(invalid)}`).toBe(false)
    }
    const cancelled = { type: 'tool_call_result', session_id: MAIN_ID, tool: 'bash', call_id: 'cancelled-call', result: null, status: 'cancelled' }
    expect(ResultSchema.safeParse(cancelled).success).toBe(true)
    expect(ResultSchema.safeParse({ ...cancelled, status: 'not-an-outcome' }).success).toBe(false)
  })

  it('hydrates from a real REST absolute total once; an omitted or stale later page cannot erase/decrease it', async () => {
    let response: SessionHistoryResponse = { ...frozenPage, session_tokens_total: 46323 }
    network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(response))
    render(<TokenCounter />)
    await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID) })
    expectFrozenCalls()
    expectCounter(46323)
    await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID) })
    expectCounter(46323)
    response = frozenPage // unknown/omitted is not a new zero observation
    await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID) })
    expectCounter(46323)
    response = { ...frozenPage, session_tokens_total: 1200 }
    await act(async () => { await useChatStore.getState().loadLatestHistory(MAIN_ID) })
    expectCounter(46323)
  })

  it('reconciles held live done/absolute state, duplicate and replay diagnostics before a stale page; next700 advances46323 to47023 only once', async () => {
    const current = startUsageTransport(1200)
    expectCounter(1200)
    const stale = deferred<Response>()
    network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => stale.promise)
    const reading = useChatStore.getState().loadLatestHistory(MAIN_ID)
    const completed = accountedDone(MAIN_ID, 101, 45123, 46323)
    current.deliver(completed, completed, currentState(MAIN_ID, 46323),
      { type: 'done', session_id: MAIN_ID, stats: { frames_emitted: 9999 } })
    expectCounter(46323)
    await act(async () => { stale.resolve(json({ ...page([], { sessionId: MAIN_ID }), session_tokens_total: 1200 })); await reading })
    expectCounter(46323)
    current.deliver(currentState(MAIN_ID))
    expectCounter(46323)
    const next = accountedDone(MAIN_ID, 102, 700, 47023)
    current.deliver(next, next)
    expectCounter(47023)
    expect(current.socket.sent.filter(frame => frame.type === 'message')).toStrictEqual([])
  })

  it('fences an old A-view page after A→B→A and never routes A absolute usage into the selected genuine-zero B', async () => {
    const current = startUsageTransport(46323)
    expectCounter(46323)
    const stale = deferred<Response>()
    network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => stale.promise)
    const oldA = useChatStore.getState().loadLatestHistory(MAIN_ID)
    const other = 'usage-independent-zero-session'
    act(() => {
      useSessionStore.getState().setActiveSession(other, 'mia')
      useChatStore.getState().releaseSessionHistory(MAIN_ID)
    })
    current.attach(other)
    current.deliver(...emptyAttachFrames(other, 0))
    expectCounter(0)
    current.deliver(accountedDone(MAIN_ID, 101, 700, 47023))
    expectCounter(0)
    expect.soft(useChatStore.getState().sessionsById[MAIN_ID].sessionTokens, 'background A updates only its own recorded total').toBe(47023)
    act(() => useSessionStore.getState().setActiveSession(MAIN_ID, 'mia'))
    current.attach(MAIN_ID)
    current.deliver(...emptyAttachFrames(MAIN_ID, 47023))
    expectCounter(47023)
    await act(async () => { stale.resolve(json({ ...page([], { sessionId: MAIN_ID }), session_tokens_total: 1200 })); await oldA })
    expectCounter(47023)
    expect(useChatStore.getState().sessionsById[other].sessionTokens).toBe(0)
    expect(current.socket.sent.filter(frame => frame.type === 'message')).toStrictEqual([])
  })
})
