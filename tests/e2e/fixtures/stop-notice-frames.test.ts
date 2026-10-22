import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ClientFrameTypes, parseFrameSafe, type ServerFrame, type WsConnection, type WsFrame } from '@/lib/ws'
import { NotificationList } from '@/lib/api/generated/schemas'
import { useChatStore, getMessages } from '@/store/chat'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { isTurnFinished } from '@/store/chat/routing'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useNotificationsStore } from '@/store/notifications'
import { useWorkspacesStore } from '@/store/workspacesStore'
import inputs from './stop-notice-frames.json'

// Oracle: round-2 requirement 1. JSON is INPUT captured from actual Go Stop
// producers/REST projection, paired by the Go test, never the expected state.
// Real: WS schema parser, chat router/reducer, buckets and notification store.
// Fake: socket send edge only. This file lives in an existing CI fixture group.
const OWNER = inputs.owner_session_id
const OTHER = 'stop-notice-other-live-chat'
const PARTIAL = 'A newer reply is still running.'
const CONTINUATION = ' It continued after the notice.'
const socket = { send: vi.fn<WsConnection['send']>() }

function reset() {
  act(() => {
    useChatStore.getState().clearStreamingState()
    __resetFinishedTurnIdsForTests()
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useNotificationsStore.setState({ byId: {}, order: [], unreadCount: 0 })
    useWorkspacesStore.setState({ activeWorkspaceId: 'stop-notice-workspace' })
    localStorage.clear()
    sessionStorage.clear()
    socket.send.mockReset().mockReturnValue(true)
    useConnectionStore.getState().setConnection(socket as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
}

beforeEach(reset)
afterEach(reset)

type ClientFrameType = (typeof ClientFrameTypes)[number]

// parseFrameSafe runs the generated WsFrame schema and still returns the broad
// wire union. A predicate on the extracted `.type` string does not narrow that
// union, so the predicate below names the exclusion itself. Returning the
// result as ServerFrame is then checked: a non-server member that is not in
// the generated client list fails compilation instead of being cast away.
// The runtime list is the same ClientFrameTypes set src/lib/ws.ts::_parseServerFrame uses.
function isClientDirection(type: string): type is ClientFrameType {
  return (ClientFrameTypes as readonly string[]).includes(type)
}

function isServerDirection(frame: WsFrame): frame is Exclude<WsFrame, { type: ClientFrameType }> {
  return !isClientDirection(frame.type)
}

function requireServerFrame(frame: WsFrame): ServerFrame {
  if (!isServerDirection(frame)) {
    throw new Error(`client-direction frame must not reach the chat reducer: ${frame.type}`)
  }
  return frame
}

function deliver(raw: unknown) {
  const frame = parseFrameSafe(raw)
  expect(frame, 'actual producer input must pass the same generated schema the SPA uses').not.toBeNull()
  if (!frame) throw new Error('producer input failed runtime schema validation')
  act(() => useChatStore.getState().handleFrame(requireServerFrame(frame)))
}

function beginReply(sid: string, turn: string) {
  act(() => useSessionStore.getState().setActiveSession(sid, 'mia'))
  deliver({
    type: 'session_state', session_id: sid, user_id: 'stop-helper-owner',
    emitted_at: '2026-10-22T08:00:00Z', pending_approvals: [],
    active_turn: { turn_id: turn, agent_id: 'mia', started_at: '2026-10-22T08:00:00Z' },
  })
  deliver({ type: 'token', session_id: sid, agent_id: 'mia', turn_id: turn, content: PARTIAL })
  const bucket = useChatStore.getState().sessionsById[sid]
  expect(bucket.isStreaming).toBe(true)
  expect(bucket.activeTurnId).toBe(turn)
  expect(getMessages(bucket).map((message) => message.content)).toContain(PARTIAL)
}

function replyState(sid: string) {
  const b = useChatStore.getState().sessionsById[sid]
  return structuredClone({
    isStreaming: b.isStreaming,
    activeTurnId: b.activeTurnId,
    activeTurnAgentId: b.activeTurnAgentId,
    messages: getMessages(b),
    toolCalls: b.toolCalls,
    toolCallOrder: b.toolCallOrder,
    sessionTokens: b.sessionTokens,
    sessionCost: b.sessionCost,
  })
}

const scenarios = [
  ['approved', 1],
  ['partial_and_failure', 2],
  ['settlement_failure', 1],
  ['latch_expired', 1],
  ['latch_no_session', 1],
] as const

describe('actual Stop outcome frames are non-terminal operational notices', () => {
  for (const [name, count] of scenarios) {
    for (const active of [OWNER, OTHER]) {
      it(`${name}, active=${active}: preserves both live replies and their identities`, () => {
        const ownerTurn = `${name}-replacement-owner-turn`
        const otherTurn = `${name}-unrelated-current-turn`
        beginReply(OWNER, ownerTurn)
        if (name === 'partial_and_failure') {
          // Same cancellation-routing setup as the actual SPA: an outstanding
          // Stop for A, then foreground B. The partial response may acknowledge
          // A; a later failure must never fall through onto B.
          act(() => { useChatStore.getState().cancelStream(OWNER) })
        }
        beginReply(OTHER, otherTurn)
        act(() => useSessionStore.getState().setActiveSession(active, 'mia'))
        const beforeOwner = replyState(OWNER)
        const beforeOther = replyState(OTHER)
        for (const raw of inputs.cases[name]) deliver(raw)

        expect(replyState(OWNER), 'an older Stop notice must not replace/finish the newer owner reply').toStrictEqual(beforeOwner)
        expect(replyState(OTHER), 'another chat must remain completely intact').toStrictEqual(beforeOther)
        expect(isTurnFinished(OWNER, ownerTurn)).toBe(false)
        expect(isTurnFinished(OTHER, otherTurn)).toBe(false)
        const notices = useNotificationsStore.getState()
        expect(notices.order).toHaveLength(count)
        expect(notices.order.map((id) => notices.byId[id].type)).toStrictEqual(Array(count).fill('stop_notice'))
        expect(notices.order.every((id) => notices.byId[id].body.length > 0)).toBe(true)
        expect(useConnectionStore.getState().connectionError).toBeNull()

        // The unrelated live reply can still receive tokens on its original
        // bubble; no hidden markTurnFinished or replacement placeholder.
        deliver({ type: 'token', session_id: OTHER, agent_id: 'mia', turn_id: otherTurn, content: CONTINUATION })
        const otherMessages = getMessages(useChatStore.getState().sessionsById[OTHER])
        expect(otherMessages.filter((message) => message.role === 'assistant')).toHaveLength(1)
        expect(otherMessages.find((message) => message.role === 'assistant')?.content).toBe(PARTIAL + CONTINUATION)

        const reload = NotificationList.parse(inputs.lists[name])
        expect(reload.notifications).toHaveLength(count)
        act(() => useNotificationsStore.getState().hydrate(reload))
        expect(useNotificationsStore.getState().order).toHaveLength(count)
        expect(replyState(OWNER)).toStrictEqual(beforeOwner)
        expect(useChatStore.getState().sessionsById[OTHER].isStreaming).toBe(true)
      })
    }
  }

  it('keeps no-op/empty-recipient outcomes silent', () => {
    beginReply(OWNER, 'no-op-live-turn')
    const before = replyState(OWNER)
    for (const name of ['approved_zero', 'no_recipient'] as const) {
      expect(inputs.cases[name]).toStrictEqual([])
      for (const raw of inputs.cases[name]) deliver(raw)
    }
    expect(replyState(OWNER)).toStrictEqual(before)
    expect(useNotificationsStore.getState().order).toStrictEqual([])
  })

  it('instrument control: a genuine turn error still closes its own reply', () => {
    beginReply(OWNER, 'genuine-error-turn')
    for (const raw of inputs.cases.real_turn_error_control) deliver(raw)
    const b = useChatStore.getState().sessionsById[OWNER]
    expect(b.isStreaming).toBe(false)
    expect(b.activeTurnId).toBeNull()
    expect(getMessages(b).filter((message) => message.role === 'assistant').map(({ content, status }) => ({ content, status })))
      .toStrictEqual([{ content: 'A real turn failed.', status: 'error' }])
    expect(isTurnFinished(OWNER, 'genuine-error-turn')).toBe(true)
  })
})
