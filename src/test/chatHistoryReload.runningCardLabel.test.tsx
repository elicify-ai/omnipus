// 6c UAT Done-card regression, ta4 brief items 1–4 (2026-11-02).
// Oracle: a started tool without a terminal result is visibly Running...,
// regardless of whether its owner is still the streaming tail. Real list,
// native tool UI, store, adapters and WsConnection; only process edges doubled.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, render, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { ClientFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { useSessionStore } from '@/store/session'
import { VirtualizedMessageList } from '@/components/chat/ChatMessageList'
import { BashOutputUI } from '@/components/chat/tools/BashOutput'
import { FileReadPreviewUI } from '@/components/chat/tools/FileReadPreview'
import { Stage1Network, resetStage1App, stopStage1App } from '../../tests/l1-room-ui/stage1-harness'
import { json } from '../../tests/l1-room-ui/harness'
import { entry, expectValidPage, page, pos, replay, snapshot, STAMP } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

const SID = '6c-running-card-session'
const TURN = '6c-running-card-turn'
const OWNER = '6c-running-card-owner'
const CALL = '6c-running-card-call'
const BODY = 'The requested tool is still executing.'
const COMMAND = 'sleep 30 && printf done-card-fixture'
const PATH = '/workspace/6c-running-card-fixture.txt'
const STEER = 'Keep the tool running and report its result.'
let network: Stage1Network
let transport: ReturnType<typeof historyTransport>

beforeEach(() => {
  resetStage1App()
  __resetFinishedTurnIdsForTests()
  network = new Stage1Network()
  // Real active-turn checklist recovery is an optional bounded HTTP read.
  network.respond('GET', `/api/v1/sessions/${SID}/messages`, () => json(page([], { sessionId: SID })))
  act(() => useSessionStore.getState().setActiveSession(SID, 'mia'))
  transport = historyTransport()
  transport.attach(SID)
  // Exercise the production list's supported browser fallback, not a mocked
  // list or zero-sized jsdom virtual canvas. It draws real historical rows.
  vi.stubGlobal('ResizeObserver', undefined)
})
afterEach(async () => {
  transport.connection.disconnect()
  await stopStage1App()
  expect(network.unexpected, 'every HTTP request is declared at the process edge').toStrictEqual([])
  expect(network.requests.filter(request => request.method !== 'GET'), 'rendering tools performs no command or write request').toStrictEqual([])
})

function bucket() { return useChatStore.getState().sessionsById[SID] }
function state(active = true): Extract<ServerFrame, { type: 'session_state' }> {
  return { type: 'session_state', session_id: SID, user_id: '6c-card-reader', emitted_at: STAMP,
    pending_approvals: [], history_state: { status: 'available' },
    ...(active ? { active_turn: { turn_id: TURN, agent_id: 'mia', started_at: STAMP } } : {}) }
}
function complete(): Extract<ServerFrame, { type: 'catch_up_complete' }> {
  return { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'snapshot' }
}
function List() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore(store => store.messages)
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root>
    <BashOutputUI /><FileReadPreviewUI />
    <VirtualizedMessageList messages={messages} liteMode={false} agentName="Mia" />
  </ThreadPrimitive.Root></AssistantRuntimeProvider>
}
function draw() {
  render(<QueryClientProvider client={queryClient}><List /></QueryClientProvider>)
}
function card(callId = CALL) {
  const rows = document.querySelectorAll<HTMLElement>(`[data-history-tool-row="${callId}"]`)
  expect(rows, 'instrument: exactly one real, visible tool card, not an absent-label pass').toHaveLength(1)
  expect(rows[0]).toBeVisible()
  return rows[0]
}
function expectRunningCard(callId = CALL) {
  const row = card(callId)
  console.info('ta4 running-label evidence', JSON.stringify({ callId, expectedLabel: 'Running...', actualCardText: row.textContent }))
  // Keep this RED nonfatal so the actual result transition is still checked.
  expect.soft(within(row).queryByText('Running...', { exact: true }), 'ta4: a started tool without a result must say Running...').toBeVisible()
  expect.soft(within(row).queryByText('Done', { exact: true }), 'ta4: a still-executing tool must not claim Done').not.toBeInTheDocument()
}
function result(tool: 'bash' | 'read_file', seq = 101) {
  const output = tool === 'bash' ? { stdout: 'done-card-fixture' } : 'first line\nsecond line'
  transport.deliver({ type: 'tool_call_result', session_id: SID, call_id: CALL, tool, agent_id: 'mia',
    status: 'success', result: output, duration_ms: 30000, seq })
  expect(transport.frames.at(-1), 'instrument: the genuine success was accepted by the parser').toMatchObject({ type: 'tool_call_result', call_id: CALL, status: 'success' })
  const settled = bucket().toolCalls[CALL] ?? Object.values(bucket().messagesById).flatMap(message => message.tool_calls ?? []).find(call => call.id === CALL)
  expect(settled, 'instrument: the real reducer retained the terminal call and output').toMatchObject({ id: CALL, tool, status: 'success', result: output, duration_ms: 30000 })
  const row = card()
  expect(within(row).queryByText('Running...', { exact: true }), 'ta4: terminal success removes the running label').not.toBeInTheDocument()
  if (tool === 'bash') {
    expect(row, 'ta4: finished bash visibly reports completion or duration').toHaveTextContent(/Done|30s/)
  } else {
    // Independent content arithmetic: two supplied lines, not a guessed
    // runtime-derived status. A real terminal read can show its line count.
    expect(row).toHaveTextContent('2 lines')
  }
}
function joinWhileRunning(tool: 'bash' | 'read_file') {
  const parameters = tool === 'bash' ? { command: COMMAND } : { path: PATH }
  const source = entry(601, { id: OWNER, role: 'assistant', turn_id: TURN, content: BODY, tool_calls: [
    { id: CALL, tool, status: 'running', content_state: 'full', parameters },
  ] })
  const selected = page([source], { sessionId: SID, rows: 2 })
  expectValidPage(selected, [pos(601)], 2) // one prose source plus one logical call
  // SessionSnapshotFrame is metadata-only by contract. The selected running
  // record arrives as the source replay plus a start, NEVER a synthetic result.
  transport.deliver(snapshot(selected), replay(source, SID),
    { type: 'tool_call_start', session_id: SID, tool, call_id: CALL, params: parameters, agent_id: 'mia',
      history_origin: { position: pos(601), entry_id: OWNER, turn_id: TURN } },
    { type: 'done', session_id: SID, stats: { frames_emitted: 2 } }, complete(), state())
  expect(transport.frames.filter(frame => frame.type === 'tool_call_result'), 'instrument: no success was replayed for a running call').toStrictEqual([])
  expect(transport.frames.find(frame => frame.type === 'done')).not.toHaveProperty('turn_id')
  expect(bucket().cursor, 'instrument: selected catch-up committed, not rejected as a bad fixture').toStrictEqual({ bootId: 'history-boot', seq: 100 })
  expect(bucket().activeTurnId).toBe(TURN)
  expect(bucket().isStreaming, 'instrument: session still has active work / Stop').toBe(true)
  expect(bucket().messagesById[OWNER]).toMatchObject({ turnId: TURN, content: BODY, status: 'done',
    tool_calls: [{ id: CALL, tool, status: 'running', params: parameters }] })
  // ChatMessage.isStreaming is optional; absent and false both mean this
  // saved owner is not the live streaming tail (dispatcher correction Q1=A).
  expect(!!bucket().messagesById[OWNER].isStreaming, 'instrument: saved owner is not streaming').toBe(false)
  draw()
  expect(card()).toHaveTextContent(tool === 'bash' ? COMMAND : '6c-running-card-fixture.txt')
}

it.each(['bash', 'read_file'] as const)('ta4 A: joined %s card stays Running... through untagged replay done until its actual result', tool => {
  joinWhileRunning(tool)
  expectRunningCard()
  result(tool)
  expect(transport.socket.sent.filter(frame => frame.type === 'message' || frame.type === 'cancel'), 'a joining tab does not execute or cancel the tool').toStrictEqual([])
})

it('ta4 B: sender bash card stays Running... after a real steer echo and working status until its result', () => {
  const empty = page([], { sessionId: SID })
  transport.deliver(snapshot(empty), state(false), { type: 'done', session_id: SID }, complete())
  act(() => useChatStore.getState().sendMessage('Run the requested tool.'))
  transport.deliver(state(),
    { type: 'token', session_id: SID, message_id: OWNER, turn_id: TURN, agent_id: 'mia', content: BODY, seq: 101 },
    { type: 'tool_call_start', session_id: SID, call_id: CALL, tool: 'bash', agent_id: 'mia', params: { command: COMMAND }, seq: 102 })
  expect(bucket().toolCalls[CALL]).toMatchObject({ id: CALL, tool: 'bash', status: 'running', params: { command: COMMAND } })
  act(() => useChatStore.getState().sendMessage(STEER))
  const sent = transport.socket.sent.filter((frame): frame is Extract<ClientFrame, { type: 'message' }> => frame.type === 'message')
  expect(sent.map(frame => frame.content), 'instrument: actual sender transport carried initial input and steer').toStrictEqual(['Run the requested tool.', STEER])
  const clientId = sent[1].client_message_id
  expect(typeof clientId, 'instrument: real steer supplied a reconciliation identity').toBe('string')
  transport.deliver({ type: 'user_message', session_id: SID, id: '6c-persisted-steer', client_message_id: clientId,
    content: STEER, timestamp: STAMP, agent_id: 'mia', seq: 103 },
  { type: 'message_status', session_id: SID, client_message_id: clientId!, state: 'working', seq: 104 })
  expect(bucket().messagesById[clientId!]).toMatchObject({ role: 'user', content: STEER, deliveryStatus: 'working' })
  expect(bucket().messageOrder.indexOf(OWNER), 'instrument: the running call owner is no longer the list tail').toBeLessThan(bucket().messageOrder.length - 1)
  expect(bucket().toolCallOwnerMessageId?.[CALL]).toBe(OWNER)
  expect(bucket().toolCalls[CALL].status).toBe('running')
  expect(bucket().isStreaming).toBe(true)
  draw()
  expectRunningCard()
  result('bash', 105)
})

it('ta4 C: another turn\'s saved pending card never borrows the active turn\'s Running... label', async () => {
  const pending: Message = entry(602, { id: '6c-unstarted-owner', role: 'assistant', turn_id: '6c-other-unstarted-turn', content: 'An unrelated unstarted request.', tool_calls: [
    { id: '6c-unstarted-call', tool: 'bash', status: 'pending', content_state: 'full', parameters: { command: 'printf must-not-start' } },
  ] })
  const selected = page([pending], { sessionId: SID, rows: 2 })
  expectValidPage(selected, [pos(602)], 2)
  const empty = page([], { sessionId: SID })
  transport.deliver(snapshot(empty), state(false), { type: 'done', session_id: SID }, complete())
  network.respond('GET', `/api/v1/sessions/${SID}/messages`, () => json(selected))
  await act(async () => useChatStore.getState().loadLatestHistory(SID))
  transport.deliver(state())
  expect(bucket().activeTurnId, 'instrument: a different active turn is genuinely present').toBe(TURN)
  expect(bucket().messagesById['6c-unstarted-owner']).toMatchObject({ turnId: '6c-other-unstarted-turn', tool_calls: [
    { id: '6c-unstarted-call', tool: 'bash', params: { command: 'printf must-not-start' } },
  ] })
  draw()
  const row = card('6c-unstarted-call')
  expect(row).toHaveTextContent('printf must-not-start')
  expect(within(row).queryByText('Running...', { exact: true }), 'ta4 C: pending is not proof of execution in another active turn').not.toBeInTheDocument()
  expect(transport.frames.filter(frame => frame.type === 'tool_call_start')).toStrictEqual([])
  expect(transport.socket.sent.filter(frame => frame.type === 'message' || frame.type === 'cancel')).toStrictEqual([])
})
