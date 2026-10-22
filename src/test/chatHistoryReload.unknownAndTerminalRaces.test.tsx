// Founder supplemental T39 pack; spec D16/B54/B56/D-P3/D-U2.
// Real generated adapters/history/store/runtime/native tool UI/footer. Only
// network/browser edges doubled; controls are not called mutation-proven here.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import type { Message, SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { SessionStateFrame as StateSchema } from '@/lib/api/generated/ws-schemas'
import { useChatStore, getMessages } from '@/store/chat'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { useSessionStore } from '@/store/session'
import { queryClient } from '@/lib/queryClient'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { AssistantMessage, UserMessage, VirtualAssistantMessageRow } from '@/components/chat/ChatScreen'
import { SystemMessage, VirtualSystemMessageRow } from '@/components/chat/SystemMessageRows'
import { VirtualizedMessageList } from '@/components/chat/ChatMessageList'
import { BashOutputUI } from '@/components/chat/tools/BashOutput'
import { TokenCounter } from '@/components/chat/composer/TokenCounter'
import { Stage1Network, resetStage1App, stopStage1App } from '../../tests/l1-room-ui/stage1-harness'
import { json } from '../../tests/l1-room-ui/harness'
import { deferred, entry, expectValidPage, page, pos, replay, snapshot, STAMP } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

const SID = 'supplemental-terminal-session'
const TURN = 'older-running-turn'
const OWNER = 'older-tool-owner'
const CTX = 'Newer retained system context'
const BODY = 'Tools with distinct recorded outcomes.'
const CANCELLED_COMMAND = 'printf cancelled-call'
const COMPLETED_COMMAND = 'printf completed-control'
const PENDING_COMMAND = 'printf pending-control'
const LATE_RESULT = { stdout: 'LATE_SUCCESS_MUST_NOT_WIN' }
const COMPLETED_RESULT = { stdout: 'completed-control' }
const SURFACES = ['normal', 'virtual-row', 'plain'] as const
// not-wire-format: renderer selection, never serialized.
type Surface = typeof SURFACES[number]
let network: Stage1Network
let transport: ReturnType<typeof historyTransport> | undefined

beforeEach(() => {
  resetStage1App()
  __resetFinishedTurnIdsForTests()
  network = new Stage1Network()
})
afterEach(async () => {
  transport?.connection.disconnect(); transport = undefined
  await act(async () => useSessionStore.getState().setActiveSession(null, 'mia'))
  await stopStage1App()
  expect(network.unexpected, 'all requests remain declared process-edge data').toStrictEqual([])
})

function socket() { if (!transport) transport = historyTransport(); return transport }
function bucket(sid = SID) { return useChatStore.getState().sessionsById[sid] }
function state(sid?: string, total?: number): Extract<ServerFrame, { type: 'session_state' }> {
  return { type: 'session_state', user_id: 'supplemental-reader', pending_approvals: [], emitted_at: STAMP, ...(sid ? { session_id: sid, history_state: { status: 'available' } } : {}), ...(total === undefined ? {} : { session_tokens_total: total }) }
}
function attachUsage(sid: string, total?: number) {
  const current = socket()
  act(() => useSessionStore.getState().setActiveSession(sid, 'mia'))
  current.attach(sid)
  const empty = page([], { sessionId: sid })
  current.deliver(snapshot(empty), state(sid, total), { type: 'done', session_id: sid, stats: { frames_emitted: 0 } }, { type: 'catch_up_complete', session_id: sid, boot_id: 'history-boot', seq: 100, mode: 'snapshot' })
}
function expectUnknown() {
  const counter = screen.getByTestId('session-token-counter')
  expect(counter, 'explicit unknown state, not a numeric default').toHaveAccessibleName(/(?:unknown|unavailable)/i)
  expect(screen.getByTestId('session-token-value')).toHaveTextContent(/(?:unknown|unavailable)/i)
  expect(counter).not.toHaveAttribute('aria-label', '0 tokens used')
  expect(counter).not.toHaveAttribute('aria-label', '46323 tokens used')
}
function expectTotal(total: number) {
  expect(screen.getByTestId('session-token-counter')).toHaveAttribute('aria-label', `${total} tokens used`)
}

describe('T39 supplemental — legal cold-unknown usage carriers', () => {
  it('cold omitted state/page/replay evidence shows unknown, distinct from a real recorded zero', async () => {
    attachUsage('cold-unknown')
    render(<TokenCounter />)
    expectUnknown()
    network.respond('GET', '/api/v1/sessions/cold-unknown/messages', () => json(page([], { sessionId: 'cold-unknown' })))
    await act(async () => { await useChatStore.getState().loadLatestHistory('cold-unknown') })
    socket().deliver({ type: 'done', session_id: 'cold-unknown', stats: { frames_emitted: 987654 } })
    expectUnknown()
    attachUsage('independent-recorded-zero', 0)
    expectTotal(0)
    expect(screen.getByTestId('session-token-value')).toHaveTextContent(/^0 tokens$/)
    expect(socket().socket.sent.filter(frame => frame.type === 'message')).toStrictEqual([])
  })

  it('A known46323 is not inherited by cold B; A survives later omissions and independent C remains true zero', async () => {
    attachUsage('known-A', 46323)
    render(<TokenCounter />)
    expectTotal(46323)
    attachUsage('unknown-B')
    expectUnknown()
    expect(bucket('known-A').sessionTokens).toBe(46323)
    attachUsage('known-A')
    network.respond('GET', '/api/v1/sessions/known-A/messages', () => json(page([], { sessionId: 'known-A' })))
    await act(async () => { await useChatStore.getState().loadLatestHistory('known-A') })
    socket().deliver(state('known-A'), { type: 'done', session_id: 'known-A', stats: { frames_emitted: 987654 } })
    expectTotal(46323)
    attachUsage('recorded-zero-C', 0)
    expectTotal(0)
    expect(bucket('known-A').sessionTokens).toBe(46323)
    expect(socket().socket.sent.filter(frame => frame.type === 'message')).toStrictEqual([])
  })

  it('a generated-valid unscoped absolute cannot seed an unknown or overwrite a known selected session', () => {
    attachUsage('unscoped-unknown')
    render(<TokenCounter />)
    const unscoped = state(undefined, 999999)
    expect(StateSchema.parse(unscoped).session_tokens_total, 'instrument: parser retains the adversarial scalar, so ignore is not a dropped-input false green').toBe(999999)
    socket().deliver(unscoped)
    expectUnknown()
    attachUsage('unscoped-known', 46323)
    socket().deliver(unscoped)
    expectTotal(46323)
    socket().deliver(state('addressed-background', 1200))
    expectTotal(46323)
    expect(bucket('addressed-background').sessionTokens).toBe(1200)
    expect(bucket('unscoped-known').sessionTokens).toBe(46323)
  })
})

function historicalFrames(entries: Message[]): ServerFrame[] {
  const frames: ServerFrame[] = []
  for (const source of entries) {
    frames.push({ ...replay(source, SID), ...(source.truncated === undefined ? {} : { truncated: source.truncated }), ...(source.truncation_reason ? { truncation_reason: source.truncation_reason } : {}) })
    for (const call of source.tool_calls ?? []) {
      const history_origin = { position: source.history_position!, entry_id: source.id }
      frames.push({ type: 'tool_call_start', session_id: SID, tool: call.tool, call_id: call.id, params: call.parameters ?? {}, history_origin })
      if (call.status === 'success' || call.status === 'error' || call.status === 'cancelled') frames.push({ type: 'tool_call_result', session_id: SID, tool: call.tool, call_id: call.id, status: call.status, result: call.result ?? null, duration_ms: call.duration_ms, history_origin })
    }
  }
  return frames
}
function wirePage(entries: Message[], rows: number, options: { initial?: boolean; older?: boolean } = {}): SessionHistoryResponse {
  const body = page(entries, { sessionId: SID, rows, earlier: options.initial, later: options.older, info: { mode: options.older ? 'older' : 'latest', limit: options.initial || options.older ? 20 : 40, min_rows: 1, ...(options.initial ? { stop_reason: 'byte_budget' } : {}) } })
  const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...info } = body.page_info
  void projected_bytes; void soft_byte_budget; void byte_budget_exceeded; void budget_override
  body.page_info.projected_bytes = Math.max(Buffer.byteLength(JSON.stringify({ ...body, page_info: info })), Buffer.byteLength(JSON.stringify([{ ...snapshot(body), page_info: info }, ...historicalFrames(entries)])))
  if (options.initial) {
    // A valid one-group byte-limited page, NOT an underfilled20-row target.
    // Positive advertised budgets are not a hardcoded4MiB enum (D4a/D10).
    body.page_info.soft_byte_budget = body.page_info.projected_bytes - 1
    body.page_info.byte_budget_exceeded = true
    body.page_info.budget_override = 'atomic_group'
    body.page_info.stop_reason = 'byte_budget'
  }
  expectValidPage(body, entries.map(source => source.history_position!), rows)
  return body
}

function beginTools() {
  const current = socket()
  act(() => useSessionStore.getState().setActiveSession(SID, 'mia'))
  expect(current.connection.send({ type: 'attach_session', session_id: SID, history_limit: 20, history_min_rows: 1 })).toBe(true)
  const context = entry(5000, { id: 'newer-context', role: 'system', content: CTX })
  const head = wirePage([context], 1, { initial: true })
  current.deliver(snapshot(head), { ...state(SID), active_turn: { turn_id: TURN, agent_id: 'mia', started_at: STAMP } }, replay(context, SID), { type: 'done', session_id: SID, stats: { frames_emitted: 1 } },
    // Current control projection of an older, still-running owning source;
    // it is not charged as the selected newer historical body (D-P3).
    { type: 'token', session_id: SID, message_id: OWNER, turn_id: TURN, agent_id: 'mia', content: BODY },
    { type: 'tool_call_start', session_id: SID, tool: 'bash', call_id: 'cancelled-call', params: { command: CANCELLED_COMMAND }, agent_id: 'mia' },
    { type: 'tool_call_start', session_id: SID, tool: 'bash', call_id: 'completed-call', params: { command: COMPLETED_COMMAND }, agent_id: 'mia' },
    { type: 'tool_call_result', session_id: SID, tool: 'bash', call_id: 'completed-call', status: 'success', result: COMPLETED_RESULT, duration_ms: 17 },
    { type: 'catch_up_complete', session_id: SID, boot_id: 'history-boot', seq: 100, mode: 'snapshot' })
  expect(bucket().toolCalls['cancelled-call']).toMatchObject({ id: 'cancelled-call', status: 'running', params: { command: CANCELLED_COMMAND } })
  expect(bucket().toolCalls['completed-call']).toMatchObject({ status: 'success', result: COMPLETED_RESULT })
  return current
}
function confirmCancelled() {
  socket().deliver({ type: 'tool_call_result', session_id: SID, tool: 'bash', call_id: 'cancelled-call', status: 'cancelled', result: null, duration_ms: 731, seq: 101 })
}
function bakeCancelled() {
  socket().deliver({ type: 'done', session_id: SID, message_id: OWNER, turn_id: TURN, seq: 102, stats: { truncated: true, truncation_reason: 'cancelled' } })
}
function terminalCall(id: string) {
  return bucket().toolCalls[id] ?? Object.values(bucket().messagesById).flatMap(row => row.tool_calls ?? []).find(call => call.id === id)
}
function expectCancellationTruth() {
  expect(terminalCall('cancelled-call')).toMatchObject({ id: 'cancelled-call', tool: 'bash', status: 'cancelled', params: { command: CANCELLED_COMMAND }, result: null, duration_ms: 731 })
  expect(terminalCall('completed-call')).toMatchObject({ id: 'completed-call', status: 'success', params: { command: COMPLETED_COMMAND }, result: COMPLETED_RESULT, duration_ms: 17 })
}
function RealRows({ surface }: { surface: Surface }) {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(state => state.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root><BashOutputUI />
    {surface === 'normal' ? <ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage, SystemMessage }} />
      : surface === 'plain' ? <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
      : messages.map(message => message.role === 'system' ? <VirtualSystemMessageRow key={message.id} message={message} /> : <VirtualAssistantMessageRow key={message.id} message={message} liteMode={false} delegationEvents={[]} pairedReplyToIds={new Set()} />)}
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
async function show(surface: Surface) {
  if (surface === 'plain') vi.stubGlobal('ResizeObserver', undefined)
  render(<QueryClientProvider client={queryClient}><RealRows surface={surface} /></QueryClientProvider>)
  await screen.findByText(BODY, { exact: true })
}
async function toggle(command: string) {
  const controls = (await screen.findAllByTestId('bash-output-toggle')).filter(control => control.textContent?.includes(command))
  expect(controls, 'positive visible native tool row; absence is not a negative-status pass').toHaveLength(1)
  expect(controls[0]).toBeVisible()
  expect(controls[0]).toHaveAttribute('data-tool', 'bash')
  return controls[0]
}
function noExecution() {
  expect(socket().socket.sent.filter(frame => frame.type === 'message' || frame.type === 'cancel')).toStrictEqual([])
  expect(network.requests.filter(request => request.method !== 'GET')).toStrictEqual([])
}

describe.each(SURFACES)('T39 supplemental %s — confirmed terminal cancellation', surface => {
  it.each(['live', 'baked'] as const)('%s call ignores late success without changing original result/duration or completed companion', async phase => {
    beginTools(); confirmCancelled(); expectCancellationTruth()
    if (phase === 'baked') {
      bakeCancelled()
      expect(bucket().toolCalls['cancelled-call'], 'instrument: terminal fact is baked, not still protected only in the live map').toBeUndefined()
      expect(Object.values(bucket().messagesById).flatMap(row => row.tool_calls ?? []).find(call => call.id === 'cancelled-call')).toMatchObject({ status: 'cancelled', result: null })
    } else {
      expect(bucket().toolCalls['cancelled-call']).toMatchObject({ status: 'cancelled', result: null })
    }
    expectCancellationTruth()
    await show(surface)
    expect(await toggle(CANCELLED_COMMAND)).toHaveTextContent('Cancelled')
    socket().deliver({ type: 'tool_call_result', session_id: SID, tool: 'bash', call_id: 'cancelled-call', status: 'success', result: LATE_RESULT, duration_ms: 999, seq: phase === 'live' ? 102 : 103 })
    expect(socket().frames.at(-1), 'instrument: legal late success reached the real reducer, not a parser drop').toMatchObject({ type: 'tool_call_result', call_id: 'cancelled-call', status: 'success', result: LATE_RESULT })
    expectCancellationTruth()
    expect(await toggle(CANCELLED_COMMAND)).toHaveTextContent('Cancelled')
    expect(await toggle(COMPLETED_COMMAND)).not.toHaveTextContent('Cancelled')
    expect(screen.queryByText(LATE_RESULT.stdout, { exact: true })).not.toBeInTheDocument()
    noExecution()
  })

  it.each(['before-read', 'during-read'] as const)('cancellation confirmed %s cannot be overwritten by a stale real OLDER page', async order => {
    beginTools()
    if (order === 'before-read') { confirmCancelled(); bakeCancelled(); expectCancellationTruth() }
    const request = deferred<URLSearchParams>()
    const response = deferred<Response>()
    network.respond('GET', `/api/v1/sessions/${SID}/messages`, read => { request.resolve(new URLSearchParams(read.query)); return response.promise })
    let reading!: Promise<void>
    act(() => { useChatStore.getState().enterHistoryReading(SID); reading = useChatStore.getState().loadOlderHistory(SID, { limit: 20, min_rows: 1 }) })
    expect(Object.fromEntries(await request.promise)).toStrictEqual({ before: pos(5000), limit: '20', min_rows: '1' })
    if (order === 'during-read') { confirmCancelled(); bakeCancelled(); expectCancellationTruth() }
    const older = entry(4999, { id: OWNER, role: 'assistant', turn_id: TURN, content: BODY, tool_calls: [
      { id: 'cancelled-call', tool: 'bash', status: 'success', content_state: 'full', parameters: { command: CANCELLED_COMMAND }, result: LATE_RESULT, duration_ms: 999 },
      { id: 'completed-call', tool: 'bash', status: 'success', content_state: 'full', parameters: { command: COMPLETED_COMMAND }, result: COMPLETED_RESULT, duration_ms: 17 },
      { id: 'pending-call', tool: 'bash', status: 'pending', content_state: 'full', parameters: { command: PENDING_COMMAND } },
    ] })
    await act(async () => { response.resolve(json(wirePage([older], 4, { older: true }))); await reading })
    expect(bucket().history.requests.older.status).toBe('idle')
    expect(bucket().history.readerPositions).toStrictEqual([pos(4999), pos(5000)])
    expectCancellationTruth()
    const committedOwners = getMessages(bucket()).filter(row => row.historyPosition === pos(4999))
    expect(committedOwners, 'assert the newly committed older owner, not a hidden retained live copy').toHaveLength(1)
    expect(committedOwners[0].tool_calls?.find(call => call.id === 'cancelled-call')).toMatchObject({ status: 'cancelled', result: null, duration_ms: 731 })
    const pending = terminalCall('pending-call')
    expect(pending).toMatchObject({ id: 'pending-call', params: { command: PENDING_COMMAND } })
    expect(pending?.result).toBeUndefined()
    expect(pending?.status).not.toBe('cancelled') // wire pending != internal enum; prior Q1=B
    await show(surface)
    expect(await toggle(CANCELLED_COMMAND)).toHaveTextContent('Cancelled')
    expect(await toggle(COMPLETED_COMMAND)).not.toHaveTextContent('Cancelled')
    expect(await toggle(PENDING_COMMAND)).not.toHaveTextContent('Cancelled')
    noExecution()
  })

  it.each(['success', 'error', 'pending'] as const)('recorded %s with no result does not inherit parent cancellation or the separate terminal outcome', async status => {
    attachUsage(SID) // open real transport makes accidental renderer execution observable
    const command = `printf inherited-${status}`
    const source = entry(5101, { id: 'inherited-owner', role: 'assistant', turn_id: TURN, content: BODY, status: 'interrupted', truncated: true, truncation_reason: 'cancelled', tool_calls: [
      { id: 'independent-call', tool: 'bash', status, content_state: 'full', parameters: { command }, ...(status === 'error' ? { error: 'Recorded tool failure' } : {}) },
    ] })
    const outcome = entry(5102, { id: 'terminal-note', role: 'assistant', turn_id: TURN, content: 'A separate cancelled-turn outcome.', terminal_outcome: true, status: 'interrupted', truncated: true, truncation_reason: 'cancelled' })
    network.respond('GET', `/api/v1/sessions/${SID}/messages`, () => json(wirePage([source, outcome], 3)))
    await act(async () => { await useChatStore.getState().loadLatestHistory(SID) })
    expect(getMessages(bucket()).map(row => row.id)).toStrictEqual(['inherited-owner', 'terminal-note'])
    const call = bucket().messagesById['inherited-owner'].tool_calls?.[0]
    expect(call).toMatchObject({ id: 'independent-call', params: { command } })
    if (status !== 'pending') expect(call?.status).toBe(status)
    expect(call?.result).toBeUndefined()
    expect(call?.status).not.toBe('cancelled')
    await show(surface)
    expect(screen.getAllByTestId('assistant-message').map(row => row.getAttribute('data-message-id')), 'distinct terminal-outcome source is not merged into its same-turn narration').toStrictEqual(['inherited-owner', 'terminal-note'])
    const control = await toggle(command)
    expect(within(control).queryByText('Cancelled', { exact: true }), 'per-call fact does not inherit cancelled parent/terminal sentence').not.toBeInTheDocument()
    expect(screen.getByText('A separate cancelled-turn outcome.', { exact: true })).toBeVisible()
    noExecution()
  })
})
