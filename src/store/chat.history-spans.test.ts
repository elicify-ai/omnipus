// #1266 helper spans in paged history. Persisted subagent_start/state/end are
// separate transcript entries, each with its own history position. The paged
// snapshot reduced every position in isolation, so a span's state and end
// never met its start (a delegation stayed "running" forever, with span
// warnings); REST pages mapped no span at all. Oracle (team-lead, Q5/Q6 =
// "keep today's replay placement on the start entry's own row"):
//  (a) start, state and end on one page: the span finishes with its final
//      state, no span warnings, and no extra visible rows;
//  (b) start on an older page: no row until that page loads; state and end
//      apply silently; after Show earlier exactly one start row carries the
//      latest state and end;
//  (c) an older page over REST renders the span exactly like the socket path;
//  (d) the server's row count still matches (the fact rows stay, silent).
// Real transport, store, REST client and history actions; only the socket,
// fetch and clock are substituted.
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'
import { createElement, type ReactNode } from 'react'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useRunningActivity } from '@/hooks/useRunningActivity'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { ServerFrame, SessionStateFrame, SubagentEndFrame, SubagentStartFrame, SubagentStateFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import type { ChatMessage, SubagentSpan } from './chat'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { SID, STAMP, entry, expectValidPage, page, pos, replay, snapshot } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

const SPAN = 'span_call-audit'
const origin = (rank: number, id: string) => ({ history_origin: { position: pos(rank), entry_id: id } })
const startFrame: SubagentStartFrame = { type: 'subagent_start', session_id: SID, span_id: SPAN, parent_call_id: 'call-audit', task_label: 'Audit the pages', agent_id: 'jim', child_session_id: 'child-session-1' }
const stateFrame: SubagentStateFrame = { type: 'subagent_state', session_id: SID, span_id: SPAN, child_session_id: 'child-session-1', state: 'running', created_at: '2026-10-21T10:00:05Z' }
const endFrame: SubagentEndFrame = { type: 'subagent_end', session_id: SID, span_id: SPAN, status: 'success', duration_ms: 4200, final_result: 'All pages audited' }

const question = entry(1, { id: 'question', role: 'user', content: 'Audit the site with a helper' })
// Persisted lifecycle entries as REST carries them (Message.subagent_*).
const startEntry = entry(2, { id: 'span-start', role: 'system', type: 'system', content: '', system_subtype: 'subagent_start', subagent_start: startFrame })
const stateEntry = entry(3, { id: 'span-state', role: 'system', type: 'system', content: '', system_subtype: 'subagent_state', subagent_state: stateFrame })
const endEntry = entry(4, { id: 'span-end', role: 'system', type: 'system', content: '', system_subtype: 'subagent_end', subagent_end: endFrame })
const later = Array.from({ length: 20 }, (_, i) => entry(5 + i, { id: `later-${i}`, role: 'assistant', type: 'message', content: `Later answer ${i}` }))

const available: SessionStateFrame = { type: 'session_state', session_id: SID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' } }
const complete = { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'snapshot' } as const
const SPAN_WARNING = /subagent_(start|state|message|end)|span index miss|unknown span/

let transport: ReturnType<typeof historyTransport> | undefined
let warn: MockInstance<typeof console.warn>
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(STAMP)
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
})
afterEach(() => {
  transport?.connection.disconnect(); transport = undefined
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals()
  vi.useRealTimers()
  warn.mockRestore()
})

function bucket() {
  const b = useChatStore.getState().sessionsById[SID]
  if (!b) throw new Error('no chat bucket for the test session')
  return b
}
/** Rows the list draws: a saved assistant row with nothing of its own is silent. */
function visibleRows(): ChatMessage[] {
  const b = bucket()
  return b.messageOrder.map((id) => b.messagesById[id]).filter((row) => !(row.role === 'assistant' && row.historyPosition && !row.isStreaming && !row.content && !row.tool_calls?.length && !row.spans?.length))
}
function spanRows(): { id: string; spans: SubagentSpan[] }[] {
  const b = bucket()
  return b.messageOrder.flatMap((id) => (b.messagesById[id].spans?.length ? [{ id, spans: b.messagesById[id].spans! }] : []))
}
function spanWarnings() {
  return warn.mock.calls.filter((call) => SPAN_WARNING.test(String(call[0])))
}
function settled(span: SubagentSpan) {
  const { lastUpdateAt, ...rest } = span
  void lastUpdateAt
  return rest
}
function response(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
function attach(entries: Message[], frames: ServerFrame[], drawnRows: number, earlier: boolean, context: Parameters<typeof page>[1] = {}) {
  const latest = page(entries, { earlier, rows: drawnRows, ...context, info: { limit: 20, min_rows: Math.min(20, drawnRows), ...context.info } })
  expectValidPage(latest, entries.map((source) => source.history_position!), drawnRows)
  transport = historyTransport()
  transport.attach()
  transport.deliver(snapshot(latest), available, ...frames, { type: 'done', session_id: SID }, complete)
  expect(bucket().history.historyLoaded, 'the snapshot page committed (server row count matched)').toBe(true)
}

// The end is authoritative (team-lead r23): a recorded success settles the
// child's last in-progress lifecycle ('running') to 'completed', so every
// lifecycle reader (Activity status, running-children count, panel dot)
// agrees the helper finished.
const FINISHED = {
  spanId: SPAN, parentCallId: 'call-audit', taskLabel: 'Audit the pages', agentId: 'jim', childSessionId: 'child-session-1',
  status: 'success', durationMs: 4200, finalResult: 'All pages audited', lifecycleState: 'completed', hasRun: true,
}

/** What the user sees: the real Activity consumer over the loaded chat. */
function activity() {
  useSessionStore.setState({ activeAgentId: null }) // no task-run polling in this store-level check
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(['agents'], [])
  const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children)
  const { result, unmount } = renderHook(() => useRunningActivity(), { wrapper })
  const view = result.current
  unmount()
  return view
}
function expectFinishedInActivity() {
  const view = activity()
  expect(view.running.filter((item) => item.key === SPAN), 'the finished helper is not open work').toStrictEqual([])
  expect(view.runningChildren, 'the pill counts no running helper').toBe(0)
  expect(view.recentlyFinished.find((item) => item.key === SPAN), 'the helper is listed as finished').toMatchObject({ kind: 'agent', status: 'success', lifecycleState: 'completed', durationMs: 4200 })
}

describe('helper spans across paged history', () => {
  it('(a)(d) same page over the socket: the span finishes on its start row, no warnings, no extra rows, row count matched', () => {
    attach([question, startEntry, stateEntry, endEntry], [
      replay(question),
      { ...startFrame, ...origin(2, 'span-start') },
      { ...stateFrame, ...origin(3, 'span-state') },
      { ...endFrame, ...origin(4, 'span-end') },
    ], 4, false)
    expect(bucket().history.readerPositions, '(d) every source position, fact rows included').toStrictEqual([pos(1), pos(2), pos(3), pos(4)])
    expect(spanRows()).toHaveLength(1)
    expect(spanRows()[0].id, 'the span sits on the start entry\'s own row').toBe('span-start')
    expect(settled(spanRows()[0].spans[0])).toMatchObject(FINISHED)
    expect(visibleRows().map((row) => row.id), 'only the question and the span row are drawn').toStrictEqual(['question', 'span-start'])
    expect(spanWarnings()).toStrictEqual([])
    expectFinishedInActivity()
  })

  it('(b) start on an older page: no row until it loads, then exactly one start row with the latest state and end', async () => {
    attach([stateEntry, endEntry, ...later], [
      { ...stateFrame, ...origin(3, 'span-state') },
      { ...endFrame, ...origin(4, 'span-end') },
      ...later.map((source) => replay(source)),
    ], 22, true, { info: { context: { spans: [{ span_id: SPAN, start: { ...startFrame, ...origin(2, 'span-start') }, state: { ...stateFrame, ...origin(3, 'span-state') }, end: { ...endFrame, ...origin(4, 'span-end') } }], duplicate_ids: [] } } })
    expect(spanRows(), 'no span row before its start page loads').toStrictEqual([])
    expect(visibleRows().map((row) => row.id)).toStrictEqual(later.map((source) => source.id))
    expect(spanWarnings(), 'state and end applied silently').toStrictEqual([])

    const older = page([question, startEntry], { later: true, rows: 2, info: { mode: 'older', limit: 20, min_rows: 2 } })
    expectValidPage(older, [pos(1), pos(2)], 2)
    vi.stubGlobal('fetch', async () => response(older))
    await useChatStore.getState().loadOlderHistory(SID)

    expect(spanRows(), 'exactly one start row').toHaveLength(1)
    expect(spanRows()[0].id).toBe('span-start')
    expect(spanRows()[0].spans, 'no duplicate span on the row').toHaveLength(1)
    expect(settled(spanRows()[0].spans[0])).toMatchObject(FINISHED)
    expect(spanWarnings()).toStrictEqual([])
    expectFinishedInActivity()
  })

  it('(c) REST renders the persisted span entries exactly like the socket path', async () => {
    attach([question, startEntry, stateEntry, endEntry], [
      replay(question),
      { ...startFrame, ...origin(2, 'span-start') },
      { ...stateFrame, ...origin(3, 'span-state') },
      { ...endFrame, ...origin(4, 'span-end') },
    ], 4, false)
    const socketSpans = spanRows().map(({ id, spans }) => ({ id, spans: spans.map(settled) }))
    const socketVisible = visibleRows().map((row) => row.id)
    transport?.connection.disconnect(); transport = undefined
    resetRealStores()
    useSessionStore.getState().setActiveSession(SID, 'mia')
    warn.mockClear()

    const rest = page([question, startEntry, stateEntry, endEntry], { rows: 4 })
    expectValidPage(rest, [pos(1), pos(2), pos(3), pos(4)], 4)
    vi.stubGlobal('fetch', async () => response(rest))
    await useChatStore.getState().loadLatestHistory(SID)

    expect(bucket().history.readerPositions).toStrictEqual([pos(1), pos(2), pos(3), pos(4)])
    expect(spanRows().map(({ id, spans }) => ({ id, spans: spans.map(settled) })), 'same span rows as the socket').toStrictEqual(socketSpans)
    expect(visibleRows().map((row) => row.id), 'same drawn rows as the socket').toStrictEqual(socketVisible)
    expect(spanWarnings()).toStrictEqual([])
  })
})

describe('the end is authoritative on the live path too', () => {
  it('live start → state running → end success: Activity shows the helper finished, not running', () => {
    useChatStore.getState().appendMessage({ id: 'live-parent', role: 'assistant', content: 'Delegating', timestamp: STAMP, status: 'streaming', isStreaming: true })
    for (const frame of [startFrame, stateFrame, endFrame] as ServerFrame[]) useChatStore.getState().handleFrame(frame)
    const span = bucket().messagesById['live-parent'].spans?.[0]
    expect(span && settled(span)).toMatchObject(FINISHED)
    expectFinishedInActivity()
    expect(spanWarnings()).toStrictEqual([])
  })
})
