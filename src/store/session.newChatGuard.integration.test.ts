/**
 * T-03 the real "+ New chat" path: startNewSession goes through decideNewChat.
 *
 * Oracles: FR-005, BDD-03.1, BDD-E02, dataset N09.
 * An unconfirmed first send with no real session id prompts. Declining keeps
 * the original text and client message id and does not touch the saved
 * pointer. Confirming abandons that delivery and starts an extra. The main
 * pointer the sidebar row click uses (`mainPointerByPair`, key
 * `${workspaceId}::${agentId}`) stays the seam main. `__pending` is never
 * that pointer. Choice is passed as `{ choice: 'decline' | 'confirm' }`.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from './session'
import { useChatStore } from './chat'
import { useWorkspacesStore } from './workspacesStore'
import { useConnectionStore } from './connection'

const MAIN = 'seam-main-mia'
const EXTRA = 'extra-real'
const WS = 'operations'
const ORIGINAL = 'Ship the launch notes'
const CLIENT = 'client-1'
const PAIR = `${WS}::mia`
const POINTER_KEY = 'omnipus.sessionByWorkspace.v1'

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: () => MAIN,
  isMainSession: (session: unknown) => {
    const id = typeof session === 'string'
      ? session
      : session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
    return id === MAIN
  },
  sessionAttention: () => 'off' as const,
  attachAckFields: () => ({}),
  attentionBoundOfFrame: () => undefined,
}))

function resetAll() {
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
    mainPointerByPair: undefined,
    newChatPrompt: undefined,
  } as never)
  useWorkspacesStore.setState({ activeWorkspaceId: WS })
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
  useChatStore.setState({
    pendingFirstSend: null,
    abandonedFirstSendIds: [],
    isStreaming: false,
    isReplaying: false,
    sessionsById: {},
  })
  localStorage.clear()
}

function armPending(status: 'unconfirmed' | 'sending' | 'retrying' | 'not_saved' | 'check_failed') {
  useSessionStore.getState().setWorkspaceSessionDescriptor(WS, {
    id: MAIN,
    type: 'chat',
    title: 'Mia main',
    agentId: 'mia',
  })
  useSessionStore.setState({
    activeSessionId: '__pending',
    activeAgentId: 'mia',
    mainPointerByPair: { [PAIR]: MAIN },
  } as never)
  useChatStore.setState({
    pendingFirstSend: {
      clientMessageId: CLIENT,
      payload: {
        type: 'message',
        content: ORIGINAL,
        client_message_id: CLIENT,
        agent_id: 'mia',
      },
      workspaceId: WS,
      attemptGeneration: 1,
      sessionId: null,
      assistantPlaceholderId: 'asst-1',
      status,
    },
    abandonedFirstSendIds: [],
  })
  useChatStore.getState().appendMessage({
    id: CLIENT,
    role: 'user',
    content: ORIGINAL,
    timestamp: '2026-01-01T00:00:00Z',
    clientMessageId: CLIENT,
  })
}

function startNewChat(choice?: 'decline' | 'confirm') {
  const start = useSessionStore.getState().startNewSession as unknown as (
    choice?: { choice: 'decline' | 'confirm' },
  ) => void
  if (choice) start({ choice })
  else start()
}

function snapshot() {
  const state = useSessionStore.getState() as {
    mainPointerByPair?: Record<string, string>
    newChatPrompt?: unknown
  }
  const pending = useChatStore.getState().pendingFirstSend
  const raw = localStorage.getItem(POINTER_KEY)
  const persisted = raw ? (JSON.parse(raw) as Record<string, { id?: string } | null>)[WS] : undefined
  return {
    clientMessageId: pending?.clientMessageId ?? null,
    text: pending?.payload.content ?? null,
    pointerId: useSessionStore.getState().sessionByWorkspace[WS]?.id ?? null,
    persistedId: persisted?.id ?? null,
    activeSessionId: useSessionStore.getState().activeSessionId,
    mainPointer: state.mainPointerByPair?.[PAIR] ?? null,
    prompt: state.newChatPrompt ?? null,
    abandoned: useChatStore.getState().abandonedFirstSendIds,
  }
}

describe('startNewSession behind decideNewChat (T-03, N09)', () => {
  beforeEach(resetAll)

  it.each(['unconfirmed', 'sending', 'retrying', 'not_saved', 'check_failed'] as const)(
    'BDD-03.1 a %s first send with no real id prompts and does not start the extra',
    (status) => {
      armPending(status)
      startNewChat()
      expect(snapshot()).toEqual({
        clientMessageId: CLIENT,
        text: ORIGINAL,
        pointerId: MAIN,
        persistedId: MAIN,
        activeSessionId: '__pending',
        mainPointer: MAIN,
        prompt: {
          action: 'prompt',
          mainSessionId: MAIN,
          clientMessageId: CLIENT,
          started: false,
        },
        abandoned: [],
      })
    },
  )

  it('N09 / BDD-E02 declining keeps the original delivery and does not touch the saved pointer', () => {
    armPending('unconfirmed')
    startNewChat('decline')
    expect(snapshot()).toEqual({
      clientMessageId: CLIENT,
      text: ORIGINAL,
      pointerId: MAIN,
      persistedId: MAIN,
      activeSessionId: '__pending',
      mainPointer: MAIN,
      prompt: null,
      abandoned: [],
    })
  })

  it('BDD-03.1 confirming abandons that delivery and does not replace the main pointer', () => {
    armPending('unconfirmed')
    startNewChat('confirm')
    expect(snapshot()).toEqual({
      clientMessageId: null,
      text: null,
      pointerId: MAIN,
      persistedId: MAIN,
      activeSessionId: null,
      mainPointer: MAIN,
      prompt: null,
      abandoned: [CLIENT],
    })
  })

  it('BDD-03.1 with no unconfirmed delivery starts an extra and leaves the main pointer in place', () => {
    useSessionStore.getState().setWorkspaceSessionDescriptor(WS, {
      id: MAIN,
      type: 'chat',
      title: 'Mia main',
      agentId: 'mia',
    })
    useSessionStore.setState({
      activeSessionId: MAIN,
      activeAgentId: 'mia',
      mainPointerByPair: { [PAIR]: MAIN },
    } as never)
    startNewChat()
    expect(snapshot()).toMatchObject({
      pointerId: MAIN,
      persistedId: MAIN,
      mainPointer: MAIN,
      prompt: null,
    })
    expect(snapshot().activeSessionId).not.toBe(MAIN)
  })

  it('an attached extra never replaces the main pointer the sidebar row click uses', () => {
    const send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({
      connection: { send, close: vi.fn(), isConnected: true } as never,
      isConnected: true,
    })
    useSessionStore.getState().attachToSession(MAIN, 'chat', 'Mia main', 'mia')
    useSessionStore.getState().attachToSession(EXTRA, 'chat', 'Launch notes', 'mia')
    const state = useSessionStore.getState()
    const mainPointer = (state as { mainPointerByPair?: Record<string, string> }).mainPointerByPair?.[PAIR] ?? null
    expect({
      remembered: state.sessionByWorkspace[WS]?.id ?? null,
      mainPointer,
      pendingSaved: state.sessionByWorkspace[WS]?.id === '__pending',
    }).toEqual({
      remembered: EXTRA,
      mainPointer: MAIN,
      pendingSaved: false,
    })
  })
})
