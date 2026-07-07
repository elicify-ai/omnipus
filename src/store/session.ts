import { create } from 'zustand'
import type { AgentKind, Session } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { logError } from '@/lib/telemetry'

// Emit a production telemetry record for a session-management diagnostic
// condition. Mirrors src/store/chat.ts's `_recordChatDiagnostic` / src/lib/api.ts's
// `_recordApiSchemaError` / src/lib/ws.ts's `_recordDropped` pattern: DEV and
// test builds already surface these via the adjacent console.warn call (kept
// as-is, unchanged), so this only adds a rate-limited structured logError()
// record in production builds — previously these conditions were
// console-only, so an operator running a shipped build had no durable signal.
function _recordSessionDiagnostic(event: string, fields: Record<string, string | number | boolean | null | undefined>): void {
  if (!import.meta.env.DEV && import.meta.env.MODE !== 'test') {
    logError({ event, ...fields })
  }
}

// syncChatForeground is imported lazily to avoid the chat ↔ session circular init.
// It is resolved at call-time via a dynamic require-style closure.
let _syncChatForeground: (() => void) | null = null
export function registerSyncChatForeground(fn: () => void): void {
  _syncChatForeground = fn
}
function syncForeground(): void {
  _syncChatForeground?.()
}

/** Descriptor stored per workspace so we can restore the last-viewed session. */
interface WorkspaceSessionDescriptor {
  id: string
  type: Session['type']
  title: string | null
  agentId: string | null
}

interface SessionStore {
  activeSessionId: string | null
  activeAgentId: string | null
  activeAgentType: AgentKind | null
  setActiveSession: (
    sessionId: string | null,
    agentId?: string | null,
    agentType?: AgentKind | null
  ) => void
  setActiveAgentType: (type: AgentKind | null) => void
  attachedSessionType: 'chat' | 'task' | 'channel' | 'scheduled' | 'heartbeat' | null
  attachedTaskTitle: string | null
  attachToSession: (
    sessionId: string,
    type: Session['type'],
    title?: string,
    agentId?: string
  ) => void
  setAttachedContext: (type: Session['type'], title: string | null) => void
  startNewSession: (agentId?: string | null, agentType?: AgentKind | null) => void
  sessionByWorkspace: Record<string, WorkspaceSessionDescriptor | null>
  /**
   * Write a session descriptor for a specific workspace key directly, bypassing
   * the activeWorkspaceId lookup.  Use this when the caller knows the target
   * workspaceId explicitly (e.g. deep-link redirect in sessions.$sessionId.tsx)
   * and needs to guarantee the write lands before enterWorkspaceChat fires.
   */
  setWorkspaceSessionDescriptor: (
    workspaceId: string,
    descriptor: WorkspaceSessionDescriptor | null
  ) => void
  enterWorkspaceChat: (workspaceId: string) => void
}

// Breaks the chat.ts ↔ session.ts circular import: chat.ts imports this module,
// then registers setReplaying so session.ts never imports chat.ts directly.
// This avoids any ES module circular-init ordering issues entirely.
// F-S7: _chatResetSession removed — per-session sharding makes resetChatSession()
// unnecessary here. Setting activeSessionId=null is sufficient to clear the foreground.
let _chatSetReplaying: ((value: boolean) => void) | null = null

/** Called once by chat.ts after it creates useChatStore (FR-I-014). */
export function registerChatSetReplaying(fn: (value: boolean) => void): void {
  _chatSetReplaying = fn
}

let _chatResetForReplay: ((sessionId: string) => void) | null = null

/** Called once by chat.ts after it creates useChatStore. */
export function registerChatResetForReplay(fn: (sessionId: string) => void): void {
  _chatResetForReplay = fn
}

export function resetChatBucketForReplay(sessionId: string): void {
  _chatResetForReplay?.(sessionId)
}

function setChatReplaying(value: boolean): void {
  if (_chatSetReplaying) {
    _chatSetReplaying(value)
  } else {
    console.warn('[session] setChatReplaying called before chat store registered — isReplaying not set')
    _recordSessionDiagnostic('sessionSetChatReplayingUnregistered', { value })
  }
}

export const useSessionStore = create<SessionStore>((set, get) => ({
  activeSessionId: null,
  activeAgentId: null,
  activeAgentType: null,

  setActiveSession: (sessionId, agentId, agentType) => {
    // Capture the current session type BEFORE the reset below nulls it out.
    // The set() call that follows clears attachedSessionType synchronously,
    // so reading state.attachedSessionType inside the second set()'s updater
    // (below) would always see null and mislabel every session as 'chat'.
    const priorSessionType = get().attachedSessionType
    // Same stale-read hazard as priorSessionType above: attachedTaskTitle is
    // nulled by the set() call below, so it must be captured beforehand too.
    const priorTaskTitle = get().attachedTaskTitle
    set((state) => ({
      ...state,
      activeSessionId: sessionId,
      activeAgentId: agentId ?? state.activeAgentId,
      activeAgentType: agentType ?? state.activeAgentType,
      attachedSessionType: null,
      attachedTaskTitle: null,
    }))
    syncForeground()
    // Record real session ids under the current workspace for restore on re-entry.
    if (sessionId !== null) {
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        set((state) => ({
          sessionByWorkspace: {
            ...state.sessionByWorkspace,
            [wsId]: {
              id: sessionId,
              type: priorSessionType ?? 'chat',
              title: priorTaskTitle,
              agentId: (agentId ?? get().activeAgentId) ?? null,
            },
          },
        }))
      }
    }
  },

  setActiveAgentType: (type) => {
    set({ activeAgentType: type })
  },

  attachedSessionType: null,
  attachedTaskTitle: null,

  attachToSession: (sessionId, type, title, agentId) => {
    const { connection } = useConnectionStore.getState()

    if (connection) {
      const sent = connection.send({ type: 'attach_session', session_id: sessionId })
      if (!sent) {
        useConnectionStore.getState().setConnectionError(
          'Could not attach to session — connection dropped. Please reconnect and try again.'
        )
        return
      }
      // Only wipe the chat bucket once the attach frame is confirmed sent —
      // resetting first (as before) would permanently lose the bucket's
      // contents if send() failed (e.g. during a reconnect window), since
      // there was no rollback for the pre-reset state.
      resetChatBucketForReplay(sessionId)
      set((state) => ({
        activeSessionId: sessionId,
        attachedSessionType: type,
        attachedTaskTitle: title ?? null,
        activeAgentId: agentId ?? state.activeAgentId,
      }))
      // Record under current workspace.
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        set((state) => ({
          sessionByWorkspace: {
            ...state.sessionByWorkspace,
            [wsId]: {
              id: sessionId,
              type,
              title: title ?? null,
              agentId: (agentId ?? get().activeAgentId) ?? null,
            },
          },
        }))
      }
      syncForeground()
      setChatReplaying(true)
    } else {
      console.warn('[session] attachToSession: no connection — attach_session not sent')
      _recordSessionDiagnostic('sessionAttachToSessionNoConnection', { sessionId, type })
      set((state) => ({
        activeSessionId: sessionId,
        attachedSessionType: type,
        attachedTaskTitle: title ?? null,
        activeAgentId: agentId ?? state.activeAgentId,
      }))
      // Record under current workspace (offline path).
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        set((state) => ({
          sessionByWorkspace: {
            ...state.sessionByWorkspace,
            [wsId]: {
              id: sessionId,
              type,
              title: title ?? null,
              agentId: (agentId ?? get().activeAgentId) ?? null,
            },
          },
        }))
      }
      syncForeground()
    }
  },

  startNewSession: (agentId, agentType) => {
    set((state) => ({
      activeSessionId: null,
      activeAgentId: agentId ?? state.activeAgentId,
      activeAgentType: agentType ?? state.activeAgentType,
      attachedSessionType: null,
      attachedTaskTitle: null,
    }))
    // Mark this workspace as intentionally fresh (null).
    const wsId = useWorkspacesStore.getState().activeWorkspaceId
    if (wsId) {
      set((state) => ({
        sessionByWorkspace: {
          ...state.sessionByWorkspace,
          [wsId]: null,
        },
      }))
    }
    syncForeground()
  },

  setAttachedContext: (type, title) => {
    set({
      attachedSessionType: type,
      attachedTaskTitle: title,
    })
  },

  setWorkspaceSessionDescriptor: (workspaceId, descriptor) => {
    set((state) => ({
      sessionByWorkspace: {
        ...state.sessionByWorkspace,
        [workspaceId]: descriptor,
      },
    }))
  },

  sessionByWorkspace: {},

  enterWorkspaceChat: (workspaceId: string) => {
    const state = get()
    const descriptor = state.sessionByWorkspace[workspaceId]

    if (descriptor === undefined) {
      // First visit: no stored session for this workspace.
      // Only reset if something is active (avoid redundant startNewSession when already null).
      if (state.activeSessionId !== null) {
        state.startNewSession()
      }
      return
    }

    if (descriptor === null) {
      // User previously started fresh in this workspace — honour that choice.
      if (state.activeSessionId !== null) {
        state.startNewSession()
      }
      return
    }

    // Stored descriptor — no-op if it's already the active session (Bug 1 fix).
    if (descriptor.id === state.activeSessionId) {
      return
    }

    // Restore the previous conversation for this workspace.
    state.attachToSession(
      descriptor.id,
      descriptor.type,
      descriptor.title ?? undefined,
      descriptor.agentId ?? undefined,
    )
  },
}))
