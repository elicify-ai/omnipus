// Sign-in sent attach_session twice for the same session (workspace entry's
// attachToSession AND the connect-time reattachActiveSession), so the gateway
// replayed the whole transcript twice (92 chat.replay_dedup_skipped warnings,
// test gateway 2026-10-11). Contract: while one attach's replay is in flight
// on a connection, the other path does not send a second attach_session for
// the same session; a replay that has ended, a disconnect, or a different
// session allows a fresh attach.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { reattachActiveSession } from './OmnipusRuntimeProvider'
import type { ReattachSender } from './OmnipusRuntimeProvider'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { queryClient } from '@/lib/queryClient'

const SID = 'sess-single-attach'

type Sent = { type: string; session_id?: string }

function openConnection(): { sent: Sent[]; conn: ReattachSender } {
  const sent: Sent[] = []
  const conn = { send: vi.fn((f: Sent) => { sent.push(f); return true }) }
  // WsLifecycle marks the transport connected before either attach path runs.
  useConnectionStore.setState({ connection: conn as never, isConnected: true })
  return { sent, conn: conn as ReattachSender }
}

const attaches = (sent: Sent[]) => sent.filter((f) => f.type === 'attach_session' && f.session_id === SID)

beforeEach(() => {
  localStorage.clear()
  useChatStore.setState({ sessionsById: {}, messages: [], isReplaying: false })
  useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null, sessionByWorkspace: {}, workspaceEntry: null })
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-single' })
  useConnectionStore.setState({ connection: null, isConnected: false })
})

describe('one owner of the initial attach', () => {
  it('entry attach, then connect-time reattach: exactly one attach_session', () => {
    const { sent, conn } = openConnection()
    expect(useSessionStore.getState().attachToSession(SID, 'chat', undefined, 'jim')).toBe(true)
    reattachActiveSession(conn, vi.fn())
    expect(attaches(sent)).toHaveLength(1)
  })

  it('connect-time reattach, then entry attach: exactly one attach_session', () => {
    const { sent, conn } = openConnection()
    useSessionStore.setState({ activeSessionId: SID })
    reattachActiveSession(conn, vi.fn())
    expect(useSessionStore.getState().attachToSession(SID, 'chat', undefined, 'jim')).toBe(true)
    expect(attaches(sent)).toHaveLength(1)
  })

  it('a reattach-started replay marks the bucket replaying', () => {
    const { conn } = openConnection()
    useSessionStore.setState({ activeSessionId: SID })
    reattachActiveSession(conn, vi.fn())
    expect(useChatStore.getState().sessionsById[SID]?.isReplaying).toBe(true)
  })

  it('after the replay ends, a later attach for the same session is sent again', () => {
    const { sent } = openConnection()
    useSessionStore.getState().attachToSession(SID, 'chat', undefined, 'jim')
    vi.useFakeTimers()
    try {
      useChatStore.getState().setReplaying(false)
      vi.runAllTimers()
    } finally {
      vi.useRealTimers()
    }
    useSessionStore.getState().attachToSession(SID, 'chat', undefined, 'jim')
    expect(attaches(sent)).toHaveLength(2)
  })

  it('a reattach-started replay is covered by the sessions-refetch gate (at most one refresh)', () => {
    const { conn } = openConnection()
    useSessionStore.setState({ activeSessionId: SID })
    const spy = vi.spyOn(queryClient, 'invalidateQueries')
    reattachActiveSession(conn, vi.fn())
    for (let i = 0; i < 40; i++) {
      useChatStore.getState().handleFrame({ type: 'subagent_state', span_id: `s${i}`, state: 'queued', created_at: '2026-10-11T08:30:00Z', session_id: SID } as never)
    }
    useChatStore.getState().handleFrame({ type: 'done', session_id: SID } as never)
    const n = spy.mock.calls.filter(([a]) => JSON.stringify((a as { queryKey?: unknown })?.queryKey) === '["sessions"]').length
    expect(n).toBe(1)
    spy.mockRestore()
  })

  it('a reconnect (new socket generation on the same connection) re-attaches even while the old replay flag is still up', () => {
    const { sent, conn } = openConnection()
    const c = conn as ReattachSender & { socketGeneration: number }
    c.socketGeneration = 1
    useSessionStore.setState({ activeSessionId: SID })
    reattachActiveSession(c, vi.fn())
    expect(attaches(sent)).toHaveLength(1)
    c.socketGeneration = 2 // the socket dropped and reopened
    reattachActiveSession(c, vi.fn())
    expect(attaches(sent)).toHaveLength(2)
  })

  it('a different connection object is a different transport: its attach is sent', () => {
    const a = openConnection()
    useSessionStore.setState({ activeSessionId: SID })
    reattachActiveSession(a.conn, vi.fn())
    const b = openConnection()
    reattachActiveSession(b.conn, vi.fn())
    expect(attaches(b.sent)).toHaveLength(1)
  })
})
