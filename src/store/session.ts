import { create } from 'zustand'
import type { AgentKind, Session } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
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
  attachedSessionType: 'chat' | 'task' | 'channel' | 'scheduled' | null
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
  }
}

export const useSessionStore = create<SessionStore>((set, get) => ({
  activeSessionId: null,
  activeAgentId: null,
  activeAgentType: null,

  setActiveSession: (sessionId, agentId, agentType) => {
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
              type: state.attachedSessionType ?? 'chat',
              title: state.attachedTaskTitle,
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
      resetChatBucketForReplay(sessionId)
      const sent = connection.send({ type: 'attach_session', session_id: sessionId })
      if (!sent) {
        useConnectionStore.getState().setConnectionError(
          'Could not attach to session — connection dropped. Please reconnect and try again.'
        )
        return
      }
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
