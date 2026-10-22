// Oracle: chat replay quick-fix brief R2/R4/R5; AsyncAPI CatchUpCompleteFrame.
// Only the browser's socket and clock are fakes. Transport validation, store
// reduction and React subscriptions are real. Frames cross distinct paints.
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WsConnection, type ServerFrame } from './ws'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useToolApprovalStore } from '@/store/toolApproval'
import { ChatConnectionNotice } from '@/components/chat/ConnectionStatus'
import { ToolApprovalModal } from '@/components/agents/ToolApprovalModal'
import { approvalPosts, installApprovalNetwork, renderWithClient } from '../../tests/l1-ui-red-u23/support'
import type { ClientFrame } from '@/lib/api/generated/asyncapi-types'
import type { Session, Message, SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import { fixtureHistoryPosition, wireHistoryPage, wireHistorySnapshot } from '../../tests/chat-history-paging/legacy-wire'
import { queryClient } from '@/lib/queryClient'
import { noteForegroundAttach } from '@/store/session/foregroundAck'
import { AttachReplayBuffer } from './attach-replay'

class Socket {
  static OPEN = 1
  static CLOSED = 3
  static latest: Socket
  readyState = Socket.OPEN
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: ((event: { code: number; reason: string }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn()
  constructor() { Socket.latest = this }
  receive(frame: ServerFrame) { this.onmessage?.({ data: JSON.stringify(frame) }) }
}

const SID = 'attach-replay-test'
const STAMP = '2026-10-22T08:00:00Z'
const originFor = (id: string) => ({ position: fixtureHistoryPosition(SID, id), entry_id: id })
function recordedSource(id: string, role: NonNullable<Message['role']>, content: string, tools?: Message['tool_calls']): Message {
  return { id, type: 'message', role, agent_id: 'mia', content, timestamp: STAMP,
    history_position: originFor(id).position, ...(tools ? { tool_calls: tools } : {}) }
}
function snapshotFor(sources: Message[], rows: number, carriers: ServerFrame[] = [], info: Partial<SessionHistoryResponse['page_info']> = {}) {
  return wireHistorySnapshot(wireHistoryPage(SID, sources, rows, { info }), {
    seq: 10, boot_id: 'boot', reason: 'unknown_position',
  }, carriers)
}
const questionSource = recordedSource('question', 'user', 'Saved question')
const answerSource = recordedSource('answer', 'assistant', 'Saved answer', [
  { id: 'read', tool: 'read_file', parameters: { path: 'note.txt' }, status: 'success', result: { content: 'Saved result' }, content_state: 'full' },
])
const sourceFrames: ServerFrame[] = [
  { type: 'replay_message', session_id: SID, id: 'question', role: 'user', content: 'Saved question', history_origin: originFor('question') },
  { type: 'tool_call_start', session_id: SID, call_id: 'read', tool: 'read_file', params: { path: 'note.txt' }, history_origin: originFor('answer') },
  { type: 'tool_call_result', session_id: SID, call_id: 'read', tool: 'read_file', status: 'success', result: { content: 'Saved result' }, history_origin: originFor('answer') },
  { type: 'replay_message', session_id: SID, id: 'answer', role: 'assistant', content: 'Saved answer', history_origin: originFor('answer') },
]
// Two owning sources: one question row plus assistant narration + one logical
// tool row. Start/result are not two sources or two tool rows (D3/D4).
const snapshot = snapshotFor([questionSource, answerSource], 3, sourceFrames)
const replayDone: ServerFrame = { type: 'done', session_id: SID, stats: { frames_emitted: 4 } }
const complete: ServerFrame = { type: 'catch_up_complete', session_id: SID, seq: 10, boot_id: 'boot', mode: 'snapshot' }
const history: ServerFrame[] = [snapshot, ...sourceFrames, replayDone]
let conn: WsConnection
let deliveries: ServerFrame[][]
let replayNotices: [sessionId: string, message: string][]

function VisibleTranscript() {
  const messages = useChatStore(s => s.messages)
  const tools = useChatStore(s => s.toolCalls)
  return <output data-testid="projection">{JSON.stringify({ text: messages.map(m => m.content), running: Object.values(tools).filter(t => t.status === 'running').length })}</output>
}

function receive(frame: ServerFrame) {
  act(() => { Socket.latest.receive(frame); vi.advanceTimersByTime(32) })
}
function attach(session = SID) {
  expect(conn.send({ type: 'attach_session', session_id: session })).toBe(true)
  act(() => {
    useSessionStore.getState().setActiveSession(session)
    useChatStore.getState().setReplaying(true)
  })
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.stubGlobal('WebSocket', Socket)
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({ sessionsById: {} })
  useChatStore.getState().resetSession()
  useSecretRequestStore.getState().reset()
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  deliveries = []
  replayNotices = []
  conn = new WsConnection({
    onFrames: frames => {
      deliveries.push(frames)
      for (const frame of frames) useChatStore.getState().handleFrame(frame)
    },
    onConnected: () => useConnectionStore.getState().setConnected(true),
    onDisconnected: () => {
      useConnectionStore.getState().recordDisconnect(null)
      useChatStore.getState().clearStreamingState()
    },
    onError: message => useConnectionStore.getState().setConnectionError(message),
    onReplayNotice: (sessionId, message) => { replayNotices.push([sessionId, message]) },
    onReconnectStateChange: (phase, attempt) => useConnectionStore.getState().setReconnectState(phase, attempt),
  })
  conn.connect()
  Socket.latest.onopen?.()
})
afterEach(() => {
  cleanup()
  conn.disconnect()
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('attach generations across live release and shown acknowledgements', () => {
  it('SF-1 keeps a held live save-failure error from retiring a later A attach', () => {
    const initialSnapshot = snapshotFor([], 0)
    const snapshot = snapshotFor([questionSource], 1, [history[1]])
    attach()
    receive(initialSnapshot)
    attach('other')
    attach()
    receive(complete)
    receive({ type: 'error', session_id: SID, seq: 11, message: 'Could not save your message.' })
    const otherDone: ServerFrame = { type: 'catch_up_complete', session_id: 'other', seq: 1, mode: 'snapshot' }
    receive(otherDone)
    const observedBeforeLatest = deliveries.length
    receive(snapshot)
    receive(history[1])
    expect(deliveries, 'the second A history must remain out of sight after the held live error').toHaveLength(observedBeforeLatest)
    receive(complete)
    expect(useChatStore.getState().messages.map(message => message.content)).toEqual(['Saved question'])
  })

  it('W1 a reopened main acknowledges its own bound 9, not an older buffered response bound 7', () => {
    const snapshot = snapshotFor([], 0)
    const main: Session = {
      id: SID, type: 'main', agent_id: 'mia', title: 'Mia main', status: 'active', needs_attention: true,
      channel: 'web', partitions: [], created_at: '2026-10-06T00:00:00Z', updated_at: '2026-10-06T00:00:01Z',
      stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    }
    queryClient.setQueryData(['sessions'], [main])
    useConnectionStore.getState().setConnection(conn)
    const shown = (sessionId: string) => { attach(sessionId); noteForegroundAttach(sessionId) }
    const state = (bound: number): ServerFrame => ({ type: 'session_state', session_id: SID, user_id: 'user', pending_approvals: [], emitted_at: '2026-10-06T00:00:01Z', history_state: { status: 'available' }, attention_bound: bound })
    shown(SID)
    receive(snapshot)
    receive(state(7))
    shown('other')
    shown(SID)
    receive(complete)
    receive({ type: 'catch_up_complete', session_id: 'other', seq: 1, mode: 'snapshot' })
    receive(snapshot)
    receive(state(9))
    receive(complete)
    const acknowledgements = Socket.latest.send.mock.calls.map(([raw]) => JSON.parse(raw) as ClientFrame)
      .filter(frame => frame.type === 'attach_session' && frame.ack_attention)
    expect(acknowledgements).toEqual([{ type: 'attach_session', session_id: SID, boot_id: 'boot', since_seq: 10, ack_attention: true, attention_bound: 9 }])
    queryClient.removeQueries({ queryKey: ['sessions'] })
  })

  it('W1 ignores a completed A1 batch queued before reopening A2 and acknowledges only its bound 9', () => {
    const snapshot = snapshotFor([], 0)
    const main: Session = {
      id: SID, type: 'main', agent_id: 'mia', title: 'Mia main', status: 'active', needs_attention: true,
      channel: 'web', partitions: [], created_at: '2026-10-06T00:00:00Z', updated_at: '2026-10-06T00:00:01Z',
      stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    }
    queryClient.setQueryData(['sessions'], [main])
    useConnectionStore.getState().setConnection(conn)
    const shown = (sessionId: string) => { attach(sessionId); noteForegroundAttach(sessionId) }
    const state = (bound: number): ServerFrame => ({ type: 'session_state', session_id: SID, user_id: 'user', pending_approvals: [], emitted_at: '2026-10-06T00:00:01Z', history_state: { status: 'available' }, attention_bound: bound })
    shown(SID)
    receive(snapshot)
    receive(state(7))
    shown('other')
    // Parse A1's completion, but HOLD its scheduled delivery. Its pending
    // buffer is already deleted when the user next explicitly reopens A.
    act(() => Socket.latest.receive(complete))
    expect(deliveries).toEqual([])
    shown(SID)
    act(() => vi.advanceTimersByTime(32))
    expect(deliveries).toHaveLength(1) // Positive control: old batch DID deliver.
    receive({ type: 'catch_up_complete', session_id: 'other', seq: 1, mode: 'snapshot' })
    receive(snapshot)
    receive(state(9))
    receive(complete)
    const acknowledgements = Socket.latest.send.mock.calls.map(([raw]) => JSON.parse(raw) as ClientFrame)
      .filter(frame => frame.type === 'attach_session' && frame.ack_attention)
    expect(acknowledgements).toEqual([{ type: 'attach_session', session_id: SID, boot_id: 'boot', since_seq: 10, ack_attention: true, attention_bound: 9 }])
    queryClient.removeQueries({ queryKey: ['sessions'] })
  })
})

describe('bounded replay fallback', () => {
  it('releases oversized history with a visible warning and continues instead of keeping an unbounded blank view', () => {
    // The serialized UTF-16 payload alone exceeds the documented 8 MiB budget.
    const result = { output: 'x'.repeat(4 * 1024 * 1024) }
    const largeStart: ServerFrame = { type: 'tool_call_start', session_id: SID, call_id: 'large-result', tool: 'read_file', params: { path: 'large.txt' }, history_origin: originFor('bounded-final') }
    const largeResult: ServerFrame = { type: 'tool_call_result', session_id: SID, call_id: 'large-result', tool: 'read_file', status: 'success', result, history_origin: originFor('bounded-final') }
    const final: ServerFrame = { type: 'replay_message', session_id: SID, id: 'bounded-final', role: 'assistant', content: 'Final saved answer', history_origin: originFor('bounded-final') }
    const finalSource = recordedSource('bounded-final', 'assistant', 'Final saved answer', [
      { id: 'large-result', tool: 'read_file', parameters: { path: 'large.txt' }, status: 'success', result, content_state: 'full' },
    ])
    const snapshot = snapshotFor([questionSource, finalSource], 3, [history[1], largeStart, largeResult, final], { byte_budget_exceeded: true, budget_override: 'atomic_group' })
    attach()
    receive(snapshot)
    receive(history[1])
    receive(largeStart)
    receive(largeResult)
    // Chief Q1 A: replace the one assertion that required SF-2's misclassification.
    expect(useConnectionStore.getState().connectionError).toBeNull()
    expect(replayNotices).toEqual([[SID, 'Saved history is large. Showing messages as they load.']])
    expect(useChatStore.getState().messages.some(message => message.content === 'Saved question')).toBe(true)
    receive(final)
    receive(replayDone)
    receive(complete)
    expect(useChatStore.getState().messages.at(-1)?.content).toBe('Final saved answer')
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('bounds retained frame count as well as bytes and reports the fallback once', () => {
    const snapshot = snapshotFor([questionSource, recordedSource('answer', 'assistant', 'Saved answer')], 2, [history[1], history[4]])
    // Squad-lead Q3 A: exact sufficient serialized UTF-16 fixture capacity.
    // The required header must fit; ONLY maxFrames1 may trip on frame2.
    const maxBytes = [snapshot, history[1], history[4]].reduce((bytes, frame) => bytes + JSON.stringify(frame).length * 2, 0)
    const onLimit = vi.fn()
    const replay = new AttachReplayBuffer({ maxBytes, maxFrames: 1, onLimit })
    replay.begin(SID)
    expect(replay.accept(snapshot)).toEqual([])
    expect(replay.accept(history[1])).toEqual([snapshot, history[1]])
    expect(onLimit).toHaveBeenCalledTimes(1)
    expect(onLimit).toHaveBeenCalledWith(SID)
    expect(replay.bufferedBytes).toBe(0)
    expect(replay.accept(history[4])).toEqual([history[4]])
    expect(replay.accept(complete)).toEqual([complete])
    replay.clear()
    expect(replay.bufferedBytes).toBe(0)
  })
})

it('frame-count control: all fixture bytes fit, but one frame beyond the frame capacity still trips', () => {
  const snapshot = snapshotFor([questionSource, recordedSource('answer', 'assistant', 'Saved answer')], 2, [history[1], history[4]])
  const traffic = [snapshot, history[1], history[4]]
  const maxBytes = traffic.reduce((bytes, frame) => bytes + JSON.stringify(frame).length * 2, 0)
  const byteControl = new AttachReplayBuffer({ maxBytes, maxFrames: traffic.length })
  byteControl.begin(SID)
  for (const frame of traffic) expect(byteControl.accept(frame), 'all exact fixture bytes fit without byte overflow').toEqual([])
  expect(byteControl.bufferedBytes).toBe(maxBytes)
  byteControl.clear()
  const onLimit = vi.fn()
  const frameControl = new AttachReplayBuffer({ maxBytes, maxFrames: traffic.length - 1, onLimit })
  frameControl.begin(SID)
  for (const frame of traffic.slice(0, -1)) expect(frameControl.accept(frame)).toEqual([])
  expect(frameControl.accept(traffic[traffic.length - 1]), 'exactly one frame beyond the cap releases the full batch').toEqual(traffic)
  expect(onLimit).toHaveBeenCalledTimes(1)
  expect(frameControl.bufferedBytes).toBe(0)
  frameControl.clear()
})

describe('attach replay is one visible transaction', () => {
  it('R2 publishes no intermediate messages or running tools across paints, then one complete batch', () => {
    render(<VisibleTranscript />)
    attach()
    for (const frame of history) {
      receive(frame)
      expect(screen.getByTestId('projection').textContent).toBe('{"text":[],"running":0}')
    }
    expect(deliveries).toEqual([])
    receive(complete)
    expect(deliveries).toEqual([[...history, complete]])
    expect(screen.getByTestId('projection').textContent).toBe('{"text":["Saved question","Saved answer"],"running":0}')
  })

  it('R4 keeps an announced live turn open after replay done and streams tokens after catch-up', () => {
    const snapshot = snapshotFor([], 0)
    const replayDone: ServerFrame = { type: 'done', session_id: SID, stats: { frames_emitted: 0 } }
    attach()
    receive(snapshot)
    receive({ type: 'session_state', session_id: SID, user_id: 'user', pending_approvals: [], emitted_at: new Date().toISOString(), history_state: { status: 'available' }, active_turn: { turn_id: 'live', agent_id: 'mia', started_at: new Date().toISOString() } })
    receive(replayDone)
    expect(deliveries).toEqual([])
    receive(complete)
    expect(useChatStore.getState().isStreaming).toBe(true)
    receive({ type: 'token', session_id: SID, turn_id: 'live', message_id: 'live-answer', agent_id: 'mia', content: 'Still ' })
    expect(useChatStore.getState().messages.at(-1)?.content).toBe('Still ')
    receive({ type: 'token', session_id: SID, turn_id: 'live', message_id: 'live-answer', agent_id: 'mia', content: 'working' })
    expect(useChatStore.getState().messages.at(-1)?.content).toBe('Still working')
    expect(useChatStore.getState().isStreaming).toBe(true)
    receive({ type: 'done', session_id: SID, turn_id: 'live', stats: { tokens: 2, cost: 0 } })
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(useChatStore.getState().messages.at(-1)?.content).toBe('Still working')
  })

  it('R4 restores pending approval and secret cards only with the completed replay, keeping their response identities', () => {
    const snapshot = snapshotFor([], 0)
    const replayDone: ServerFrame = { type: 'done', session_id: SID, stats: { frames_emitted: 0 } }
    attach()
    receive(snapshot)
    receive({ type: 'tool_approval_required', session_id: SID, approval_id: 'approval-1', tool_call_id: 'call-1', tool_name: 'bash', args: { command: 'pwd' }, agent_id: 'mia', turn_id: 'live', expires_in_ms: 60000 })
    receive({ type: 'secret_request', session_id: SID, request_id: 'secret-1', acting_agent_id: 'mia', acting_agent_name: 'Mia', purpose: 'Connect calendar', recipient: { kind: 'provider_key', lines: ['Calendar'] }, fields: [{ key: 'key', label: 'API key' }] })
    expect(useToolApprovalStore.getState().queue).toEqual([])
    expect(useSecretRequestStore.getState().byId).toEqual({})
    receive(replayDone)
    receive(complete)
    expect(useToolApprovalStore.getState().queue.map(item => item.approvalId)).toEqual(['approval-1'])
    expect(useSecretRequestStore.getState().byId['secret-1']?.request.state).toBe('pending')
    expect(useSecretRequestStore.getState().byId['secret-1'].request.fields).toEqual([{ key: 'key', label: 'API key' }])
    receive({ type: 'tool_approval_resolved', session_id: SID, approval_id: 'approval-1', state: 'approved' })
    receive({ type: 'secret_request_resolved', session_id: SID, request_id: 'secret-1', state: 'submitted' })
    expect(useToolApprovalStore.getState().queue).toEqual([])
    expect(useSecretRequestStore.getState().byId['secret-1'].request.state).toBe('submitted')
  })

  it('R4 an approval restored from session state can be approved once through its real dialog', async () => {
    const snapshot = snapshotFor([], 0)
    const replayDone: ServerFrame = { type: 'done', session_id: SID, stats: { frames_emitted: 0 } }
    installApprovalNetwork() // Fetch/socket boundaries only; the established connection stays real.
    renderWithClient(<ToolApprovalModal />)
    attach()
    receive(snapshot)
    receive({ type: 'session_state', session_id: SID, user_id: 'user', emitted_at: new Date().toISOString(), history_state: { status: 'available' }, pending_approvals: [{ approval_id: 'approval-restored', session_id: SID, tool_name: 'read_file', agent_id: 'mia', tool_call_id: 'call-restored', turn_id: 'live', args: { path: 'note.txt' }, expires_in_ms: 60000 }] })
    expect(screen.queryByRole('dialog')).toBeNull()
    receive(replayDone)
    receive(complete)
    expect(screen.getByRole('dialog')).toBeVisible()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /^Approve Once$/ })) })
    expect(approvalPosts()).toEqual([{ id: 'approval-restored', body: { action: 'allow_once' } }])
    expect(useToolApprovalStore.getState().queue).toEqual([])
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('R5 drops an interrupted replay, exposes reconnect state, and cannot leak its rows into the retry', () => {
    const discarded: ServerFrame = { type: 'replay_message', session_id: SID, id: 'discard', role: 'user', content: 'Incomplete old replay', history_origin: originFor('discard') }
    const snapshot = snapshotFor([recordedSource('discard', 'user', 'Incomplete old replay')], 1, [discarded])
    attach()
    receive(snapshot)
    receive(discarded)
    act(() => Socket.latest.onclose?.({ code: 1006, reason: 'connection lost' }))
    expect(useChatStore.getState().messages).toEqual([])
    expect(useChatStore.getState().isReplaying).toBe(false)
    expect(useConnectionStore.getState().isConnected).toBe(false)
    expect(useConnectionStore.getState().reconnectPhase).toBe('reconnecting')
    act(() => vi.advanceTimersByTime(1000))
    Socket.latest.onopen?.()
    attach()
    const fresh: ServerFrame = { type: 'replay_message', session_id: SID, id: 'fresh', role: 'user', content: 'Successful retry', history_origin: originFor('fresh') }
    receive(snapshotFor([recordedSource('fresh', 'user', 'Successful retry')], 1, [fresh]))
    receive(fresh)
    receive(complete)
    expect(useChatStore.getState().messages.map(m => m.content)).toEqual(['Successful retry'])
  })

  it('R5 an interrupted empty replay reaches the existing reconnect notice instead of remaining silently blank', () => {
    const snapshot = snapshotFor([], 0)
    attach()
    receive(snapshot)
    act(() => Socket.latest.onclose?.({ code: 1006, reason: 'connection lost' }))
    render(<ChatConnectionNotice />)
    expect(screen.queryByTestId('connection-status-line')).toBeNull() // Existing brief-drop quiet period.
    act(() => vi.advanceTimersByTime(120_001)) // Existing two-minute chat notice threshold.
    expect(screen.getByTestId('connection-status-line').textContent).toBe("Can't reach Omnipus right now. Your agents keep working. · Try again")
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('R5 surfaces a replay error without publishing partial history or waiting forever for catch-up', () => {
    const discarded: ServerFrame = { type: 'replay_message', session_id: SID, id: 'discard', role: 'user', content: 'Partial', history_origin: originFor('discard') }
    const snapshot = snapshotFor([recordedSource('discard', 'user', 'Partial')], 1, [discarded])
    attach()
    receive(snapshot)
    receive(discarded)
    receive({ type: 'error', session_id: SID, message: 'Session replay failed' })
    receive({ type: 'done', session_id: SID, stats: { frames_emitted: 1, replay_error: true } })
    act(() => vi.advanceTimersByTime(800))
    expect(useChatStore.getState().messages.some(m => m.content === 'Partial')).toBe(false)
    expect(useChatStore.getState().messages.map(m => m.content)).toEqual(['Session replay failed'])
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('R5 releases an error-only attach refusal while the socket stays healthy', () => {
    attach()
    const refusal: ServerFrame = { type: 'error', session_id: SID, message: 'session not found' }
    receive(refusal)
    expect(deliveries).toEqual([[refusal]])
    act(() => vi.advanceTimersByTime(800))
    expect(useChatStore.getState().isReplaying).toBe(false)
    expect(useChatStore.getState().messages.map(m => m.content)).toEqual(['session not found'])
    expect(conn.isConnected).toBe(true)
  })

  it('R5 a global attach refusal clears outstanding buffers so a retry can finish', () => {
    const snapshot = snapshotFor([questionSource], 1, [history[1]])
    attach()
    const refusal: ServerFrame = { type: 'error', message: 'invalid session_id' }
    receive(refusal)
    expect(deliveries).toEqual([[{ ...refusal, session_id: SID }]])
    act(() => vi.advanceTimersByTime(800))
    expect(useChatStore.getState().isReplaying).toBe(false)
    attach()
    receive(snapshot)
    receive(history[1])
    receive(complete)
    expect(useChatStore.getState().messages.map(m => m.content)).toEqual(['Saved question'])
  })

  it('R2 a refused first attach does not unbuffer another outstanding response', () => {
    attach()
    attach('other')
    const refusal: ServerFrame = { type: 'error', message: 'invalid session_id' }
    receive(refusal)
    expect(deliveries).toEqual([[{ ...refusal, session_id: SID }]])
    act(() => vi.advanceTimersByTime(800))
    expect(useChatStore.getState().sessionsById[SID]?.isReplaying).toBe(false)
    expect(useChatStore.getState().sessionsById[SID]?.messageOrder.map(id => useChatStore.getState().sessionsById[SID].messagesById[id].content)).toEqual(['invalid session_id'])
    expect(useChatStore.getState().isReplaying).toBe(true)
    expect(useChatStore.getState().messages).toEqual([])
    const otherHistory: ServerFrame = { type: 'replay_message', session_id: 'other', id: 'other-row', role: 'user', content: 'Other history' }
    receive(otherHistory)
    expect(deliveries).toEqual([[{ ...refusal, session_id: SID }]])
    const otherDone: ServerFrame = { type: 'catch_up_complete', session_id: 'other', seq: 1, mode: 'snapshot' }
    receive(otherDone)
    expect(deliveries).toEqual([[{ ...refusal, session_id: SID }], [otherHistory, otherDone]])
    expect(useChatStore.getState().isReplaying).toBe(false)
    expect(useChatStore.getState().messages.map(m => m.content)).toEqual(['Other history'])
  })

  it('R2 an early error plus replay-failure done consumes only one overlapping attach', () => {
    const snapshot = snapshotFor([questionSource], 1, [history[1]])
    attach()
    attach('other')
    attach()
    receive({ type: 'error', session_id: SID, message: 'Session replay failed' })
    receive({ type: 'done', session_id: SID, stats: { replay_error: true, frames_emitted: 0 } })
    const otherDone: ServerFrame = { type: 'catch_up_complete', session_id: 'other', seq: 1, mode: 'snapshot' }
    receive(otherDone)
    expect(deliveries).toEqual([[otherDone]])
    receive(snapshot)
    receive(history[1])
    expect(deliveries).toEqual([[otherDone]])
    receive(complete)
    expect(useChatStore.getState().messages.map(m => m.content)).toEqual(['Saved question'])
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('R2 preserves an A → B → A attach until both A responses finish, without replaying the second visibly', () => {
    const snapshot = snapshotFor([questionSource], 1, [history[1]])
    const replayDone: ServerFrame = { type: 'done', session_id: SID, stats: { frames_emitted: 1 } }
    const final: ServerFrame = { type: 'replay_message', session_id: SID, id: 'final', role: 'user', content: 'Latest complete history', history_origin: originFor('final') }
    const finalSnapshot = snapshotFor([recordedSource('final', 'user', 'Latest complete history')], 1, [final])
    attach()
    receive(snapshot)
    receive(history[1])
    attach('other')
    attach()
    receive(replayDone)
    receive(complete)
    expect(deliveries).toEqual([])
    const other: ServerFrame = { type: 'catch_up_complete', session_id: 'other', seq: 1, mode: 'snapshot' }
    receive(other)
    expect(deliveries).toEqual([[other]])
    receive(finalSnapshot)
    receive(final)
    expect(deliveries).toEqual([[other]])
    receive(complete)
    expect(deliveries.at(-1)).toEqual([snapshot, history[1], replayDone, complete, finalSnapshot, final, complete])
    expect(useChatStore.getState().messages.map(m => m.content)).toEqual(['Latest complete history'])
  })

  it('R2 does not hold unrelated sessions or traffic when there is no attach', () => {
    attach()
    receive(history[1])
    const other: ServerFrame = { type: 'replay_message', session_id: 'other', id: 'other-row', role: 'user', content: 'Other session' }
    receive(other)
    expect(deliveries).toEqual([[other]])
    receive(complete)
    expect(deliveries.at(-1)).toEqual([history[1], complete])
  })
})
