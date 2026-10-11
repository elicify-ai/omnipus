/**
 * pairExtra.test.ts — C4 (R3 delta re-review): "+ New chat" switched the
 * active workspace BEFORE the new-chat decision. On a 'prompt' outcome
 * ("Keep this chat" / the dialog opens) the active workspace stayed
 * switched to the row's workspace while the route still showed the old one,
 * and the @-recipient pair (which reads activeWorkspaceId at send time)
 * read the wrong workspace.
 *
 * The rule under test: the workspace switch belongs to the 'started'
 * outcome ONLY — a 'prompt'/'kept'/'refused' outcome leaves the user exactly
 * where the route says they are.
 *
 * Real stores (the proven session.workspaceEntry.recovery.test.ts pattern);
 * only the network and the diagnostic sink are mocked. No DOM.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { beginPairExtra } from './pairExtra'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchSessions: vi.fn(),
  fetchWorkspace: vi.fn(),
}))
vi.mock('@/lib/telemetry', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/telemetry')>(),
  logDiagnostic: vi.fn(),
}))

const WS_A = 'ws-a'
const WS_B = 'ws-b'

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useUiStore.setState({ toasts: [] })
  useWorkspacesStore.setState({ activeWorkspaceId: WS_A })
})

function connect() {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({ connection: { send } as never, isConnected: true })
  return send
}

describe('beginPairExtra — the workspace switch belongs to a started outcome only (C4)', () => {
  it("a 'prompt' outcome leaves the active workspace on the workspace the route shows", () => {
    connect()
    // Workspace A hosts an unconfirmed first send with no session id yet —
    // the new-chat decision must prompt instead of starting.
    useSessionStore.getState().setActiveSession(null, 'ava')
    useChatStore.getState().sendMessage('first message in A')
    expect(useSessionStore.getState().pendingFirstSend?.sessionId ?? null).toBeNull()

    const navigate = vi.fn()
    const close = vi.fn()
    const outcome = beginPairExtra(WS_B, 'mia', navigate, close)

    expect(outcome).toBe('prompt')
    // THE RULE: workspace A is untouched — the route still shows A, and the
    // @-recipient pair keeps reading A at send time.
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS_A)
    expect(navigate).not.toHaveBeenCalled()
    expect(close).not.toHaveBeenCalled()
    // The dialog is open for the row that asked (the mia row), so the user
    // can answer "Keep this chat" or "Start a new chat" there.
    expect(useSessionStore.getState().newChatPrompt?.action).toBe('prompt')
  })

  it("a 'started' outcome switches the workspace and navigates to the extra's workspace", () => {
    connect()
    useSessionStore.getState().setActiveSession(null, 'ava')
    useChatStore.getState().sendMessage('first message in A')
    // The pending send is abandoned (the dialog's confirm path), so the
    // extra starts outright.
    useChatStore.getState().abandonPendingFirstSend()

    const navigate = vi.fn()
    const close = vi.fn()
    const outcome = beginPairExtra(WS_B, 'mia', navigate, close)

    expect(outcome).toBe('started')
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS_B)
    expect(navigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: WS_B },
    })
    expect(close).toHaveBeenCalledTimes(1)
  })
})
