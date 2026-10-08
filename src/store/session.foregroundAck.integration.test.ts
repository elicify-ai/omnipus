/**
 * T-14 foreground-commit acknowledgement through the frames the store sends.
 *
 * Oracles: FR-013, BDD-04.2, BDD-04.4, BDD-02.4, BDD-E03, BDD-E04,
 * datasets A06, A07, A08, founder Q-G1, foreground commitment steps 1–4.
 *
 * A shown commit is catch_up_complete for the main that is the committed
 * foreground view (document visible, seam says it is a main, attention is
 * not unknown). That frame's attach_session carries the object returned by
 * attachAckFields at that moment, and a later retry sends that same object.
 * Prefetch (document.hidden), hidden reconnect, a non-main, unknown
 * attention, a failed send, and an overtaken A do not.
 *
 * The seam fixture returns { ack_attention: true, observed_bound }.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session } from '@/lib/api'
import { useSessionStore } from './session'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'

type Signal = 'on' | 'off' | 'unknown'

const seam = vi.hoisted(() => {
  const mains = new Set<string>()
  const attention = new Map<string, Signal>()
  let observed = 'goal-1'
  return {
    mains,
    attention,
    observed: () => observed,
    setObserved: (value: string) => {
      observed = value
    },
    mainSessionIdOfMember: () => undefined as string | undefined,
    isMainSession: (session: unknown) => {
      const id = session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
      return mains.has(id)
    },
    sessionAttention: (session: unknown): Signal => {
      const id = session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
      return attention.get(id) ?? 'unknown'
    },
    attachAckFields: () => ({ ack_attention: true, observed_bound: observed }),
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: () => seam.mainSessionIdOfMember(),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: (session: unknown) => seam.sessionAttention(session),
  attachAckFields: () => seam.attachAckFields(),
}))

const hidden = { value: false }
const originalHidden = Object.getOwnPropertyDescriptor(Document.prototype, 'hidden')
  ?? Object.getOwnPropertyDescriptor(document, 'hidden')

function session(id: string, agentId = 'mia'): Session {
  return {
    id,
    agent_id: agentId,
    title: id,
    type: 'chat',
    status: 'active',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    channel: 'web',
    partitions: [],
    workspace_id: 'operations',
    stats: {
      tokens_in: 0,
      tokens_out: 0,
      tokens_total: 0,
      cost: 0,
      tool_calls: 0,
      message_count: 0,
    },
  }
}

function resetAll() {
  hidden.value = false
  seam.mains.clear()
  seam.attention.clear()
  seam.setObserved('goal-1')
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    agentSelectionSource: 'auto',
    agentSelectionWorkspaceId: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    resolvingSessionForWorkspace: {},
  })
  useWorkspacesStore.setState({ activeWorkspaceId: 'operations' })
  useChatStore.setState({
    sessionsById: {},
    isStreaming: false,
    isReplaying: false,
    pendingFirstSend: null,
  })
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
  localStorage.clear()
}

function connect() {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({
    connection: { send, close: vi.fn(), isConnected: true } as never,
    isConnected: true,
  })
  return send
}

function acks(send: ReturnType<typeof vi.fn>): Array<Record<string, unknown>> {
  return send.mock.calls
    .map((call) => call[0] as Record<string, unknown>)
    .filter((frame) => frame.ack_attention === true)
}

function show(sessionId: string) {
  useChatStore.getState().handleFrame({
    type: 'catch_up_complete',
    session_id: sessionId,
    seq: 4,
    boot_id: 'boot-1',
    mode: 'snapshot',
  })
}

describe('foreground commit acknowledgement (T-14, A06–A08)', () => {
  beforeEach(() => {
    resetAll()
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden.value })
  })

  afterEach(() => {
    if (originalHidden) {
      Object.defineProperty(Document.prototype, 'hidden', originalHidden)
    }
  })

  it('BDD-04.2 a shown main catch-up carries the seam acknowledgement and that observed bound', () => {
    const send = connect()
    const main = session('main-a')
    seam.mains.add(main.id)
    seam.attention.set(main.id, 'on')
    seam.setObserved('goal-1')
    useSessionStore.getState().attachToSession(main.id, 'chat', main.title, main.agent_id)
    show(main.id)

    expect(acks(send)).toEqual([
      {
        type: 'attach_session',
        session_id: main.id,
        ack_attention: true,
        observed_bound: 'goal-1',
      },
    ])
  })

  it('A06 / BDD-E04 a retry keeps the bound captured at the shown commit and does not pick up goal-2', () => {
    const send = connect()
    const main = session('main-a')
    seam.mains.add(main.id)
    seam.attention.set(main.id, 'on')
    seam.setObserved('goal-1')
    useSessionStore.getState().attachToSession(main.id, 'chat', main.title, main.agent_id)
    show(main.id)
    seam.setObserved('goal-2')
    show(main.id)

    expect(acks(send).map((frame) => frame.observed_bound)).toEqual(['goal-1'])
  })

  it('A08 a hidden prefetch does not replace the committed chat and does not acknowledge', () => {
    const send = connect()
    const committed = session('main-b')
    const prefetched = session('main-a')
    seam.mains.add(committed.id)
    seam.mains.add(prefetched.id)
    seam.attention.set(committed.id, 'on')
    seam.attention.set(prefetched.id, 'on')
    useSessionStore.getState().attachToSession(committed.id, 'chat', committed.title, committed.agent_id)
    send.mockClear()
    hidden.value = true
    useSessionStore.getState().attachToSession(prefetched.id, 'chat', prefetched.title, prefetched.agent_id)
    show(prefetched.id)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      acks: acks(send),
    }).toEqual({
      sessionId: committed.id,
      acks: [],
    })
  })

  it('E03 an overtaken A is not acknowledged, and a later winning A carries its own bound', () => {
    const send = connect()
    const first = session('main-a')
    const winner = session('main-b')
    seam.mains.add(first.id)
    seam.mains.add(winner.id)
    seam.attention.set(first.id, 'on')
    seam.attention.set(winner.id, 'on')
    seam.setObserved('goal-a1')
    useSessionStore.getState().attachToSession(first.id, 'chat', first.title, first.agent_id)
    seam.setObserved('goal-b')
    useSessionStore.getState().attachToSession(winner.id, 'chat', winner.title, winner.agent_id)
    show(first.id)
    show(winner.id)
    seam.setObserved('goal-a2')
    useSessionStore.getState().attachToSession(first.id, 'chat', first.title, first.agent_id)
    show(first.id)

    expect(acks(send).map((frame) => ({
      session_id: frame.session_id,
      observed_bound: frame.observed_bound,
    }))).toEqual([
      { session_id: winner.id, observed_bound: 'goal-b' },
      { session_id: first.id, observed_bound: 'goal-a2' },
    ])
  })

  it('BDD-04.4 unknown attention, a non-main, and a failed send do not acknowledge; a later shown main does', () => {
    const send = connect()
    const unknownMain = session('main-unknown')
    const helper = session('helper-1', 'jim')
    const shown = session('main-shown')
    seam.mains.add(unknownMain.id)
    seam.mains.add(shown.id)
    seam.attention.set(unknownMain.id, 'unknown')
    seam.attention.set(shown.id, 'on')
    seam.setObserved('goal-shown')
    useSessionStore.getState().attachToSession(unknownMain.id, 'chat', unknownMain.title, unknownMain.agent_id)
    show(unknownMain.id)
    useSessionStore.getState().attachToSession(helper.id, 'chat', helper.title, helper.agent_id)
    show(helper.id)
    send.mockReturnValueOnce(false)
    useSessionStore.getState().attachToSession(shown.id, 'chat', shown.title, shown.agent_id)
    send.mockReturnValue(true)
    useSessionStore.getState().attachToSession(shown.id, 'chat', shown.title, shown.agent_id)
    show(shown.id)

    expect(acks(send).map((frame) => ({
      session_id: frame.session_id,
      observed_bound: frame.observed_bound,
    }))).toEqual([
      { session_id: shown.id, observed_bound: 'goal-shown' },
    ])
  })

  it('A08 a hidden reconnect does not add an acknowledgement beyond the shown commit', () => {
    const send = connect()
    const main = session('main-a')
    seam.mains.add(main.id)
    seam.attention.set(main.id, 'on')
    seam.setObserved('goal-1')
    useSessionStore.getState().attachToSession(main.id, 'chat', main.title, main.agent_id)
    show(main.id)
    hidden.value = true
    seam.setObserved('goal-2')
    reattachActiveSession(
      { send: (frame) => send(frame) },
      () => undefined,
    )

    expect(acks(send).map((frame) => frame.observed_bound)).toEqual(['goal-1'])
  })
})
