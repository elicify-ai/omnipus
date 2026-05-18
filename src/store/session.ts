import { create } from 'zustand'
import type { Session } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
// syncChatForeground is imported lazily to avoid the chat ↔ session circular init.
// It is resolved at call-time via a dynamic require-style closure.
let _syncChatForeground: (() => void) | null = null
export function registerSyncChatForeground(fn: () => void): void {
  _syncChatForeground = fn
}
function syncForeground(): void {
  _syncChatForeground?.()
}

interface SessionStore {
  activeSessionId: string | null
  activeAgentId: string | null
  /** The type of the currently active agent ('core' | 'custom' | 'system' | null).
   *  Set by setActiveSession so all callers stay in sync without manual tracking. */
  activeAgentType: 'core' | 'custom' | 'system' | null
  setActiveSession: (
    sessionId: string | null,
    agentId?: string | null,
    agentType?: 'core' | 'custom' | 'system' | null
  ) => void
  /** Proper store action for updating activeAgentType.
   *  Replaces direct useSessionStore.setState({ activeAgentType }) call-sites
   *  so future side-effects can be added here without touching callers. */
  setActiveAgentType: (type: 'core' | 'custom' | 'system' | null) => void

  // Attached session context — tracks when viewing a task/channel session
  attachedSessionType: 'chat' | 'task' | 'channel' | null
  attachedTaskTitle: string | null
  attachToSession: (
    sessionId: string,
    type: Session['type'],
    title?: string,
    agentId?: string
  ) => void

  /** Starts a fresh chat in the SPA: clears activeSessionId so the next
   *  message frame omits session_id, prompting the server to mint a new
   *  session and ack it back via {type:"session_started", session_id}.
   *  Does NOT touch existing per-session buckets — background sessions
   *  keep streaming and remain visible in the SessionPanel. */
  startNewSession: (agentId?: string | null, agentType?: 'core' | 'custom' | 'system' | null) => void
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
    // F-S3: single set() call so any frame arriving mid-update sees a consistent state.
    set((state) => ({
      ...state,
      activeSessionId: sessionId,
      activeAgentId: agentId ?? state.activeAgentId,
      activeAgentType: agentType ?? state.activeAgentType,
      attachedSessionType: null,
      attachedTaskTitle: null,
    }))
    // Sync chat foreground selectors to the new active session's bucket.
    // Does NOT reset the bucket — background sessions keep their state.
    // F-S2: drop the __default orphan bucket in production (defensive cleanup after session switch).
    syncForeground()
  },

  // Dedicated action so future side effects can be added without touching callers.
  setActiveAgentType: (type) => {
    set({ activeAgentType: type })
  },

  attachedSessionType: null,
  attachedTaskTitle: null,

  attachToSession: (sessionId, type, title, agentId) => {
    const { connection } = useConnectionStore.getState()

    // W1-11: send the WS frame BEFORE committing state. If send fails, leave
    // the previous session state intact so the UI doesn't show a phantom
    // attached session with no gateway replay in flight.
    if (connection) {
      // Reset BEFORE sending attach so the upcoming replay rebuilds the
      // bucket from scratch. Without this, reopening a session that
      // already lived in the SPA store appends every replayed message
      // again, doubling (or quadrupling) bubbles per click.
      resetChatBucketForReplay(sessionId)
      const sent = connection.send({ type: 'attach_session', session_id: sessionId })
      if (!sent) {
        useConnectionStore.getState().setConnectionError(
          'Could not attach to session — connection dropped. Please reconnect and try again.'
        )
        return
      }
      set({
        activeSessionId: sessionId,
        attachedSessionType: type,
        attachedTaskTitle: title ?? null,
        activeAgentId: agentId ?? get().activeAgentId,
      })
      syncForeground()
      // FR-I-014: replay is now in flight — disable send until done arrives.
      setChatReplaying(true)
    } else {
      // No connection at all — commit state anyway (offline/optimistic path) but
      // do not set replaying since no replay will arrive.
      console.warn('[session] attachToSession: no connection — attach_session not sent')
      set({
        activeSessionId: sessionId,
        attachedSessionType: type,
        attachedTaskTitle: title ?? null,
        activeAgentId: agentId ?? get().activeAgentId,
      })
      syncForeground()
    }
  },

  startNewSession: (agentId, agentType) => {
    // Setting activeSessionId=null shows an empty foreground; existing session
    // buckets are untouched so background sessions keep streaming.
    set({
      activeSessionId: null,
      activeAgentId: agentId ?? get().activeAgentId,
      activeAgentType: agentType ?? get().activeAgentType,
      attachedSessionType: null,
      attachedTaskTitle: null,
    })
    syncForeground()
  },
}))
