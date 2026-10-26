// #1266 x #1418 ("cancelled row lost on reload"): a cancelled turn's reply that
// sits on an OLDER page than its turn_canceled marker must show as interrupted
// once the reader loads that page, exactly as when the reply is loaded
// directly with its marker. The server resolves the cancellation into the
// selected reply itself (pkg/session/chat_history_reduce.go
// deriveCancelTruncation: truncated + truncation_reason "cancelled"), on every
// carrier and whichever page holds the marker.
// Real transport, real store, real REST client and history actions; only the
// socket, fetch and clock are substituted.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { ReplayMessageFrame, ServerFrame, SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { getMessageStatusSuffix } from '@/lib/truncation'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { SID, STAMP, entry, expectValidPage, page, pos, replay, snapshot } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

const TURN = 'cancelled-turn'
const question = entry(1, { role: 'user', content: 'Draft the long report' })
const reply = entry(2, {
  id: 'cancelled-reply', role: 'assistant', type: 'message', turn_id: TURN,
  content: 'The report starts with', truncated: true, truncation_reason: 'cancelled',
})
const marker = entry(3, { id: 'cancel-marker', role: undefined, type: 'turn_canceled', turn_id: TURN, content: '' })
const later = entry(4, { id: 'later-answer', role: 'assistant', type: 'message', turn_id: 'later-turn', content: 'Next answer' })
// A real "earlier exists" latest page stops at its row target (limit 20).
const laterRows = [later, ...Array.from({ length: 19 }, (_, i) => entry(5 + i, { role: 'assistant', type: 'message', turn_id: `later-turn-${i}`, content: `Later answer ${i}` }))]

const available: SessionStateFrame = {
  type: 'session_state', session_id: SID, user_id: 'history-reader',
  pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' },
}
const complete = { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'snapshot' } as const

function replayed(source: Message): ReplayMessageFrame {
  const frame = replay(source)
  return {
    ...frame,
    ...(source.truncated ? { truncated: true } : {}),
    ...(source.truncation_reason ? { truncation_reason: source.truncation_reason } : {}),
  }
}
function markerFrame(): ReplayMessageFrame {
  return {
    type: 'replay_message', session_id: SID, id: marker.id, role: 'turn_canceled', content: '',
    timestamp: marker.timestamp, turn_id: TURN, history_origin: { position: pos(3), entry_id: marker.id },
  }
}
function response(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
function storedReply() {
  const row = useChatStore.getState().sessionsById[SID]?.messagesById['cancelled-reply']
  if (!row) throw new Error('the cancelled reply is not in the loaded history')
  return { suffix: getMessageStatusSuffix(row), status: row.status, truncationReason: row.truncationReason, content: row.content, position: row.historyPosition }
}

let transport: ReturnType<typeof historyTransport> | undefined
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(STAMP)
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
})
afterEach(() => {
  transport?.connection.disconnect(); transport = undefined
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

function attachLatest(entries: Message[], frames: ServerFrame[], drawnRows: number, earlier: boolean) {
  const latest = page(entries, { earlier, rows: drawnRows, info: { limit: 20, min_rows: 20 } })
  expectValidPage(latest, entries.map((source) => source.history_position!), drawnRows)
  transport = historyTransport()
  transport.attach()
  transport.deliver(snapshot(latest), available, ...frames, { type: 'done', session_id: SID }, complete)
  expect(useChatStore.getState().sessionsById[SID].history.historyLoaded, 'the latest page committed').toBe(true)
}

async function loadDirectly() {
  // Reply and marker on the same latest page; marker rows weigh zero.
  attachLatest([question, reply, marker, later], [replay(question), replayed(reply), markerFrame(), replay(later)], 3, false)
  return storedReply()
}

describe('#1418 family: a cancelled reply on an older page shows as interrupted once loaded', () => {
  it('direct load: the reply loaded with its marker shows (interrupted)', async () => {
    const direct = await loadDirectly()
    expect(direct.suffix).toBe('(interrupted)')
    expect(direct.content).toBe('The report starts with')
  })

  it('older page: the reply loaded after its marker shows exactly what a direct load shows', async () => {
    const direct = await loadDirectly()
    transport?.connection.disconnect(); transport = undefined
    resetRealStores()
    useSessionStore.getState().setActiveSession(SID, 'mia')

    // The marker and a later answer are the latest page; the reply is earlier.
    attachLatest([marker, ...laterRows], [markerFrame(), ...laterRows.map((source) => replay(source))], 20, true)
    expect(useChatStore.getState().sessionsById[SID].messagesById['cancelled-reply'], 'reply not loaded yet').toBeUndefined()

    const older = page([question, reply], { later: true, rows: 2, info: { mode: 'older', limit: 20, min_rows: 2 } })
    expectValidPage(older, [pos(1), pos(2)], 2)
    const reads: string[] = []
    vi.stubGlobal('fetch', async (url: RequestInfo | URL) => { reads.push(String(url)); return response(older) })
    await useChatStore.getState().loadOlderHistory(SID)

    expect(reads, 'exactly one older REST read before the marker position').toHaveLength(1)
    expect(new URL(reads[0], 'http://localhost').searchParams.get('before')).toBe(pos(3))
    expect(storedReply(), 'same interrupted display and stored state as the direct load').toStrictEqual(direct)
    expect(storedReply().suffix).toBe('(interrupted)')
  })
})
