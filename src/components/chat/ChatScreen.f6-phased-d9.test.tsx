// F6 phased proofs: architect seam T, current spec D9. The original recording
// and its exact A/B content remain frozen. Real app/runtime/navigation/history
// owners; only HTTP/WebSocket/browser layout are doubled. No manual release.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import type { Message, SessionHistoryResponse, SessionPage } from '@/lib/api/generated/openapi-types'
import type { ServerFrame, ClientFrame } from '@/lib/api/generated/asyncapi-types'
import { AttachSessionFrame as AttachSchema } from '@/lib/api/generated/ws-schemas'
import { fetchSessionDetail, fetchSessionMessages } from '@/lib/api'
import { parseFrameSafe } from '@/lib/ws'
import { queryClient } from '@/lib/queryClient'
import { getMessages, useChatStore } from '@/store/chat'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { useSessionStore } from '@/store/session'
import f6 from '@/store/__fixtures__/catchup/F6.json'
import { json, TestSocket } from '../../../tests/l1-room-ui/harness'
import { mountStage1App, resetStage1App, Stage1Network, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { availableDetail, entry, expectValidPage, page, pos, replay, session, snapshot, STAMP } from '../../../tests/chat-history-paging/fixtures'
import { sendFrames } from '../../../tests/chat-history-paging/application'

const A = 'sess-1'
const B = 'sess-2'
const A_TEXT = f6.expect.assistant_messages[0]
const B_TEXT = 'an older chat'
const B_BODY = 'B-only settled page body'
const B_FRESH = 'B appended while this reader was inactive'
const MEDIA_URL = '/api/v1/media/f6-phased-b-settled'
const RESULT_BODY = `F6_B_RESULT_BODY:${'b'.repeat(1024)}`
const MEDIA_PART: Extract<ServerFrame, { type: 'media' }>['parts'][number] = {
  type: 'file', url: MEDIA_URL, filename: 'b-settled.bin', content_type: 'application/octet-stream', caption: 'F6 B-only cached media',
}
const events = f6.tabs[0].events
const awayIndex = events.findIndex(event => event.frame?.type === 'attach_session' && event.frame.session_id === B)
const returnIndex = events.findIndex(event => event.frame?.type === 'attach_session' && event.frame.session_id === A)
const bRecordedSnapshot = events[awayIndex + 1].frame
if (bRecordedSnapshot?.type !== 'session_snapshot' || !bRecordedSnapshot.page_info?.first_position) throw new Error('Fixture instrument: F6 B snapshot missing')
const B_POSITION = bRecordedSnapshot.page_info.first_position
const bSources: Message[] = [
  entry(2001, { id: 'id-2', role: 'user', content: B_TEXT, history_position: B_POSITION }),
  entry(2002, { id: 'b-body', role: 'assistant', turn_id: 'b-body-turn', content: B_BODY, tool_calls: [
    { id: 'b-settled-call', tool: 'bash', status: 'success', content_state: 'full', parameters: { command: 'printf b-only-result' }, result: { stdout: RESULT_BODY, asset: MEDIA_URL } },
  ] }),
]
const bFreshSources = [
  ...bSources,
  entry(2003, { id: 'b-media-owner', role: 'assistant', turn_id: 'b-media-turn', content: 'B-only cached media reply', attachments: [{ type: 'file', path: 'b-settled.bin', size: 0, mime_type: 'application/octet-stream' }] }),
  entry(2004, { id: 'b-fresh', role: 'user', content: B_FRESH, client_message_id: 'b-fresh-other-client' }),
]
const aSources = [
  entry(2101, { id: 'id-1', role: 'user', content: f6.expect.user_message, client_message_id: 'client-1' }),
  entry(2102, { id: 'msg-1', role: 'assistant', turn_id: 'turn-1', content: A_TEXT }),
]

// not-wire-format: evidence/control metadata; never sent to the gateway.
type Phase = 'A-start' | 'B-active' | 'A-return' | 'B-reentry'
type Read = { phase: Phase; query: Record<string, string>; mode: 'latest' | 'window_refresh' }
type Attach = Extract<ClientFrame, { type: 'attach_session' }>
let network: Stage1Network
let app: Awaited<ReturnType<typeof mountStage1App>> | undefined
let phase: Phase
let bReads: Read[]
let freshSnapshotRequests: Attach[]
let navigation: Attach[]

function recordedFrames(part: typeof events): ServerFrame[] {
  return part.filter(event => event.dir === 'server→client').map(event => {
    const frame = parseFrameSafe(event.frame)
    if (!frame) throw new Error('Fixture instrument: invalid generated F6 frame')
    // Narrow the generated wider WS union by its actual frozen discriminators,
    // not a cast of unknown JSON into a hand-written/future wire format.
    switch (frame.type) {
      case 'session_state': case 'session_started': case 'user_message': case 'message_status':
      case 'token': case 'session_snapshot': case 'replay_message': case 'done': case 'catch_up_complete': return frame
      default: throw new Error(`Fixture instrument: unexpected F6 server type ${frame.type}`)
    }
  })
}

function carriers(entries: Message[], sid: string, media = false): ServerFrame[] {
  const frames: ServerFrame[] = []
  for (const source of entries) {
    frames.push(replay(source, sid))
    for (const call of source.tool_calls ?? []) {
      const origin = { position: source.history_position!, entry_id: source.id }
      frames.push(
        { type: 'tool_call_start', session_id: sid, tool: call.tool, call_id: call.id, params: call.parameters ?? {}, history_origin: origin, agent_id: 'mia' },
        { type: 'tool_call_result', session_id: sid, tool: call.tool, call_id: call.id, status: 'success', result: call.result ?? null, history_origin: origin, agent_id: 'mia' },
      )
      if (media) frames.push({ type: 'media', session_id: sid, parts: [MEDIA_PART] })
    }
  }
  return frames
}

function packet(entries: Message[], sid: string, rows: number, options: { range?: boolean; media?: boolean; limit?: number; min?: number } = {}): SessionHistoryResponse {
  const result = page(entries, { sessionId: sid, rows, info: { mode: options.range ? 'window_refresh' : 'latest', limit: options.limit ?? (options.range ? 100 : 40), min_rows: options.min ?? 20, ...(options.range ? { stop_reason: 'range_end' } : {}) } })
  // D4a independent UTF-8 carrier arithmetic, including actual tool/media
  // frames. No production weigher and no normalized1-as-measured-byte claim.
  const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...info } = result.page_info
  void projected_bytes; void soft_byte_budget; void byte_budget_exceeded; void budget_override
  const rest = { ...result, page_info: info }
  const cut = sid === A ? (phase === 'A-start' ? 1 : 13) : phase === 'B-reentry' ? 12 : 8
  const header = { ...snapshot(result), boot_id: 'boot-1', seq: cut, page_info: info }
  result.page_info.projected_bytes = Math.max(Buffer.byteLength(JSON.stringify(rest)), Buffer.byteLength(JSON.stringify([header, ...carriers(entries, sid, options.media)])))
  expectValidPage(result, entries.map(source => source.history_position!), rows)
  return result
}

function snapshotReply(body: SessionHistoryResponse, seq: number, media = false, active = false): ServerFrame[] {
  return [
    { ...snapshot(body), boot_id: 'boot-1', seq },
    { type: 'session_state', session_id: body.session_id, user_id: 'f6-phase-reader', pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' }, ...(active ? { active_turn: { turn_id: 'b-body-turn', agent_id: 'mia', started_at: STAMP } } : {}) },
    ...carriers(body.entries, body.session_id, media),
    { type: 'done', session_id: body.session_id, stats: { frames_emitted: carriers(body.entries, body.session_id, media).length } },
    { type: 'catch_up_complete', session_id: body.session_id, boot_id: 'boot-1', seq, mode: 'snapshot' },
  ]
}

function bucket(sid: string) { return useChatStore.getState().sessionsById[sid] }
function server() { if (!app) throw new Error('Fixture instrument: no real application'); return app.socket }
function attachRequests() { return server().sent.filter((frame): frame is Attach => frame.type === 'attach_session' && !frame.ack_attention) }
function note(label: string) {
  console.info('F6 phased proof', JSON.stringify({ phase: label, selected: useSessionStore.getState().activeSessionId, A: bucket(A)?.messageOrder, B: bucket(B)?.messageOrder, B_reader: bucket(B)?.history.readerPositions, B_tail: bucket(B)?.history.tailEntries.map(row => row.id), B_media: bucket(B)?.messagesById['b-media-owner']?.media?.map(part => part.url) }))
}

async function navigate(sid: string): Promise<Attach> {
  const count = attachRequests().length
  await act(async () => { expect(useSessionStore.getState().attachToSession(sid, 'chat', `F6 ${sid}`, 'mia'), 'real selected-session attach accepted').toBe(true) })
  await waitFor(() => expect(attachRequests()).toHaveLength(count + 1))
  const sent = AttachSchema.parse(attachRequests().at(-1))
  expect(sent.session_id).toBe(sid)
  expect(useSessionStore.getState().activeSessionId).toBe(sid)
  navigation.push(sent)
  return sent
}

beforeEach(() => {
  resetStage1App()
  __resetFinishedTurnIdsForTests()
  vi.stubGlobal('ResizeObserver', undefined) // real plain fallback, no geometry claim
  phase = 'A-start'; bReads = []; freshSnapshotRequests = []; navigation = []
  network = new Stage1Network()
  const a = { ...session(A), title: 'F6 first chat' }
  const b = { ...session(B), title: 'F6 independent other chat' }
  const listing: SessionPage = { sessions: [a, b] }
  network.respond('GET', '/api/v1/sessions', () => json(listing))
  network.respond('GET', `/api/v1/sessions/${A}`, () => json(availableDetail(phase === 'A-start' ? packet([], A, 0) : packet(aSources, A, 2), a)))
  network.respond('GET', `/api/v1/sessions/${B}`, () => json(availableDetail(packet(bSources, B, 3), b)))
  network.respond('GET', `/api/v1/sessions/${A}/messages`, () => json(phase === 'A-start' ? packet([], A, 0) : packet(aSources, A, 2)))
  network.respond('GET', `/api/v1/sessions/${B}/messages`, request => {
    const query = new URLSearchParams(request.query)
    const range = query.has('window_start')
    bReads.push({ phase, query: Object.fromEntries(query), mode: range ? 'window_refresh' : 'latest' })
    return json(packet(phase === 'B-reentry' ? bFreshSources : bSources, B, phase === 'B-reentry' ? 5 : 3, { range }))
  })
  act(() => useSessionStore.getState().setActiveSession(null, 'mia'))
})
afterEach(async () => {
  // Teardown after assertions only; navigation helpers never invoke release.
  await act(async () => useSessionStore.getState().setActiveSession(null, 'mia'))
  await stopStage1App()
  app = undefined
  expect(network.unexpected, 'all app HTTP is declared at its process edge').toStrictEqual([])
})

async function activeB() {
  app = await mountStage1App(`/sessions/${A}`, { initialAttachResponse: 'caller' })
  await waitFor(() => expect(useSessionStore.getState().activeSessionId).toBe(A))
  await sendFrames(server(), snapshotReply(packet([], A, 0), 1))
  await waitFor(() => expect(bucket(A).cursor).toStrictEqual({ bootId: 'boot-1', seq: 1 }))
  const head = events.slice(0, awayIndex)
  await sendFrames(server(), recordedFrames(head.slice(0, 1)))
  await act(async () => useChatStore.getState().sendMessage(f6.expect.user_message, { clientMessageId: 'client-1' }))
  const first = server().sent.filter(frame => frame.type === 'message')
  expect(first).toHaveLength(1)
  expect(first[0]).toMatchObject({ type: 'message', agent_id: 'mia', client_message_id: 'client-1', content: f6.expect.user_message })
  await sendFrames(server(), recordedFrames(head.slice(2)))
  await waitFor(() => expect(bucket(A).cursor).toStrictEqual({ bootId: 'boot-1', seq: 7 }))
  expect(getMessages(bucket(A)).filter(row => row.role === 'assistant').map(row => row.content)).toStrictEqual(['x0 x1 x2 '])
  expect(bucket(A).isStreaming).toBe(true)
  phase = 'B-active'
  const recorded = AttachSchema.parse(events[awayIndex].frame)
  expect(await navigate(B)).toMatchObject(recorded)
  await sendFrames(server(), recordedFrames(events.slice(awayIndex + 1, returnIndex)))
  await screen.findByText(B_TEXT, { exact: true })
  await act(async () => { await useChatStore.getState().loadLatestHistory(B) })
  await screen.findByText(B_BODY, { exact: true })

  // Positively populate actual adapter/query bodies. Infinity prevents normal
  // observer GC from concealing a missing feature-owned eviction.
  await act(async () => {
    await queryClient.fetchQuery({ queryKey: ['messages', B], queryFn: () => fetchSessionMessages(B), staleTime: Infinity, gcTime: Infinity })
    await queryClient.fetchQuery({ queryKey: ['session-detail', B], queryFn: () => fetchSessionDetail(B), staleTime: Infinity, gcTime: Infinity })
    useChatStore.getState().enterHistoryReading(B)
  })
  const refreshAttach = await navigate(B)
  const body = packet(bSources, B, 3, { limit: refreshAttach.history_limit, min: refreshAttach.history_min_rows })
  // A legitimate bounded revalidation populates tailEntries while preserving
  // this real cached reader; new media follows the completed replay boundary.
  await sendFrames(server(), [
    ...snapshotReply(body, 8),
    { type: 'token', session_id: B, message_id: 'b-media-owner', turn_id: 'b-media-turn', agent_id: 'mia', content: 'B-only cached media reply', seq: 9 },
    { type: 'media', session_id: B, seq: 10, parts: [MEDIA_PART] },
    { type: 'done', session_id: B, message_id: 'b-media-owner', turn_id: 'b-media-turn', seq: 11, stats: { tokens: 0 } },
  ])
  await waitFor(() => expect(bucket(B).cursor).toStrictEqual({ bootId: 'boot-1', seq: 11 }))
  await waitFor(() => expect(bucket(B).history.requests.refresh.status).toBe('idle'))
  expectBPopulated()
  note('B-active-positive')
}

function expectBPopulated() {
  const b = bucket(B)
  expect(useSessionStore.getState().activeSessionId).toBe(B)
  expect(b.messageOrder).toStrictEqual(['id-2', 'b-body'])
  expect(getMessages(b).map(row => row.content)).toStrictEqual([B_TEXT, B_BODY])
  expect(b.history.readerPositions).toStrictEqual([B_POSITION, pos(2002)])
  expect(b.history.tailEntries.map(row => row.id)).toStrictEqual(['id-2', 'b-body'])
  expect(b.messagesById['b-media-owner'].media?.map(part => part.url)).toStrictEqual([MEDIA_URL])
  expect(b.messagesById['b-media-owner']).toMatchObject({ role: 'assistant', content: 'B-only cached media reply', status: 'done' })
  expect(b.isStreaming).toBe(false)
  expect(JSON.stringify(queryClient.getQueryData(['messages', B]))).toContain(RESULT_BODY)
  expect(JSON.stringify(queryClient.getQueryData(['session-detail', B]))).toContain(MEDIA_URL)
  expect(JSON.stringify(b)).not.toContain(f6.expect.user_message)
  expect(JSON.stringify(b)).not.toContain('x0 x1 x2')
  expect(screen.getByText(B_TEXT, { exact: true })).toBeVisible()
  expect(screen.getByText(B_BODY, { exact: true })).toBeVisible()
  expect(screen.queryByText(f6.expect.user_message, { exact: true })).not.toBeInTheDocument()
}

async function returnA() {
  phase = 'A-return'
  const sent = await navigate(A)
  // Process-edge server follows the actual legal client request. This never
  // sends an invented incremental reply to a cursorless attach, nor edits F6.
  if (sent.since_seq === undefined) {
    await sendFrames(server(), snapshotReply(packet(aSources, A, 2, { limit: sent.history_limit, min: sent.history_min_rows }), 13))
  } else {
    expect(sent).toMatchObject(AttachSchema.parse(events[returnIndex].frame))
    await sendFrames(server(), recordedFrames(events.slice(returnIndex + 1)))
  }
  await waitFor(() => expect(bucket(A).cursor).toStrictEqual({ bootId: 'boot-1', seq: 13 }))
  await screen.findByText(A_TEXT.trim(), { exact: true })
  note('A-returned')
  return sent
}

function expectSettledBReleased() {
  const b = bucket(B)
  expect(b.messageOrder).toStrictEqual([])
  expect(b.messagesById).toStrictEqual({})
  expect(b.history.readerPositions).toStrictEqual([])
  expect(b.history.tailEntries).toStrictEqual([])
  expect(b.historyLiveOrder ?? []).toStrictEqual([])
  expect(queryClient.getQueryData(['messages', B])).toBeUndefined()
  expect(queryClient.getQueryData(['session-detail', B])).toBeUndefined()
  expect(JSON.stringify(b), 'no hidden retained media/result body in the inactive bucket').not.toContain(MEDIA_URL)
  expect(JSON.stringify(b)).not.toContain(RESULT_BODY)
}

function expectCompleteA() {
  const rows = getMessages(bucket(A))
  const answers = rows.filter(row => row.role === 'assistant')
  console.info('F6 A reconstruction', JSON.stringify(answers.map(row => ({ id: row.id, turnId: row.turnId, content: row.content, status: row.status, historyPosition: row.historyPosition }))))
  // Every oracle is unchanged and still FAILS the case. Collecting instead
  // of aborting lets P3/P5 execute their remaining independent phase checks.
  expect.soft(answers.map(row => row.content)).toStrictEqual(f6.expect.assistant_messages)
  expect.soft(answers).toHaveLength(1)
  expect.soft(answers[0].status).toBe('done')
  expect.soft(bucket(A).isStreaming).toBe(false)
  expect.soft(rows.filter(row => row.role === 'user').map(row => row.content)).toStrictEqual([f6.expect.user_message])
  expect.soft(JSON.stringify(bucket(A))).not.toContain(B_TEXT)
  expect.soft(JSON.stringify(bucket(A))).not.toContain(B_BODY)
  expect.soft(JSON.stringify(bucket(A))).not.toContain(MEDIA_URL)
  expect.soft(server().sent.filter(frame => frame.type === 'cancel')).toStrictEqual([])
}

describe('F6 / D9 actual phased application navigation', () => {
  it('P1: B visibly displays its own independent frozen history WHILE ACTIVE, before any release', async () => {
    await activeB()
    expectBPopulated()
  })

  it('P2: leaving a positively populated B releases settled rows, reader/tail, long-lived queries and native media/result payload', async () => {
    await activeB()
    await returnA()
    expectSettledBReleased()
    expect(server().sent.filter(frame => frame.type === 'message')).toHaveLength(1)
  })

  it('P3: leaving B preserves genuinely pending input and necessary active execution/tool controls, not its settled history', async () => {
    await activeB()
    await act(async () => useChatStore.getState().sendMessage('B pending input survives navigation', { clientMessageId: 'b-pending-client' }))
    const pending = server().sent.filter((frame): frame is Extract<ClientFrame, { type: 'message' }> => frame.type === 'message' && frame.client_message_id === 'b-pending-client')
    expect(pending).toHaveLength(1)
    expect(pending[0]).toMatchObject({ session_id: B, content: 'B pending input survives navigation' })
    await sendFrames(server(), [
      { type: 'session_state', session_id: B, user_id: 'f6-phase-reader', emitted_at: STAMP, pending_approvals: [], history_state: { status: 'available' }, active_turn: { turn_id: 'b-active-turn', agent_id: 'mia', started_at: STAMP } },
      { type: 'token', session_id: B, message_id: 'b-active-reply', turn_id: 'b-active-turn', agent_id: 'mia', content: 'B is still running', seq: 12 },
      { type: 'tool_call_start', session_id: B, tool: 'bash', call_id: 'b-active-call', params: { command: 'printf still-running' }, agent_id: 'mia', seq: 13 },
    ])
    await waitFor(() => expect(bucket(B).toolCalls['b-active-call']?.status).toBe('running'))
    const liveOwners = Object.values(bucket(B).messagesById).filter(row => row.role === 'assistant' && row.turnId === 'b-active-turn')
    expect(liveOwners).toHaveLength(1)
    const liveOwnerId = liveOwners[0].id // D9 preserves generated identity; it is not the wire message_id literal.
    await returnA()
    const b = bucket(B)
    // An unacknowledged optimistic row is identified by its actual sent ID;
    // clientMessageId is an optional confirmed-row alias, not a required copy.
    expect(b.messagesById['b-pending-client']).toMatchObject({ id: 'b-pending-client', role: 'user', content: 'B pending input survives navigation' })
    expect(['sending', 'queued', 'received', 'working', 'failed'], 'pending/failed is unsettled, never silently completed or discarded').toContain(b.messagesById['b-pending-client'].deliveryStatus)
    expect(b.activeTurnId).toBe('b-active-turn')
    expect(b.isStreaming).toBe(true)
    expect(b.messagesById[liveOwnerId]).toMatchObject({ id: liveOwnerId, turnId: 'b-active-turn', content: 'B is still running', status: 'streaming' })
    expect(b.toolCallOwnerMessageId?.['b-active-call']).toBe(liveOwnerId)
    expect(b.toolCalls['b-active-call']).toMatchObject({ id: 'b-active-call', tool: 'bash', status: 'running', params: { command: 'printf still-running' } })
    expect(b.messageOrder).not.toContain('id-2')
    expect(b.messageOrder).not.toContain('b-body')
    expect(b.history.readerPositions).toStrictEqual([])
    expect(b.history.tailEntries).toStrictEqual([])
    expect(queryClient.getQueryData(['messages', B])).toBeUndefined()
    expect(queryClient.getQueryData(['session-detail', B])).toBeUndefined()
    expect(JSON.stringify(b)).not.toContain(RESULT_BODY)
    expectCompleteA()
    expect(server().sent.filter(frame => frame.type === 'message')).toHaveLength(2)
    note('B-pending-controls-preserved')
  })

  it('P4: A reconstructs its exact complete answer ONCE with no B text/tool/media contamination or navigation-as-Stop', async () => {
    await activeB()
    await returnA()
    expectCompleteA()
    expectSettledBReleased()
  })

  it('P5: real A→B→A→B performs a FRESH bounded history read and displays B own original plus newly appended row once', async () => {
    await activeB()
    await returnA()
    expectCompleteA()
    expectSettledBReleased()
    phase = 'B-reentry'
    const sent = await navigate(B)
    if (sent.since_seq === undefined) {
      freshSnapshotRequests.push(sent)
      await sendFrames(server(), snapshotReply(packet(bFreshSources, B, 5, { limit: sent.history_limit, min: sent.history_min_rows }), 12))
    } else {
      await sendFrames(server(), [
        { type: 'session_state', session_id: B, user_id: 'f6-phase-reader', emitted_at: STAMP, pending_approvals: [], history_state: { status: 'available' } },
        { type: 'user_message', session_id: B, id: 'b-fresh', client_message_id: 'b-fresh-other-client', agent_id: 'mia', content: B_FRESH, timestamp: STAMP, seq: 12 },
        { type: 'catch_up_complete', session_id: B, boot_id: 'boot-1', seq: 12, mode: 'incremental' },
      ])
    }
    await screen.findByText(B_FRESH, { exact: true })
    await waitFor(() => expect(bucket(B).history.historyLoaded).toBe(true))
    expect(freshSnapshotRequests.length + bReads.filter(read => read.phase === 'B-reentry' && read.mode === 'latest').length, 'a fresh bounded WS or REST read, not a surviving cache or incremental-only tail').toBe(1)
    expect(navigation.map(frame => frame.session_id)).toStrictEqual([B, B, A, B]) // includes B revalidation
    expect(getMessages(bucket(B)).map(row => row.id)).toStrictEqual(['id-2', 'b-body', 'b-media-owner', 'b-fresh'])
    expect(getMessages(bucket(B)).map(row => row.content)).toStrictEqual([B_TEXT, B_BODY, 'B-only cached media reply', B_FRESH])
    expect(screen.getAllByText(B_TEXT, { exact: true })).toHaveLength(1)
    expect(screen.getAllByText(B_FRESH, { exact: true })).toHaveLength(1)
    expect(bucket(B).history.readerPositions).toStrictEqual([B_POSITION, pos(2002), pos(2003), pos(2004)])
    expect(JSON.stringify(bucket(B))).not.toContain(f6.expect.user_message)
    expect(JSON.stringify(bucket(B))).not.toContain(A_TEXT)
    expect(server().sent.filter(frame => frame.type === 'message')).toHaveLength(1)
    expect(server().sent.filter(frame => frame.type === 'cancel')).toStrictEqual([])
    note('B-fresh-reentry')
  })
})
