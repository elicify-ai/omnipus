import { create } from 'zustand'
import type { AgentKind, Session } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { logDiagnostic } from '@/lib/telemetry'

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

/**
 * Where the current `activeAgentId` came from — the input to the agent
 * PRECEDENCE RULE below.
 *
 *   - `'auto'` — derived, not chosen: a session attach/reattach, a
 *     `session_started` ack, a deep-link, the workspace-setup kickoff, or
 *     AgentPicker's auto-select-first-ready-agent effect.
 *   - `'user'` — the human explicitly picked this agent in THIS mount, via
 *     the AgentPicker dropdown or the composer's "@" mention menu (both go
 *     through `selectAgent`, the only writer of this value).
 */
export type AgentSelectionSource = 'auto' | 'user'

interface SessionStore {
  activeSessionId: string | null
  activeAgentId: string | null
  activeAgentType: AgentKind | null
  /**
   * ── AGENT PRECEDENCE RULE ────────────────────────────────────────────────
   *
   * `activeAgentId` decides which agent the NEXT message is routed to
   * (`chat.ts` stamps it onto the outbound `message` frame's `agent_id`, and
   * `pkg/gateway/websocket.go` treats an explicit `agent_id` as
   * authoritative). Several writers used to race for it last-write-wins,
   * which meant a background session attach could silently re-point the user
   * at a different agent AFTER they had picked one — the user saw the picker
   * flip back on its own and their message went to the wrong agent, with no
   * error and no way to tell.
   *
   * Precedence, highest first:
   *
   *   1. A SERVER-AUTHORITATIVE switch (`applyServerAgentSwitch`, driven by
   *      the WS `agent_switched` frame). The backend has genuinely handed the
   *      conversation to another agent; the picker must report reality, so
   *      this always wins AND resets the source back to `'auto'`.
   *   2. An EXPLICIT USER SELECTION (`selectAgent`). It outranks any
   *      session-derived agent and stays in force until the user changes it
   *      again, the server switches agents, or they leave the workspace they
   *      chose it in (`enterWorkspaceChat` — a different workspace has a
   *      different team roster, so carrying the pick across would point the
   *      composer at an agent that may not even be on the new team).
   *   3. A SESSION-DERIVED HINT — the `agentId` argument of
   *      `setActiveSession` / `attachToSession` / `startNewSession` and the
   *      argument of `setActiveAgentType`. These are hints, NOT commands:
   *      they are adopted only when no explicit selection is in force (see
   *      `adoptsAgentHint`).
   *
   * Deliberately NOT chosen: reconciling at attach time by prompting, or
   * letting the most recent write win with a toast. A session's remembered
   * agent is a default, and a default must never overwrite a choice.
   */
  agentSelectionSource: AgentSelectionSource
  /**
   * The workspace `selectAgent` was last called in — the scope of the
   * rule-2 selection above. `null` whenever `agentSelectionSource` is
   * `'auto'`, or when the pick was made outside any workspace.
   */
  agentSelectionWorkspaceId: string | null
  /**
   * Rule 2 — record an EXPLICIT user agent choice (AgentPicker dropdown, "@"
   * mention). Deliberately does NOT touch `activeSessionId`,
   * `attachedSessionType` or `attachedTaskTitle`: picking who you are talking
   * to is not switching what you are talking about, and routing it through
   * `setActiveSession` (as both call sites used to) both detached nothing
   * useful and silently wiped the "Task:" banner off a task session.
   */
  selectAgent: (agentId: string, agentType?: AgentKind | null) => void
  /**
   * Rule 1 — apply a SERVER-AUTHORITATIVE agent change (WS `agent_switched`,
   * e.g. a Mia → Jim handover). Clears any user pin first, because the
   * backend has already moved the conversation: leaving the picker on the
   * user's old choice would misreport who is actually answering.
   */
  applyServerAgentSwitch: (
    sessionId: string | null,
    agentId: string,
    agentType?: AgentKind | null
  ) => void
  setActiveSession: (
    sessionId: string | null,
    agentId?: string | null,
    agentType?: AgentKind | null
  ) => void
  setActiveAgentType: (type: AgentKind | null) => void
  // 'verifier' is part of Session['type'] (ADR-052 FR-036) — verifier
  // sessions are never actually attachable via any UI session-selection flow
  // (Sidebar/SearchModal exclude them by default), so this value is
  // structurally reachable but not expected in practice. Derived from
  // Session['type'] (rather than a hand-respelled union literal) so a future
  // wire enum change can't silently drift out of sync here.
  attachedSessionType: Session['type'] | null
  attachedTaskTitle: string | null
  /**
   * Attaches the WS to a session (sends `attach_session`) and updates local
   * state. Returns whether the attach actually succeeded:
   *   - `true`  — the frame was sent (connected path), OR the WS is offline
   *               (state is still recorded and a toast warns the user;
   *               `WsLifecycle.onConnected` -> `reattachActiveSession` finishes
   *               the job once the connection returns).
   *   - `false` — the WS is connected but `connection.send()` itself returned
   *               false (a real "up but currently unable to send" state, e.g.
   *               mid reconnect-backoff). Store state is intentionally left
   *               UNTOUCHED on this path (Wave-1 Bug 2 regression test) — the
   *               caller MUST check this return value and abort any follow-on
   *               work (seeding tokens, navigating, closing a modal, etc.)
   *               rather than proceeding as though the attach worked. Model:
   *               `reattachActiveSession`'s failed-send branch in
   *               `OmnipusRuntimeProvider.tsx`.
   */
  attachToSession: (
    sessionId: string,
    type: Session['type'],
    title?: string,
    agentId?: string
  ) => boolean
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
  /**
   * Removes any `sessionByWorkspace` entries that point at `sessionId` — set
   * to `null` ("explicitly fresh") rather than deleted, so the next
   * `enterWorkspaceChat` for that workspace starts a new session instead of
   * silently reattaching the now-deleted id (a zombie reattach). Also clears
   * `activeSessionId` (via `setActiveSession`) when it matches the deleted
   * session — this replaces the inline `activeSessionId === deletedId` check
   * that used to live in SearchModal's `deleteMut.onSuccess`, so the pruning
   * logic lives in one place regardless of which UI triggers the delete.
   */
  pruneSessionDescriptor: (sessionId: string) => void
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

/**
 * Rule 3 of the AGENT PRECEDENCE RULE (see `SessionStore.agentSelectionSource`):
 * may a session-derived agent hint be adopted right now?
 *
 * Yes when nothing the user picked is in force — either the source is
 * `'auto'`, or there is no active agent at all (a pin on "no agent" is
 * meaningless, and refusing a hint there would leave the composer with
 * nothing to route to).
 */
function adoptsAgentHint(state: Pick<SessionStore, 'agentSelectionSource' | 'activeAgentId'>): boolean {
  return state.agentSelectionSource === 'auto' || state.activeAgentId === null
}

function setChatReplaying(value: boolean): void {
  if (_chatSetReplaying) {
    _chatSetReplaying(value)
  } else {
    console.warn('[session] setChatReplaying called before chat store registered — isReplaying not set')
    logDiagnostic('sessionSetChatReplayingUnregistered', { value })
  }
}

export const useSessionStore = create<SessionStore>((set, get) => ({
  activeSessionId: null,
  activeAgentId: null,
  activeAgentType: null,
  agentSelectionSource: 'auto',
  agentSelectionWorkspaceId: null,

  selectAgent: (agentId, agentType) => {
    const wsId = useWorkspacesStore.getState().activeWorkspaceId
    set((state) => {
      // Keep this workspace's remembered agent in step with the choice, so a
      // LATER mount (after a reload, where the pin is gone) restores the
      // agent the user actually picked rather than the one the session was
      // created with. Only updates an EXISTING descriptor: `null` means the
      // user deliberately started fresh here and `undefined` means they have
      // never had a session in this workspace — neither should be
      // resurrected into a descriptor by an agent pick.
      const descriptor = wsId ? state.sessionByWorkspace[wsId] : undefined
      return {
        activeAgentId: agentId,
        // `agentType ?? state.activeAgentType` matches what the two call
        // sites got from `setActiveSession` before this action existed —
        // both pass `agent.type ?? null`, and a null there has always meant
        // "unknown, leave it alone", not "clear it".
        activeAgentType: agentType ?? state.activeAgentType,
        agentSelectionSource: 'user' as const,
        agentSelectionWorkspaceId: wsId,
        ...(wsId && descriptor
          ? {
              sessionByWorkspace: {
                ...state.sessionByWorkspace,
                [wsId]: { ...descriptor, agentId },
              },
            }
          : {}),
      }
    })
    syncForeground()
  },

  applyServerAgentSwitch: (sessionId, agentId, agentType) => {
    // Drop the user pin BEFORE delegating, so the hint below is adopted:
    // this is not a hint, it is the backend reporting what it already did.
    set({ agentSelectionSource: 'auto', agentSelectionWorkspaceId: null })
    get().setActiveSession(sessionId, agentId, agentType)
  },

  setActiveSession: (sessionId, agentId, agentType) => {
    // Capture the current session type BEFORE the reset below nulls it out.
    // The set() call that follows clears attachedSessionType synchronously,
    // so reading state.attachedSessionType inside the second set()'s updater
    // (below) would always see null and mislabel every session as 'chat'.
    const priorSessionType = get().attachedSessionType
    // Same stale-read hazard as priorSessionType above: attachedTaskTitle is
    // nulled by the set() call below, so it must be captured beforehand too.
    const priorTaskTitle = get().attachedTaskTitle
    set((state) => {
      // `agentId`/`agentType` are a session-derived HINT (precedence rule 3),
      // never an override — see `SessionStore.agentSelectionSource`.
      const adopt = adoptsAgentHint(state)
      return {
        ...state,
        activeSessionId: sessionId,
        activeAgentId: adopt ? (agentId ?? state.activeAgentId) : state.activeAgentId,
        activeAgentType: adopt ? (agentType ?? state.activeAgentType) : state.activeAgentType,
        attachedSessionType: null,
        attachedTaskTitle: null,
      }
    })
    syncForeground()
    // Record real session ids under the current workspace for restore on re-entry.
    // Kickoff pending-session hardening: the '__pending' sentinel is a
    // LOCAL, transient placeholder for a turn that hasn't been acked by the
    // server yet — it is never a real, attachable session id. Recording it
    // here would poison sessionByWorkspace: the next time the user re-enters
    // this workspace, enterWorkspaceChat would call attachToSession('__pending'),
    // which the server rejects ("invalid session_id") after
    // resetChatBucketForReplay has already wiped the local bucket — repeating
    // on every re-entry until a full reload. Only ever record real ids.
    if (sessionId !== null && sessionId !== '__pending') {
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        set((state) => ({
          sessionByWorkspace: {
            ...state.sessionByWorkspace,
            [wsId]: {
              id: sessionId,
              type: priorSessionType ?? 'chat',
              title: priorTaskTitle,
              // The EFFECTIVE agent (post-write), not the raw hint — when a
              // user pin rejected the hint, remembering the hint would hand
              // the same losing value back on the next attach.
              agentId: get().activeAgentId,
            },
          },
        }))
      }
    }
  },

  setActiveAgentType: (type) => {
    // Precedence rule 3: the only caller (useSelectSession) derives `type`
    // from the ATTACHED SESSION's agent, immediately after the attach whose
    // agent hint may have been refused by a user pin. Writing it
    // unconditionally would leave `activeAgentType` describing a different
    // agent than `activeAgentId` — a silently mismatched pair.
    set((state) => (adoptsAgentHint(state) ? { activeAgentType: type } : state))
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
        return false
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
        // Precedence rule 3: the attached session's agent is a hint. This is
        // the exact write that used to clobber a user's pick — a background
        // attach re-pointed the composer at the session's creating agent
        // moments after the user had chosen someone else.
        activeAgentId: adoptsAgentHint(state) ? (agentId ?? state.activeAgentId) : state.activeAgentId,
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
              // Effective agent, not the raw hint — see setActiveSession.
              agentId: get().activeAgentId,
            },
          },
        }))
      }
      syncForeground()
      setChatReplaying(true)
      return true
    } else {
      console.warn('[session] attachToSession: no connection — attach_session not sent')
      logDiagnostic('sessionAttachToSessionNoConnection', { sessionId, type })
      // The click otherwise looks like it "succeeded" (activeSessionId updates,
      // the UI navigates) but the transcript replay never happens because the
      // attach_session frame was never sent — surface that to the user rather
      // than failing silently. Reattach happens automatically once the
      // connection is restored (WsLifecycle.onConnected → reattachActiveSession).
      useUiStore.getState().addToast({
        message: 'Not connected — this session will finish loading once your connection is restored.',
        variant: 'warning',
      })
      set((state) => ({
        activeSessionId: sessionId,
        attachedSessionType: type,
        attachedTaskTitle: title ?? null,
        // Precedence rule 3 — same hint semantics as the connected branch.
        activeAgentId: adoptsAgentHint(state) ? (agentId ?? state.activeAgentId) : state.activeAgentId,
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
              // Effective agent, not the raw hint — see setActiveSession.
              agentId: get().activeAgentId,
            },
          },
        }))
      }
      syncForeground()
      return true
    }
  },

  startNewSession: (agentId, agentType) => {
    set((state) => {
      // Precedence rule 3 — `agentId`/`agentType` are a hint here too, for
      // uniformity across every session-derived writer. A `/new` keeps the
      // user's pick either way (no caller passes an agent today), which is
      // intended: starting a fresh conversation is not un-choosing an agent.
      const adopt = adoptsAgentHint(state)
      return {
        activeSessionId: null,
        activeAgentId: adopt ? (agentId ?? state.activeAgentId) : state.activeAgentId,
        activeAgentType: adopt ? (agentType ?? state.activeAgentType) : state.activeAgentType,
        attachedSessionType: null,
        attachedTaskTitle: null,
      }
    })
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

  pruneSessionDescriptor: (sessionId) => {
    set((state) => {
      const next: Record<string, WorkspaceSessionDescriptor | null> = {}
      for (const [workspaceId, descriptor] of Object.entries(state.sessionByWorkspace)) {
        next[workspaceId] = descriptor?.id === sessionId ? null : descriptor
      }
      return { sessionByWorkspace: next }
    })
    // Mirrors the inline check this replaces: deleting the currently-attached
    // session must not leave chat pointed at a now-dead session id.
    if (get().activeSessionId === sessionId) {
      get().setActiveSession(null, null, null)
    }
  },

  sessionByWorkspace: {},

  enterWorkspaceChat: (workspaceId: string) => {
    // Precedence rule 2 is scoped to the workspace the pick was made in.
    // Every workspace has its own team roster, so carrying a pick from
    // workspace A into workspace B would leave the composer routing to an
    // agent that may not even be on B's team — and would block B's own
    // remembered agent from ever being restored. Release the pin BEFORE
    // reading state below, so this workspace's descriptor hint is adopted.
    if (get().agentSelectionSource === 'user' && get().agentSelectionWorkspaceId !== workspaceId) {
      set({ agentSelectionSource: 'auto', agentSelectionWorkspaceId: null })
    }
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

    if (descriptor.id === '__pending') {
      // Defense-in-depth: a legacy '__pending' descriptor
      // (written before setActiveSession stopped recording the sentinel, or
      // by any future code path that still does) must never be attached to
      // — the server rejects it as an invalid session_id, and
      // resetChatBucketForReplay would have already wiped the bucket for
      // nothing. Treat exactly like a null descriptor: start fresh instead.
      if (state.activeSessionId !== null) {
        state.startNewSession()
      }
      return
    }

    // Stored descriptor — no-op if it's already the active session (Bug 1 fix).
    if (descriptor.id === state.activeSessionId) {
      return
    }

    // Restore the previous conversation for this workspace. The boolean
    // return is intentionally ignored here: on failure (send() returned
    // false), attachToSession leaves state untouched — this descriptor still
    // points at the same session — and surfaces a connection error itself
    // via setConnectionError. There is no follow-on work in this function to
    // abort; the next enterWorkspaceChat for this workspace (re-entering it,
    // or after reconnect) simply retries the same attach.
    state.attachToSession(
      descriptor.id,
      descriptor.type,
      descriptor.title ?? undefined,
      descriptor.agentId ?? undefined,
    )
  },
}))
