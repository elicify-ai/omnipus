/**
 * T-13 the real "+ New chat" row. Oracles: FR-005, BDD-03.1, BDD-E02,
 * dataset N09, and the deleted screen test at fef5e42d0
 * (src/components/chat/ChatScreen.firstSendNewChat.test.tsx, cases D8).
 *
 * Behaviour: the sidebar row's New chat button is the only way to start an
 * extra. An unconfirmed first send with no real session id opens the existing
 * guard. Keep this chat retains that delivery and does not touch the main
 * pointer. Start a new chat abandons that client message id and starts an
 * extra owned by the row's agent, without replacing the main pointer.
 * A dialog opened against an older pending id cannot discard a newer message
 * that has since reused the pending slot. With no unconfirmed send, New chat
 * starts the extra directly.
 *
 * REAL: WorkspaceAgentList, ConfirmDialog, decideNewChat, beginPairExtra,
 * startNewSession, and the chat/session stores.
 * MOCK: the router (navigation is a process edge) and the session-core seam
 * (the row's main id is not on the wire yet). The test never calls
 * startNewSession itself except to set up the stale-slot replacement the
 * deleted test performed with a real store action.
 *
 * Cases: Keep (negative), Start (exact abandon + owner), stale slot
 * (negative), no pending (direct extra). No sized numeric boundary.
 * Mutations that must die: confirm passes a string and returns; confirm
 * abandons whatever id is pending now; confirm starts the extra for the
 * previously active agent; decline clears the message. Proof of those
 * mutations is deferred to CHECK, except the red run of this file.
 */
import type { ReactNode } from 'react'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session, Workspace, WorkspaceMemberConfig } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { getMessages, useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { WorkspaceAgentList } from './WorkspaceAgentList'

const WARNING = 'Delivery not confirmed. Copy your message before starting a new chat.'
const WS = 'product-launch'
const MIA_MAIN = 'seam-main-mia'
const JIM_MAIN = 'seam-main-jim'
const MIA_PAIR = `${WS}::mia`
const JIM_PAIR = `${WS}::jim`
const OLD_ID = 'nav-newchat-old'
const OLD_TEXT = 'Do not discard this unconfirmed first message.'
const NEW_ID = 'nav-newchat-new'
const NEW_TEXT = 'A newer first message must survive an old dialog choice.'
const POINTER_KEY = 'omnipus.sessionByWorkspace.v1'

const miaMember: WorkspaceMemberConfig = {}
const jimMember: WorkspaceMemberConfig = {}

const seam = vi.hoisted(() => {
  const mains = new Map<object, string>()
  return {
    mains,
    mainSessionIdOfMember: (member: object) => mains.get(member),
    isMainSession: (session: unknown) => {
      const id = session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
      return id === MIA_MAIN || id === JIM_MAIN
    },
    sessionAttention: () => 'off' as const,
    attachAckFields: () => ({}),
    attentionBoundOfFrame: (_frame: unknown) => {
      void _frame
      return undefined
    },
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: (member: object) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: () => seam.sessionAttention(),
  attachAckFields: () => seam.attachAckFields(),
  attentionBoundOfFrame: (frame: unknown) => seam.attentionBoundOfFrame(frame),
}))

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children }: { children: ReactNode }) => children,
}))

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  })
}
if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

const project: Workspace = {
  id: WS,
  name: 'Product launch',
  is_default: false,
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  revision: '0'.repeat(64),
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  member_configs: { mia: miaMember, jim: jimMember },
}

const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'Main' })
const jim = makeAgent({ id: 'jim', name: 'Jim', type: 'Main' })

function chatSession(id: string, agentId: string): Session {
  return {
    id,
    type: 'chat',
    agent_id: agentId,
    title: id,
    workspace_id: WS,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 0,
  }
}

function pointers() {
  const state = useSessionStore.getState()
  const raw = localStorage.getItem(POINTER_KEY)
  const persisted = raw ? (JSON.parse(raw) as Record<string, { id?: string } | null>)[WS] : undefined
  return {
    mia: state.mainPointerByPair[MIA_PAIR] ?? null,
    jim: state.mainPointerByPair[JIM_PAIR] ?? null,
    saved: state.sessionByWorkspace[WS]?.id ?? null,
    persisted: persisted?.id ?? null,
  }
}

function pendingBucket() {
  return useChatStore.getState().sessionsById.__pending
}

// D8 (fef5e42d0): Start a new chat removes the pending bucket synchronously.
// getMessages requires a bucket, so a missing one is absence, not an empty list
// the helper may invent by reading undefined.
function pendingUsers() {
  const bucket = pendingBucket()
  if (bucket == null) return []
  return getMessages(bucket)
    .filter((message) => message.role === 'user')
    .map(({ id, content }) => ({ id, content }))
}

function renderList() {
  render(
    <WorkspaceAgentList
      projects={[project]}
      expandedIds={new Set([WS])}
      activeId={WS}
      agents={[mia, jim]}
      rosterState="fresh"
      sessions={[chatSession(MIA_MAIN, 'mia'), chatSession(JIM_MAIN, 'jim')]}
      onToggle={() => {}}
      onOpen={() => {}}
      onOverlayClose={() => {}}
    />,
  )
}

function armUnconfirmed(activeAgentId: string) {
  act(() => {
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: '__pending',
      activeAgentId,
      mainPointerByPair: { [MIA_PAIR]: MIA_MAIN, [JIM_PAIR]: JIM_MAIN },
    }, true)
    useSessionStore.getState().setWorkspaceSessionDescriptor(WS, {
      id: MIA_MAIN,
      type: 'chat',
      title: 'Mia main',
      agentId: 'mia',
    })
    useWorkspacesStore.setState({ activeWorkspaceId: WS })
    useChatStore.setState({
      pendingFirstSend: {
        clientMessageId: OLD_ID,
        payload: {
          type: 'message',
          content: OLD_TEXT,
          client_message_id: OLD_ID,
          agent_id: 'mia',
        },
        workspaceId: WS,
        attemptGeneration: 1,
        sessionId: null,
        assistantPlaceholderId: 'asst-old',
        status: 'unconfirmed',
      },
      abandonedFirstSendIds: [],
      pendingKickoff: null,
      isStreaming: false,
      sessionsById: {},
    })
    useChatStore.getState().appendMessage({
      id: OLD_ID,
      role: 'user',
      content: OLD_TEXT,
      timestamp: '2026-01-01T00:00:00Z',
      clientMessageId: OLD_ID,
    })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
  })
}

function openMiaNewChat() {
  const row = screen.getByRole('group', { name: 'Mia' })
  fireEvent.click(within(row).getByRole('button', { name: /^New chat with Mia$/ }))
  const dialog = screen.getByRole('alertdialog', { name: 'Start a new chat?' })
  expect(within(dialog).getByText(WARNING, { exact: true }).textContent).toBe(WARNING)
  return dialog
}

describe('WorkspaceAgentList + New chat (FR-005, BDD-03.1, BDD-E02, N09)', () => {
  beforeEach(() => {
    seam.mains.clear()
    seam.mains.set(miaMember, MIA_MAIN)
    seam.mains.set(jimMember, JIM_MAIN)
    localStorage.clear()
  })

  afterEach(() => {
    cleanup()
    localStorage.clear()
  })

  it('Keep this chat retains the original id, text, and recovery request, and does not start an extra or touch the pointer', () => {
    armUnconfirmed('jim')
    const recovery = structuredClone(useChatStore.getState().pendingFirstSend)
    const before = pointers()
    renderList()

    const dialog = openMiaNewChat()
    expect(pendingUsers(), 'opening New chat must not drop the message').toEqual([{ id: OLD_ID, content: OLD_TEXT }])
    expect(useChatStore.getState().pendingFirstSend, 'opening New chat keeps the recovery request').toEqual(recovery)

    fireEvent.click(within(dialog).getByRole('button', { name: /^Keep this chat$/ }))

    expect(screen.queryByRole('alertdialog'), 'Keep this chat dismisses the guard').not.toBeInTheDocument()
    expect(pendingUsers(), 'Keep this chat preserves the original message').toEqual([{ id: OLD_ID, content: OLD_TEXT }])
    expect(useChatStore.getState().pendingFirstSend, 'Keep this chat preserves the recovery request').toEqual(recovery)
    expect(useChatStore.getState().abandonedFirstSendIds, 'Keep this chat abandons nothing').toEqual([])
    expect(useSessionStore.getState().activeSessionId, 'Keep this chat leaves the pending chat selected').toBe('__pending')
    expect(useSessionStore.getState().activeAgentId, 'Keep this chat does not start an extra for the row').toBe('jim')
    expect(pointers(), 'Keep this chat does not touch the main pointer').toEqual(before)
    expect(pointers().saved, 'N09: __pending is not the saved pointer').not.toBe('__pending')
  })

  it('Start a new chat abandons that id, starts an extra owned by this agent, and does not replace the main pointer', () => {
    armUnconfirmed('jim')
    const before = pointers()
    renderList()
    const dialog = openMiaNewChat()

    fireEvent.click(within(dialog).getByRole('button', { name: /^Start a new chat$/ }))

    const pending = useChatStore.getState().pendingFirstSend
    expect(pendingBucket(), 'D8: Start a new chat removes the pending bucket').toBeUndefined()
    expect(pendingUsers(), 'the abandoned id is gone from pending').toEqual([])
    expect({
      clientMessageId: pending?.clientMessageId ?? null,
      text: pending?.payload.content ?? null,
      bubble: pendingUsers(),
      abandoned: useChatStore.getState().abandonedFirstSendIds,
      activeSessionId: useSessionStore.getState().activeSessionId,
      owner: useSessionStore.getState().activeAgentId,
      pointers: pointers(),
    }, 'BDD-03.1 Start a new chat abandons the original delivery and starts Mia\'s extra').toEqual({
      clientMessageId: null,
      text: null,
      bubble: [],
      abandoned: [OLD_ID],
      activeSessionId: null,
      owner: 'mia',
      pointers: before,
    })
    expect(pointers().mia, 'the main pointer stays the seam main').toBe(MIA_MAIN)
    expect(pointers().saved, 'N09: the saved pointer is not the pending placeholder').toBe(MIA_MAIN)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('a stale Start a new chat cannot discard a newer message that reused the pending slot', () => {
    armUnconfirmed('mia')
    const before = pointers()
    renderList()
    const dialog = openMiaNewChat()

    const sender = { send: vi.fn().mockReturnValue(false), close: vi.fn(), isConnected: true as const }
    act(() => {
      useConnectionStore.setState({
        connection: sender as never,
        isConnected: true,
        connectionError: null,
      })
      useSessionStore.getState().startNewSession({ choice: 'confirm' })
      useChatStore.getState().sendMessage(NEW_TEXT, { clientMessageId: NEW_ID })
    })
    const newer = structuredClone(useChatStore.getState().pendingFirstSend)
    expect(pendingUsers(), 'fixture: the newer message occupies the pending slot').toEqual([{ id: NEW_ID, content: NEW_TEXT }])
    expect(newer?.clientMessageId, 'fixture: the recovery request is the newer id').toBe(NEW_ID)
    expect(useSessionStore.getState().activeSessionId, 'fixture: the newer send is the selected chat').toBe('__pending')
    expect(pointers(), 'fixture: replacing the slot did not replace the main pointer').toEqual(before)
    expect(screen.getByRole('alertdialog', { name: 'Start a new chat?' }), 'fixture: the old dialog is still open').toBe(dialog)

    fireEvent.click(within(dialog).getByRole('button', { name: /^Start a new chat$/ }))

    expect(screen.queryByRole('alertdialog'), 'the stale dialog can close').not.toBeInTheDocument()
    expect(pendingUsers(), 'D8: the stale choice preserves the newer message').toEqual([{ id: NEW_ID, content: NEW_TEXT }])
    expect(useChatStore.getState().pendingFirstSend, 'D8: the stale choice cannot replace the newer recovery request').toEqual(newer)
    expect(useChatStore.getState().abandonedFirstSendIds, 'D8: the newer id is not abandoned').not.toContain(NEW_ID)
    expect(useSessionStore.getState().activeSessionId, 'D8: the stale choice cannot leave the newer chat').toBe('__pending')
    expect(pointers(), 'the stale choice does not replace the main pointer').toEqual(before)
  })

  it('with no unconfirmed send, New chat starts the extra for this agent and leaves the main pointer in place', () => {
    act(() => {
      useSessionStore.setState({
        ...useSessionStore.getInitialState(),
        activeSessionId: MIA_MAIN,
        activeAgentId: 'jim',
        mainPointerByPair: { [MIA_PAIR]: MIA_MAIN, [JIM_PAIR]: JIM_MAIN },
      }, true)
      useSessionStore.getState().setWorkspaceSessionDescriptor(WS, {
        id: MIA_MAIN,
        type: 'chat',
        title: 'Mia main',
        agentId: 'mia',
      })
      useWorkspacesStore.setState({ activeWorkspaceId: WS })
      useChatStore.setState({
        pendingFirstSend: null,
        abandonedFirstSendIds: [],
        pendingKickoff: null,
        isStreaming: false,
        sessionsById: {},
      })
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    })
    const before = pointers()
    renderList()

    const row = screen.getByRole('group', { name: 'Mia' })
    fireEvent.click(within(row).getByRole('button', { name: /^New chat with Mia$/ }))

    expect(screen.queryByRole('alertdialog'), 'no unconfirmed send means no guard').not.toBeInTheDocument()
    expect({
      activeSessionId: useSessionStore.getState().activeSessionId,
      owner: useSessionStore.getState().activeAgentId,
      abandoned: useChatStore.getState().abandonedFirstSendIds,
      pointers: pointers(),
    }, 'BDD-03.1 New chat starts Mia\'s extra without replacing the main').toEqual({
      activeSessionId: null,
      owner: 'mia',
      abandoned: [],
      pointers: before,
    })
    expect(pointers().saved).toBe(MIA_MAIN)
  })
})
