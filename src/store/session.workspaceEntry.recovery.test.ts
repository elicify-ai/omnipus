/**
 * Gate round 2: FR-004/005/013, BDD-02.3/02.4/03.1.
 * Oracles are the review findings: no lost queued messages, no stale block on
 * a deliberately new chat, no source-chat/target-workspace send, and no
 * transport failure invented from a hidden foreground guard.
 * Real stores/flows; only network, diagnostic sink and unpublished main-ID
 * seam are mocked. No timing thresholds stand in for delivery.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session, Workspace, WorkspaceMemberConfig } from '@/lib/api'
import { fetchSessions, fetchWorkspace } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { workspaceEntryBlocksSend } from '@/lib/nav/workspaceEntry'
import { beginPairExtra } from '@/components/layout/sidebar/pairExtra'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

const seam = vi.hoisted(() => ({ mains: new Map<object, string>() }))
vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: (member: object) => seam.mains.get(member),
  isMainSession: () => false,
  sessionAttention: () => 'unknown',
  attachAckFields: () => ({}),
  attentionBoundOfFrame: () => undefined,
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchSessions: vi.fn(),
  fetchWorkspace: vi.fn(),
}))
vi.mock('@/lib/telemetry', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/telemetry')>(),
  logDiagnostic: vi.fn(),
}))

const SOURCE_WS = 'source-workspace'
const TARGET_WS = 'target-workspace'
const SOURCE = 'source-chat'
const TARGET = 'target-chat'
const AVA = 'ava-welcome'
const failed = {
  status: 'failed-attempt' as const,
  committed: { sessionId: SOURCE, agentId: 'jim' },
  attempted: { sessionId: TARGET, sendEnabled: false as const },
  acknowledged: false as const,
  retry: true as const,
  fellBack: false as const,
}
const unavailable = {
  status: 'unavailable' as const, sessionId: null, sendEnabled: false as const,
  acknowledged: false as const, retry: true as const,
}
const queued = [
  { id: 'queued-a', content: 'First queued message', timestamp: '2026-10-08T12:00:00Z' },
  { id: 'queued-b', content: 'Second queued message', timestamp: '2026-10-08T12:01:00Z' },
]

function targetSession(): Session {
  return {
    id: TARGET, agent_id: 'mia', active_agent_id: 'jim', workspace_id: TARGET_WS,
    title: 'Target conversation', type: 'chat', message_count: 1,
    created_at: '2026-10-08T12:00:00Z', updated_at: '2026-10-08T12:00:00Z',
  }
}

function targetWorkspace(member: WorkspaceMemberConfig = {}): Workspace {
  return {
    id: TARGET_WS, name: 'Target', revision: 'rev-1', status: 'active',
    pinned: false, pin_order: 0, task_count: 0,
    created_at: '2026-10-08T12:00:00Z', updated_at: '2026-10-08T12:00:00Z',
    member_configs: { ava: member },
  }
}

function connect() {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({ connection: { send } as never, isConnected: true })
  return send
}

function retainSourceAndRememberTarget() {
  useWorkspacesStore.setState({ activeWorkspaceId: SOURCE_WS })
  useSessionStore.getState().setActiveSession(SOURCE, 'jim')
  useChatStore.getState().appendMessage({
    id: 'source-message', role: 'user', content: 'Source history',
    timestamp: '2026-10-08T11:00:00Z', status: 'error', deliveryStatus: 'failed',
  })
  useSessionStore.getState().setWorkspaceSessionDescriptor(TARGET_WS, {
    id: TARGET, type: 'chat', title: 'Target conversation', agentId: 'mia',
  })
  useWorkspacesStore.setState({ activeWorkspaceId: TARGET_WS })
}

function messageFrames(send: ReturnType<typeof vi.fn>) {
  return send.mock.calls.map(([frame]) => frame as Record<string, unknown>)
    .filter((frame) => frame.type === 'message')
}

async function settleInputPromises() {
  await Promise.allSettled([
    vi.mocked(fetchSessions).mock.results[0]?.value,
    vi.mocked(fetchWorkspace).mock.results[0]?.value,
  ])
  await new Promise<void>((resolve) => queueMicrotask(resolve))
}

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
  seam.mains.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useUiStore.setState({ toasts: [] })
  useWorkspacesStore.setState({ activeWorkspaceId: TARGET_WS })
  vi.mocked(fetchSessions).mockResolvedValue([targetSession()])
  vi.mocked(fetchWorkspace).mockResolvedValue(targetWorkspace())
})

afterEach(() => {
  vi.restoreAllMocks()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
})

describe('workspace-entry recovery — gate round 2', () => {
  it('New chat clears a failed entry and sends a fresh extra for the selected pair, never the source chat', () => {
    retainSourceAndRememberTarget()
    const send = connect()
    useSessionStore.setState({
      workspaceEntry: failed,
      resolvingSessionForWorkspace: { [TARGET_WS]: true },
      mainPointerByPair: { [`${TARGET_WS}::mia`]: 'mia-main' },
    })
    const navigate = vi.fn()
    const close = vi.fn()
    beginPairExtra(TARGET_WS, 'mia', navigate, close)

    expect(useSessionStore.getState().workspaceEntry).toBeNull()
    expect(useSessionStore.getState().resolvingSessionForWorkspace).toEqual({})
    expect(workspaceEntryBlocksSend(useSessionStore.getState(), TARGET_WS)).toBe(false)
    expect(useSessionStore.getState().mainPointerByPair).toEqual({ [`${TARGET_WS}::mia`]: 'mia-main' })
    expect(useSessionStore.getState().sessionByWorkspace[TARGET_WS]?.id).toBe(TARGET)
    useChatStore.getState().sendMessage('Fresh extra', { clientMessageId: 'fresh-message' })
    expect(messageFrames(send)).toEqual([expect.objectContaining({
      type: 'message', content: 'Fresh extra', client_message_id: 'fresh-message',
      agent_id: 'mia', metadata: { workspace_id: TARGET_WS },
    })])
    expect(messageFrames(send)[0].session_id).toBeUndefined()
    expect(useChatStore.getState().sessionsById[SOURCE].messageOrder).toEqual(['source-message'])
    expect(navigate).toHaveBeenCalledExactlyOnceWith({
      to: '/workspaces/$workspaceId/chat', params: { workspaceId: TARGET_WS },
    })
    expect(close).toHaveBeenCalledTimes(1)
  })

  it.each(['failed-attempt', 'unavailable', 'resolving'] as const)(
    'a %s entry preserves every queued item and resumes delivery in order only after target entry succeeds',
    async (status) => {
      retainSourceAndRememberTarget()
      const send = connect()
      useSessionStore.setState({
        workspaceEntry: status === 'failed-attempt' ? failed : status === 'unavailable' ? unavailable : null,
        resolvingSessionForWorkspace: status === 'resolving' ? { [TARGET_WS]: true } : {},
      })
      useChatStore.setState({ outboundQueue: [...queued] })
      useChatStore.getState().drainOutboundQueue()

      expect(messageFrames(send)).toEqual([])
      expect(useChatStore.getState().outboundQueue).toEqual([])
      expect(useChatStore.getState().pendingDrainQueue).toEqual(queued)
      expect(logDiagnostic).toHaveBeenCalledWith('chatWorkspaceEntrySendBlocked', expect.objectContaining({
        operation: 'drain', workspaceId: TARGET_WS,
      }))
      const toast = useUiStore.getState().toasts[0]
      expect(toast?.message).toBe(status === 'failed-attempt'
        ? 'Could not restore your last conversation. Retry to try again.'
        : status === 'unavailable' ? 'This chat is unavailable right now' : 'Restoring your conversation…')
      if (status !== 'resolving') expect(toast?.action?.label).toBe('Retry')
      // Empty outboundQueue still reaches maybeDrainNext: do not lose its head.
      useChatStore.getState().drainOutboundQueue()
      expect(useChatStore.getState().pendingDrainQueue).toEqual(queued)

      await useSessionStore.getState().enterWorkspaceChat(TARGET_WS)
      await vi.waitFor(() => expect(messageFrames(send)).toHaveLength(1))
      expect(useChatStore.getState().pendingDrainQueue).toEqual([queued[1]])
      useChatStore.getState().handleFrame({ type: 'done', session_id: TARGET })
      expect(messageFrames(send)).toEqual(queued.map((item) => expect.objectContaining({
        type: 'message', content: item.content, client_message_id: item.id,
        session_id: TARGET, agent_id: 'mia', metadata: { workspace_id: TARGET_WS },
      })))
      expect(useChatStore.getState().pendingDrainQueue).toEqual([])
      expect(useChatStore.getState().outboundQueue).toEqual([])
      expect(useChatStore.getState().sessionsById[SOURCE].messageOrder).toEqual(['source-message'])
      expect(useSessionStore.getState().sessionByWorkspace[SOURCE_WS]?.id).toBe(SOURCE)
    },
  )

  it.each(['absent', 'remembered'] as const)(
    'a rejected workspace fetch with an %s pointer reports transport failure, retains source and recovers through Retry',
    async (pointer) => {
      retainSourceAndRememberTarget()
      if (pointer === 'absent') useSessionStore.setState({ sessionByWorkspace: {} })
      if (pointer === 'absent') localStorage.clear()
      const member: WorkspaceMemberConfig = {}
      seam.mains.set(member, AVA)
      vi.mocked(fetchWorkspace).mockRejectedValueOnce(new Error('Workspace request offline'))
      const send = connect()
      await useSessionStore.getState().enterWorkspaceChat(TARGET_WS)

      expect(useSessionStore.getState().workspaceEntry).toEqual({
        ...failed, attempted: { sessionId: pointer === 'absent' ? '' : TARGET, sendEnabled: false },
      })
      expect(useSessionStore.getState().activeSessionId).toBe(SOURCE)
      expect(useSessionStore.getState().activeAgentId).toBe('jim')
      expect(send).not.toHaveBeenCalled()
      expect(logDiagnostic).toHaveBeenCalledWith('sessionWorkspaceEntryLoadFailed', expect.objectContaining({
        workspaceId: TARGET_WS, resource: 'workspace',
      }))
      useChatStore.getState().sendMessage('Do not combine source and target')
      expect(messageFrames(send)).toEqual([])
      const toast = useUiStore.getState().toasts[0]
      expect(toast?.message).toBe('Could not restore your last conversation. Retry to try again.')
      expect(toast?.action?.label).toBe('Retry')

      vi.mocked(fetchWorkspace).mockResolvedValue(targetWorkspace(member))
      toast.action?.onClick()
      await useSessionStore.getState().enterWorkspaceChat(TARGET_WS)
      expect(useSessionStore.getState().activeSessionId).toBe(pointer === 'absent' ? AVA : TARGET)
      expect(useSessionStore.getState().workspaceEntry?.status).toBe(pointer === 'absent' ? 'welcome' : 'exact')
      expect(workspaceEntryBlocksSend(useSessionStore.getState(), TARGET_WS)).toBe(false)
    },
  )

  it.each(['send', 'resend'] as const)(
    '%s respects the failed-entry gate, reports Retry and leaves the original failed bubble intact',
    (operation) => {
      retainSourceAndRememberTarget()
      const send = connect()
      const original = useChatStore.getState().sessionsById[SOURCE]
      useSessionStore.setState({ workspaceEntry: failed })
      if (operation === 'send') useChatStore.getState().sendMessage('Assistant retry request')
      else useChatStore.getState().resendMessage('source-message')

      expect(messageFrames(send)).toEqual([])
      expect(useChatStore.getState().sessionsById[SOURCE]).toEqual(original)
      expect(logDiagnostic).toHaveBeenCalledWith('chatWorkspaceEntrySendBlocked', expect.objectContaining({
        operation, workspaceId: TARGET_WS, entryStatus: 'failed-attempt',
      }))
      expect(useUiStore.getState().toasts[0]?.message).toBe('Could not restore your last conversation. Retry to try again.')
      expect(useUiStore.getState().toasts[0]?.action?.label).toBe('Retry')
    },
  )

  it('a tab hidden during input loading defers the attach until focus instead of inventing a restore failure', async () => {
    retainSourceAndRememberTarget()
    const send = connect()
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    let resolveSessions!: (sessions: Session[]) => void
    vi.mocked(fetchSessions).mockImplementationOnce(() => new Promise((resolve) => { resolveSessions = resolve }))
    const entry = useSessionStore.getState().enterWorkspaceChat(TARGET_WS)
    hidden.mockReturnValue(true)
    resolveSessions([targetSession()])
    await settleInputPromises()

    expect(useSessionStore.getState().workspaceEntry).toBeNull()
    expect(useSessionStore.getState().activeSessionId).toBe(SOURCE)
    expect(workspaceEntryBlocksSend(useSessionStore.getState(), TARGET_WS)).toBe(true)
    expect(send).not.toHaveBeenCalled()
    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await entry
    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(useSessionStore.getState().activeSessionId).toBe(TARGET)
    expect(send).toHaveBeenCalledExactlyOnceWith({ type: 'attach_session', session_id: TARGET })
  })

  it('New chat supersedes an in-flight network restore; its late response cannot replace the fresh pending send', async () => {
    retainSourceAndRememberTarget()
    const send = connect()
    let resolveSessions!: (sessions: Session[]) => void
    vi.mocked(fetchSessions).mockImplementationOnce(() => new Promise((resolve) => { resolveSessions = resolve }))
    const entry = useSessionStore.getState().enterWorkspaceChat(TARGET_WS)
    beginPairExtra(TARGET_WS, 'mia', vi.fn(), vi.fn())
    useChatStore.getState().sendMessage('Fresh chat wins', { clientMessageId: 'winning-send' })
    resolveSessions([targetSession()])
    await entry

    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(useSessionStore.getState().workspaceEntry).toBeNull()
    expect(useChatStore.getState().pendingFirstSend?.clientMessageId).toBe('winning-send')
    expect(send).toHaveBeenCalledTimes(1)
    expect(messageFrames(send)).toEqual([expect.objectContaining({
      type: 'message', content: 'Fresh chat wins', client_message_id: 'winning-send',
      agent_id: 'mia', metadata: { workspace_id: TARGET_WS },
    })])
    expect(messageFrames(send)[0].session_id).toBeUndefined()
  })

  it('a hidden entry waits without a restore failure or send; focus commits the validated target without acknowledging', async () => {
    retainSourceAndRememberTarget()
    const send = connect()
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    const entry = useSessionStore.getState().enterWorkspaceChat(TARGET_WS)
    await settleInputPromises()

    expect(useSessionStore.getState().workspaceEntry).toBeNull()
    expect(useSessionStore.getState().resolvingSessionForWorkspace).toEqual({ [TARGET_WS]: true })
    expect(useSessionStore.getState().activeSessionId).toBe(SOURCE)
    expect(send).not.toHaveBeenCalled()
    expect(useUiStore.getState().toasts).toEqual([])
    expect(workspaceEntryBlocksSend(useSessionStore.getState(), TARGET_WS)).toBe(true)

    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await entry
    expect(useSessionStore.getState().activeSessionId).toBe(TARGET)
    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(useSessionStore.getState().resolvingSessionForWorkspace).toEqual({})
    expect(send).toHaveBeenCalledExactlyOnceWith({ type: 'attach_session', session_id: TARGET })
  })

  it('New chat supersedes a hidden entry so focus cannot restore the abandoned target or re-block the fresh extra', async () => {
    retainSourceAndRememberTarget()
    const send = connect()
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    const entry = useSessionStore.getState().enterWorkspaceChat(TARGET_WS)
    await settleInputPromises()
    beginPairExtra(TARGET_WS, 'mia', vi.fn(), vi.fn())
    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await entry

    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(useSessionStore.getState().workspaceEntry).toBeNull()
    expect(useSessionStore.getState().resolvingSessionForWorkspace).toEqual({})
    expect(send).not.toHaveBeenCalled()
    expect(workspaceEntryBlocksSend(useSessionStore.getState(), TARGET_WS)).toBe(false)
  })
})
