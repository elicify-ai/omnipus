/**
 * M3 final-gate finding — workspace entry must not read "absent from a
 * partial session list" as "deleted". fetchSessions marks an incomplete
 * enumeration on the list itself (ADR-057 FR-091 coverage — the same flag
 * SearchModal/sessionOverview read); the old entry path never consulted it,
 * so a partial list sent the user to Ava's main and overwrote the saved
 * pointer. Now the entry resolves the remembered id directly
 * (GET /sessions/{id}): found → exact restore; 404 → a TRUTHFUL deletion
 * (the welcome path may then take over); any other failure → keep the
 * pointer as a failed attempt with Retry.
 *
 * Oracles: FR-004 (a visible remembered chat wins; nothing overwrites it
 * without proof), FR-091 coverage contract. REAL stores and flow; network
 * is mocked. WU-10 FR-107 / BDD-12.11 replaces Ava fallback with first
 * colleague Mia; real generated main_session_id reads replace the seam double.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session, Workspace, WorkspaceMemberConfig } from '@/lib/api'
import { fetchSessionDetail, fetchSessions, fetchWorkspace, fetchAgents } from '@/lib/api'
import { MIA } from '../../tests/omnipus-l1-ui-red/fixtures'
import { availableHistoryDetail } from '../../tests/chat-history-paging/legacy-fixtures'
import { ApiError } from '@/lib/api-error'
import { logDiagnostic } from '@/lib/telemetry'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

const TARGET_WS = 'target-workspace'
const TARGET = 'target-chat'
const OTHER = 'other-chat'
// Historical constant name retained; the amended fallback owner is Mia.
const AVA = 'opaque-mia-welcome'
const SOURCE_WS = 'source-workspace'
const SOURCE = 'source-chat'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchSessions: vi.fn(),
  fetchSessionDetail: vi.fn(),
  fetchWorkspace: vi.fn(),
  fetchAgents: vi.fn(),
}))
vi.mock('@/lib/telemetry', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/telemetry')>(),
  logDiagnostic: vi.fn(),
}))

function targetSession(): Session {
  return {
    id: TARGET, agent_id: 'mia', workspace_id: TARGET_WS,
    title: 'Target conversation', type: 'chat', message_count: 1,
    created_at: '2026-10-08T12:00:00Z', updated_at: '2026-10-08T12:00:00Z',
  }
}

function otherSession(): Session {
  return { ...targetSession(), id: OTHER, title: 'A listed conversation' }
}

function avaWorkspace(member: WorkspaceMemberConfig = {}): Workspace {
  return {
    id: TARGET_WS, name: 'Target', revision: '1'.repeat(64), status: 'active',
    pinned: false, pin_order: 0, task_count: 0,
    created_at: '2026-10-08T12:00:00Z', updated_at: '2026-10-08T12:00:00Z',
    core_team: ['mia'], member_configs: { mia: member },
  }
}

/** A list that fetchSessions would really return when an agent failed to enumerate. */
function partialList(sessions: Session[]): Session[] {
  return Object.assign(sessions, { partialErrors: ['agent-store-failed'], incomplete: true })
}

function rememberTargetPointer() {
  useSessionStore.getState().setWorkspaceSessionDescriptor(TARGET_WS, {
    id: TARGET, type: 'chat', title: 'Target conversation', agentId: 'mia',
  })
}

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
  vi.mocked(fetchAgents).mockResolvedValue([MIA])
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useUiStore.setState({ toasts: [] })
  useWorkspacesStore.setState({ activeWorkspaceId: TARGET_WS })
  const member: WorkspaceMemberConfig = { main_session_id: AVA }
  vi.mocked(fetchWorkspace).mockResolvedValue(avaWorkspace(member))
})

afterEach(() => {
  vi.restoreAllMocks()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
})

describe('workspace entry on a partial session list (M3)', () => {
  it('a remembered chat missing from an INCOMPLETE list is resolved directly and restored — never swapped for the main', async () => {
    useConnectionStore.setState({ connection: { send: vi.fn().mockReturnValue(true) } as never, isConnected: true })
    vi.mocked(fetchSessions).mockResolvedValue(partialList([otherSession()]))
    vi.mocked(fetchSessionDetail).mockResolvedValue(availableHistoryDetail(targetSession()))
    rememberTargetPointer()

    await useSessionStore.getState().enterWorkspaceChat(TARGET_WS)

    expect(fetchSessionDetail).toHaveBeenCalledExactlyOnceWith(TARGET)
    expect(useSessionStore.getState().activeSessionId, 'the remembered chat is restored').toBe(TARGET)
    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(useSessionStore.getState().sessionByWorkspace[TARGET_WS]?.id, 'the saved pointer is kept').toBe(TARGET)
    expect(useUiStore.getState().toasts).toEqual([])
  })

  // FR-107 / BDD-12.11 preserves truthful deletion; only fallback owner changes.
  it('a 404 from the direct check is a truthful deletion: the first colleague main takes over and the pointer follows it', async () => {
    useConnectionStore.setState({ connection: { send: vi.fn().mockReturnValue(true) } as never, isConnected: true })
    vi.mocked(fetchSessions).mockResolvedValue(partialList([]))
    vi.mocked(fetchSessionDetail).mockRejectedValue(new ApiError(404, 'Session not found'))
    rememberTargetPointer()

    await useSessionStore.getState().enterWorkspaceChat(TARGET_WS)

    expect(useSessionStore.getState().activeSessionId).toBe(AVA)
    expect(useSessionStore.getState().workspaceEntry?.status).toBe('welcome')
    expect(useSessionStore.getState().sessionByWorkspace[TARGET_WS]?.id).toBe(AVA)
  })

  it('a failed direct check keeps the committed chat and the saved pointer, and asks Retry — it never falls back', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: SOURCE_WS })
    useSessionStore.getState().setActiveSession(SOURCE, 'jim')
    useWorkspacesStore.setState({ activeWorkspaceId: TARGET_WS })
    rememberTargetPointer()
    const send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({ connection: { send } as never, isConnected: true })
    vi.mocked(fetchSessions).mockResolvedValue(partialList([otherSession()]))
    vi.mocked(fetchSessionDetail).mockRejectedValue(new ApiError(0, 'Network unavailable. Check your connection.'))

    await useSessionStore.getState().enterWorkspaceChat(TARGET_WS)

    expect(useSessionStore.getState().activeSessionId, 'the committed chat is kept').toBe(SOURCE)
    expect(useSessionStore.getState().workspaceEntry).toEqual({
      status: 'failed-attempt',
      committed: { sessionId: SOURCE, agentId: 'jim' },
      attempted: { sessionId: TARGET, sendEnabled: false },
      acknowledged: false,
      retry: true,
      fellBack: false,
    })
    expect(useSessionStore.getState().sessionByWorkspace[TARGET_WS]?.id, 'the saved pointer is not overwritten').toBe(TARGET)
    expect(send).not.toHaveBeenCalled()
    const toast = useUiStore.getState().toasts[0]
    expect(toast?.message).toBe('Could not restore your last conversation. Retry to try again.')
    expect(toast?.action?.label).toBe('Retry')
    expect(logDiagnostic).toHaveBeenCalledWith('sessionWorkspaceEntryPointerCheckFailed', expect.objectContaining({
      workspaceId: TARGET_WS,
    }))
  })
})
