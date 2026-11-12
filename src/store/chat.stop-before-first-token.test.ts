import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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

// #1787: Stop pressed before the first token. Live must read like the reload.
const SID = 'session-1787'
const WORKSPACE = 'ws-1787'
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
  role: m.role, status: m.status, text: String(m.content ?? '').slice(0, 80), streaming: !!m.isStreaming,
}))
const llm = { llm_error: { code: 'turn_canceled', message: STOP_COPY, retryable: true } }

const reading = (rows: ReturnType<typeof shape>) => rows.filter(r => r.text !== '').map(({ role, text }) => ({ role, text }))

function liveStopBeforeFirstToken() {
  seedCanonicalSession(SID, 'jim', WORKSPACE)
  act(() => { useChatStore.getState().sendMessage('Hello', { clientMessageId: 'u1' }) })
  act(() => { useChatStore.getState().cancelStream() })
  act(() => {
    useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: STOP_COPY, payload: llm } as never)
    useChatStore.getState().handleFrame({ type: 'done', session_id: SID } as never)
  })
  return shape()
}

async function reloadedStopBeforeFirstToken() {
  const rows = [
    entry(1, { id: 'u1', role: 'user', content: 'Hello', client_message_id: 'u1', agent_id: 'jim' }),
    entry(2, { id: 'sys1', type: 'system', role: undefined, content: STOP_COPY, status: 'error', agent_id: 'jim' } as never),
    entry(3, { id: 'tc1', type: 'turn_canceled', role: undefined, content: undefined, agent_id: '', turn_id: 'jim-turn-1' } as never),
  ]
  reset()
  act(() => { useSessionStore.getState().setActiveSession(SID, 'jim') })
  await act(async () => { await reopenFixtureHistory(useChatStore, SID, page(rows, { sessionId: SID, rows: 2 })) })
  return shape()
}

describe('#1787 stop before the first token: live and reload read the same', () => {
  it('shows the stop notice live, not an empty interrupted bubble', async () => {
    const live = reading(liveStopBeforeFirstToken())
    const reloaded = reading(await reloadedStopBeforeFirstToken())
    expect(reloaded.map(r => r.text)).toStrictEqual(['Hello', STOP_COPY])
    expect(live).toStrictEqual(reloaded)
  })
  it('shows no empty bubble live', () => {
    const live = liveStopBeforeFirstToken()
    expect(live.filter(r => r.role === 'assistant' && r.text === '')).toHaveLength(0)
  })
  it('makes the empty bubble a real notice: error status, code, owner agent, time of the stop', () => {
    liveStopBeforeFirstToken()
    const m = getMessages(useChatStore.getState().sessionsById[SID]).filter(r => r.role === 'assistant')
    expect(m).toHaveLength(1)
    expect(m[0].status).toBe('error')
    expect(m[0].errorCode).toBe('turn_canceled')
    expect(m[0].agentId).toBe('jim')
  })
  it('keeps a tool-only reply interrupted: the notice does not take its bubble', () => {
    seedCanonicalSession(SID, 'jim', WORKSPACE)
    const feed = (f: Record<string, unknown>) => useChatStore.getState().handleFrame({ session_id: SID, ...f } as never)
    act(() => { useChatStore.getState().sendMessage('List it', { clientMessageId: 'u1' }) })
    act(() => { feed({ type: 'tool_call_start', tool: 'list_directory', call_id: 'c1', params: { path: '.' }, turn_id: 'jim-turn-1' }) })
    act(() => { useChatStore.getState().cancelStream() })
    act(() => { feed({ type: 'error', message: STOP_COPY, payload: llm }); feed({ type: 'done' }) })
    const withTool = getMessages(useChatStore.getState().sessionsById[SID]).filter(r => r.role === 'assistant' && ((r.tool_calls?.length ?? 0) > 0))
    expect(withTool).toHaveLength(1)
    expect(withTool[0].tool_calls?.[0]?.id).toBeTruthy()
    expect(withTool[0].content ?? '').toBe('')
  })
  it('does not move or rewrite an earlier turn\'s notice when another turn\'s stop arrives', () => {
    seedCanonicalSession(SID, 'jim', WORKSPACE)
    const feed = (f: Record<string, unknown>) => useChatStore.getState().handleFrame({ session_id: SID, ...f } as never)
    act(() => { useChatStore.getState().sendMessage('One', { clientMessageId: 'u1' }) })
    act(() => { useChatStore.getState().cancelStream() })
    act(() => { feed({ type: 'error', message: STOP_COPY, payload: llm }); feed({ type: 'done' }) })
    // A second user message arrives (another tab) before any assistant row exists for it.
    act(() => { feed({ type: 'user_message', id: 'u2', content: 'Two', timestamp: new Date().toISOString() }) })
    act(() => { feed({ type: 'error', message: STOP_COPY, payload: llm }) })
    const rows = getMessages(useChatStore.getState().sessionsById[SID])
    const first = rows.findIndex(r => String(r.content).includes('stopped'))
    const u2 = rows.findIndex(r => r.content === 'Two')
    expect(first).toBeGreaterThanOrEqual(0)
    expect(first).toBeLessThan(u2)
  })
  it('keeps the stopped turn\'s owner when Stop clears the turn before the error frame (background chat)', () => {
    seedCanonicalSession(SID, 'jim', WORKSPACE)
    act(() => { useChatStore.getState().sendMessage('Hello', { clientMessageId: 'u1' }) })
    act(() => { useChatStore.setState((s) => ({ sessionsById: { ...s.sessionsById, [SID]: { ...s.sessionsById[SID], activeTurnAgentId: 'jim' } } })) })
    act(() => { useChatStore.getState().cancelStream() })
    act(() => { useSessionStore.setState({ activeAgentId: 'mia' } as never) })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: STOP_COPY, payload: llm } as never)
      useChatStore.getState().handleFrame({ type: 'done', session_id: SID } as never)
    })
    const a = getMessages(useChatStore.getState().sessionsById[SID]).filter(r => r.role === 'assistant')
    expect(a).toHaveLength(1)
    expect(a[0].status).toBe('error')
    expect(a[0].agentId).toBe('jim')
  })
  it('dates the notice at the stop, not at Send', () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(new Date('2026-11-11T12:00:00Z'))
      seedCanonicalSession(SID, 'jim', WORKSPACE)
      act(() => { useChatStore.getState().sendMessage('Hello', { clientMessageId: 'u1' }) })
      vi.setSystemTime(new Date('2026-11-11T12:00:20Z'))
      act(() => { useChatStore.getState().cancelStream() })
      act(() => {
        useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: STOP_COPY, payload: llm } as never)
        useChatStore.getState().handleFrame({ type: 'done', session_id: SID } as never)
      })
      const a = getMessages(useChatStore.getState().sessionsById[SID]).filter(r => r.role === 'assistant')
      expect(a[0].timestamp).toBe('2026-11-11T12:00:20.000Z')
    } finally { vi.useRealTimers() }
  })
  it('combined with #1789: the saved notice replayed after a stop before the first token gives ONE row', () => {
    liveStopBeforeFirstToken()
    act(() => {
      useChatStore.getState().handleFrame({ type: 'replay_error', session_id: SID, entry_id: 'sys1', agent_id: 'jim',
        timestamp: new Date().toISOString(), kind: 'error', message: STOP_COPY, payload: llm } as never)
    })
    const rows = getMessages(useChatStore.getState().sessionsById[SID]).filter(r => String(r.content).includes('stopped'))
    expect(rows).toHaveLength(1)
    expect(rows[0].errorEntryId).toBe('sys1')
    // The same saved entry again changes nothing.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'replay_error', session_id: SID, entry_id: 'sys1', agent_id: 'jim',
        timestamp: new Date().toISOString(), kind: 'error', message: STOP_COPY, payload: llm } as never)
    })
    expect(getMessages(useChatStore.getState().sessionsById[SID]).filter(r => String(r.content).includes('stopped'))).toHaveLength(1)
  })
  it('background chat with no known owner keeps today\'s row, so a saved copy is not shown twice', () => {
    seedCanonicalSession(SID, 'jim', WORKSPACE)
    act(() => { useChatStore.getState().sendMessage('Hello', { clientMessageId: 'u1' }) })
    act(() => { useSessionStore.getState().setActiveSession('other-session', 'mia') })
    act(() => { useChatStore.getState().cancelStream(SID) })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: STOP_COPY, payload: llm } as never)
      useChatStore.getState().handleFrame({ type: 'done', session_id: SID } as never)
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'replay_error', session_id: SID, entry_id: 'sys1', agent_id: 'jim',
        timestamp: new Date().toISOString(), kind: 'error', message: STOP_COPY, payload: llm } as never)
    })
    const rows = getMessages(useChatStore.getState().sessionsById[SID]).filter(r => r.role === 'assistant' && String(r.content).includes('stopped'))
    expect(rows).toHaveLength(1)
  })
})
