import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ErrorFrame } from '@/lib/api/generated/asyncapi-types'
import { codeToDisplay } from '@/lib/llm-error'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { queryClient } from '@/lib/queryClient'
import { seedCanonicalSession } from '../../tests/unit/fixtures/canonical-session'
import { entry, page } from '../../tests/chat-history-paging/fixtures'
import { reopenFixtureHistory } from '../../tests/chat-history-paging/reopen-fixture-history'

// #1775: a stopped turn must read the same live and after a reload. The saved
// order below is the REAL one from the hands-on capture (Team B, validator S6,
// build d2fdf7776): the server writes the stop notice BEFORE the interrupted
// partial reply (4 ms apart), then the turn_canceled marker.
const SID = 'session-1775'
const WORKSPACE = 'ws-1775'
const STOP_COPY = codeToDisplay.turn_canceled
const sender = { send: vi.fn<WsConnection['send']>() }

function reset() {
  queryClient.clear()
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    localStorage.clear(); sessionStorage.clear()
  })
}
beforeEach(() => {
  reset()
  sender.send.mockReset().mockReturnValue(true)
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
    useSessionStore.getState().setActiveSession(SID, 'jim')
  })
})
afterEach(() => { reset(); vi.unstubAllGlobals() })

const shape = () => getMessages(useChatStore.getState().sessionsById[SID]).map(m => ({
  role: m.role, status: m.status, errorCode: (m as { errorCode?: string }).errorCode,
  text: String(m.content ?? '').slice(0, 30), tools: Object.keys((m as { toolCalls?: object }).toolCalls ?? {}).length || (m.tool_calls?.length ?? 0),
}))

function liveStoppedTurnWithTool() {
  seedCanonicalSession(SID, 'jim', WORKSPACE)
  const feed = (f: Record<string, unknown>) => useChatStore.getState().handleFrame({ session_id: SID, ...f } as never)
  act(() => {
    useChatStore.getState().sendMessage('First write three sentences, then list the directory.', { clientMessageId: 'u1' })
    feed({ type: 'token', agent_id: 'jim', content: 'A lighthouse works by light.', message_id: 'm1', turn_id: 'jim-turn-1', seq: 1 })
    feed({ type: 'tool_call_start', tool: 'list_directory', call_id: 'c1', params: { path: '.' }, turn_id: 'jim-turn-1' })
    feed({ type: 'tool_call_result', tool: 'list_directory', call_id: 'c1', status: 'success', result: { text: 'DIR: .git' }, turn_id: 'jim-turn-1' })
    feed({ type: 'token', agent_id: 'jim', content: 'The three sentences are above, and', message_id: 'm2', turn_id: 'jim-turn-1', seq: 2 })
  })
  act(() => { useChatStore.getState().cancelStream() })
  const frame: ErrorFrame = { type: 'error', session_id: SID, message: STOP_COPY,
    payload: { llm_error: { code: 'turn_canceled', message: STOP_COPY, retryable: true } } }
  act(() => { useChatStore.getState().handleFrame(frame); useChatStore.getState().handleFrame({ type: 'done', session_id: SID } as never) })
  return shape()
}

async function reloadedStoppedTurnWithTool(noticeFirst = true) {
  const rows = [
    entry(1, { id: 'u1', role: 'user', content: 'First write three sentences, then list the directory.', client_message_id: 'u1', agent_id: 'jim' }),
    entry(2, { id: 'a1', role: 'assistant', content: 'A lighthouse works by light.', agent_id: 'jim', turn_id: 'jim-turn-1' }),
    entry(3, { id: 'c1', type: 'tool_call', role: undefined, content: undefined, agent_id: 'jim', turn_id: 'jim-turn-1',
      tool_calls: [{ id: 'c1', tool: 'list_directory', status: 'success', content_state: 'full', parameters: { path: '.' }, result: { text: 'DIR: .git' } }] } as never),
  ]
  const notice = (p: number) => entry(p, { id: 'sys1', type: 'system', role: undefined, content: STOP_COPY, status: 'error', agent_id: 'jim' } as never)
  const partial = (p: number) => entry(p, { id: 'a2', role: 'assistant', content: 'The three sentences are above, and', agent_id: 'jim', turn_id: 'jim-turn-1', truncated: true, truncation_reason: 'cancelled' })
  rows.push(noticeFirst ? notice(4) : partial(4), noticeFirst ? partial(5) : notice(5),
    entry(6, { id: 'tc1', type: 'turn_canceled', role: undefined, content: undefined, agent_id: '', turn_id: 'jim-turn-1' } as never))
  const saved = page(rows, { sessionId: SID, rows: 5 })
  reset()
  act(() => { useSessionStore.getState().setActiveSession(SID, 'jim') })
  await act(async () => { await reopenFixtureHistory(useChatStore, SID, saved) })
  return shape()
}

// What the user reads: order, who, text and card count. The empty row the
// turn_canceled marker maps to is bookkeeping, and the "interrupted" label vs
// "done" is a status detail the order bug does not touch.
const reading = (rows: ReturnType<typeof shape>) =>
  rows.filter(r => r.text !== '' || r.tools > 0).map(({ role, text, tools }) => ({ role, text: text.replace(/\s+$/, ''), tools }))

describe('#1775 stopped turn: live and reload read the same', () => {
  // The engine now saves partial reply, stop notice, turn_canceled marker
  // (pkg/agent TestStoppedTurn_SavedOrderIsPartialNoticeMarker). Reload must
  // then read exactly what live showed: one reply bubble that holds its tool
  // card, followed by the notice.
  it('reloads a stopped tool turn, saved in the engine order, like live', async () => {
    const live = reading(liveStoppedTurnWithTool())
    const reloaded = reading(await reloadedStoppedTurnWithTool(false))
    expect(live).toHaveLength(3)
    expect(live[1].tools).toBe(1)
    expect(live[2].text).toContain('stopped')
    expect(reloaded).toStrictEqual(live)
  })

  // Instrument check: the same reader, fed the OLD saved order (notice before
  // the partial), does split the reply and put the notice first. This is the
  // 2026-11-11 capture, and the documented limit for sessions saved before the
  // engine fix: if this stops differing, the test above can no longer tell the
  // two orders apart.
  it('control: sessions saved in the old order still read split (documented limit)', async () => {
    const live = reading(liveStoppedTurnWithTool())
    const old = reading(await reloadedStoppedTurnWithTool(true))
    expect(old).not.toStrictEqual(live)
    expect(old.findIndex(r => r.text.includes('stopped'))).toBeLessThan(old.findIndex(r => r.text.startsWith('The three')))
  })
})
