import { create } from 'zustand'
import type { AgentKind, Session } from '@/lib/api'
import { isMainSession } from '@/lib/nav/sessionCoreSeam'
import { workspaceEntryBlocksSend } from '@/lib/nav/workspaceEntry'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { logDiagnostic } from '@/lib/telemetry'
import { noteForegroundAttach } from '@/store/session/foregroundAck'
import { runStartNewSession, type NewChatStartArg } from '@/store/session/newChatFlow'
import type { NewChatPrompt } from '@/store/session/newChatFlow'
import { resumeWorkspaceEntryQueue, runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'
import type { WorkspaceEntryView } from '@/store/session/workspaceEntryFlow'

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

// ── Cross-context session-bleed fix ─────────────────────────────────────────
//
// `sessionByWorkspace` (below) is a plain in-memory Zustand slice — a reload
// wipes it to `{}`, and so does opening a brand-new browser/tab/test context
// that has never touched this app before. Those two situations are NOT the
// same thing, but `enterWorkspaceChat`'s `descriptor === undefined` branch
// used to treat them identically and ask the SERVER "what's the most
// recently updated session in this workspace?" (resolveRememberedSessionFromServer
// below) either way.
//
// That question is answerable regardless of whether THIS BROWSER has ever
// been in the workspace — it's a workspace-scoped query, not a browser-scoped
// one. A brand-new browser/tab/test run sharing a workspace with an existing
// conversation (real multi-device use, or — concretely — every e2e spec file
// sharing one gateway/workspace per CI shard, `playwright.config.ts`
// `workers: 1`) would silently inherit whatever conversation was most
// recently active there, even though THIS client never opened it. That is
// the exact CI regression this fixes: chat.spec.ts (d), open-in-chat.spec.ts
// (b), delegation-hidden.spec.ts, handoff.spec.ts, and subagent.spec.ts all
// saw a fresh context's page load restore a PRIOR spec's conversation.
//
// The fix: persist `sessionByWorkspace` to localStorage, scoped to THIS
// browser. A real reload of a browser that has decided about a workspace
// before (whether that decision was "attached to session S" or "explicitly
// started fresh") rehydrates that exact decision from localStorage — no
// server guess needed, and no way to resurrect a session a "New chat" click
// just walked away from (that decision is `null`, and `null` persists too).
// A browser/tab/test that has NEVER decided anything for a workspace has no
// localStorage entry for it at all, and lands on a blank composer without
// ever asking the server — closing the cross-context bleed.
const PERSISTED_SESSION_BY_WORKSPACE_KEY = 'omnipus.sessionByWorkspace.v1'

type PersistedSessionByWorkspace = Record<string, WorkspaceSessionDescriptor | null>

export function readPersistedSessionByWorkspace(): PersistedSessionByWorkspace {
  try {
    const raw = localStorage.getItem(PERSISTED_SESSION_BY_WORKSPACE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as PersistedSessionByWorkspace
    }
    return {}
  } catch {
    // Corrupt JSON or localStorage unavailable (private-mode quota, SSR,
    // disabled storage) — degrade to "no browser has ever decided anything",
    // i.e. every workspace starts fresh. Never crash the chat shell over
    // a restore-convenience feature.
    return {}
  }
}

function persistSessionByWorkspaceEntry(
  workspaceId: string,
  descriptor: WorkspaceSessionDescriptor | null,
): void {
  try {
    const all = readPersistedSessionByWorkspace()
    all[workspaceId] = descriptor
    localStorage.setItem(PERSISTED_SESSION_BY_WORKSPACE_KEY, JSON.stringify(all))
  } catch {
    // localStorage write failed (quota/private mode) — the in-memory value
    // the caller just set() is still correct for the rest of this page life;
    // only the NEXT reload's restore degrades to "start fresh", not a crash.
  }
}

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
  agentSelectionSource: AgentSelectionSource | null
  /**
   * Retired with the composer agent picker. Kept on the slice so older tests
   * can still reset it; every writer now clears it. The session owner is the
   * send destination — there is no user pin.
   */
  agentSelectionWorkspaceId: string | null
  /** The validated main id per `${workspaceId}::${agentId}`. An extra never replaces it. */
  mainPointerByPair: Record<string, string>
  /** Set when + New chat must ask before abandoning an unconfirmed first send. */
  newChatPrompt: NewChatPrompt | null
  /** Last workspace-entry decision. `sendEnabled: false` blocks send. */
  workspaceEntry: WorkspaceEntryView | null
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
  startNewSession: (
    agentId?: NewChatStartArg,
    agentType?: AgentKind | null,
  ) => void
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
  /**
   * D4 fix (revised — see the "Cross-context session-bleed fix" block near
   * the top of this file): `sessionByWorkspace` is an in-memory-only
   * pointer — a reload wipes it to `{}`. When called with a workspace that
   * has no LOCAL descriptor (`undefined`), this consults the
   * localStorage-persisted marker for THIS BROWSER before deciding what to
   * do: no persisted entry at all (or an explicitly-fresh `null`) resolves
   * immediately to a blank composer with no network call; a persisted REAL
   * session id is verified against the server
   * (`resolveRememberedSessionFromServer`) and attached to before falling
   * back to a fresh composer if it was deleted. Returns a Promise so
   * callers/tests can await the server round-trip in that last branch; every
   * other branch resolves immediately.
   */
  enterWorkspaceChat: (workspaceId: string) => Promise<void>
  /**
   * Workspaces currently mid-flight verifying a remembered session against
   * the server (see `enterWorkspaceChat` / `resolveRememberedSessionFromServer`
   * below). Read by `WorkspaceChatTab` to show a loading state instead of a
   * premature "select an agent" Welcome screen while the restore is in
   * flight. Never set for a workspace this browser has no persisted
   * knowledge of — that case resolves synchronously.
   */
  resolvingSessionForWorkspace: Record<string, boolean>
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

// ADR-092 UX fix: same cycle-break pattern as _chatSetReplaying above.
// startNewSession, attachToSession, and enterWorkspaceChat below all clear
// this so a per-chat Auto-approve choice made in a chat that was then
// abandoned (no message ever sent, so the choice was never consumed by
// session_started) does not silently leak onto a LATER, unrelated chat,
// whether that chat is reached by attaching to a session, starting fresh, or
// switching workspaces (even into a workspace that itself resolves to no
// session at all). startNewSession and enterWorkspaceChat call this
// unconditionally, as the very first thing each does — every branch of
// either is a genuine chat/workspace change. attachToSession is the one
// exception: its failed-send branch (`!sent`) deliberately does NOT clear —
// Wave-1 Bug 2's contract is that a failed send leaves ALL store state
// untouched (the attach never happened, so whatever chat the user was
// already on, pending choice included, is unchanged) — so the clear call
// there is scoped to its two branches that DO change state: the
// connected+sent branch (its own explicit call — #823 no longer wipes the
// bucket there via resetChatBucketForReplay) and the
// offline/no-connection branch (its own explicit call). Deliberately NOT
// wired into setActiveSession itself — sendMessage's own pending-session
// mint (session id '__pending') and sendWorkspaceSetupKickoff both call
// setActiveSession too, and clearing there would erase the very choice this
// fix exists to carry through to session_started.
let _chatClearPendingAutoApprove: (() => void) | null = null

/** Called once by chat.ts after it creates useChatStore. */
export function registerChatClearPendingAutoApprove(fn: () => void): void {
  _chatClearPendingAutoApprove = fn
}

let _chatAbandonPendingFirstSend: (() => void) | null = null

/** Same cycle-break wiring: /new releases the ordinary pending bucket synchronously. */
export function registerChatAbandonPendingFirstSend(fn: () => void): void {
  _chatAbandonPendingFirstSend = fn
}

/**
 * The clear point called by startNewSession, enterWorkspaceChat, and (on its
 * two state-changing branches only) attachToSession — see the doc comment
 * above.
 */
export function clearPendingAutoApproveOnSessionChange(): void {
  _chatClearPendingAutoApprove?.()
}

export function abandonRegisteredPendingFirstSend(): void {
  _chatAbandonPendingFirstSend?.()
}

export type PendingFirstSendSnapshot = {
  sessionId: string | null
  clientMessageId: string
  text: string
  status: 'sending' | 'unconfirmed' | 'retrying' | 'not_saved' | 'check_failed' | 'saved'
} | null

let _readPendingFirstSend: () => PendingFirstSendSnapshot = () => null

/** Called once by chat.ts. startNewSession reads the first-send slot through this. */
export function registerReadPendingFirstSend(fn: () => PendingFirstSendSnapshot): void {
  _readPendingFirstSend = fn
}

export function readPendingFirstSend(): PendingFirstSendSnapshot {
  return _readPendingFirstSend()
}

function rememberMainPointer(
  get: () => SessionStore,
  set: (partial: Partial<SessionStore> | ((state: SessionStore) => Partial<SessionStore>)) => void,
  sessionId: string,
): void {
  const workspaceId = useWorkspacesStore.getState().activeWorkspaceId
  const owner = get().activeAgentId
  if (!workspaceId || !owner || !isMainSession({ id: sessionId })) return
  const key = `${workspaceId}::${owner}`
  set((state) => ({
    mainPointerByPair: { ...state.mainPointerByPair, [key]: sessionId },
  }))
}

export function resetChatBucketForReplay(sessionId: string): void {
  _chatResetForReplay?.(sessionId)
  _chatClearPendingAutoApprove?.()
}

// #823 catch-up redesign (BE-DESIGN.md §6.1) — same circular-import-break
// pattern as _chatResetForReplay above. chat.ts registers a getter for a
// session bucket's cursor (src/store/chat/types.ts's SessionCursor) so the
// attach path here can send `attach_session{since_seq, boot_id}` instead of
// wiping the bucket on every (re)attach — only a `session_snapshot` frame
// wipes now (§6.1: "no longer wipe the bucket ... Only session_snapshot
// wipes").
let _getSessionCursor: ((sessionId: string) => { bootId: string; seq: number } | null) | null = null

/** Called once by chat.ts after it creates useChatStore. */
export function registerGetSessionCursor(fn: (sessionId: string) => { bootId: string; seq: number } | null): void {
  _getSessionCursor = fn
}

/**
 * The wire-shaped `since_seq`/`boot_id` pair to send on `attach_session` for
 * `sessionId`, or `{}` when this browser has no cursor for it yet (first
 * attach ever, or the getter isn't registered — e.g. a unit test that
 * constructs this store without chat.ts's registration side effect). The
 * server's own §3.3 rule decides whether a sent cursor is still servable;
 * this function's only job is "send what we have, or nothing."
 */
export function attachSessionCursorFields(sessionId: string): { since_seq?: number; boot_id?: string } {
  const cursor = _getSessionCursor?.(sessionId) ?? null
  return cursor ? { since_seq: cursor.seq, boot_id: cursor.bootId } : {}
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
function clearRetiredSelection(): { agentSelectionSource: null; agentSelectionWorkspaceId: null } {
  return { agentSelectionSource: null, agentSelectionWorkspaceId: null }
}

/** Same-session hints must not replace the owner already attached to this chat. */
function keepAttachedOwner(
  state: Pick<SessionStore, 'activeSessionId' | 'activeAgentId'>,
  sessionId: string | null,
): boolean {
  return sessionId === state.activeSessionId && state.activeAgentId != null
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
  agentSelectionSource: null,
  agentSelectionWorkspaceId: null,
  mainPointerByPair: {},
  newChatPrompt: null,
  workspaceEntry: null,

  applyServerAgentSwitch: (sessionId, agentId, agentType) => {
    // Force the answering agent first. setActiveSession keeps an already
    // attached owner, so a same-session handover would otherwise be ignored.
    set({
      activeAgentId: agentId,
      activeAgentType: agentType ?? get().activeAgentType,
      ...clearRetiredSelection(),
    })
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
      const keep = keepAttachedOwner(state, sessionId)
      return {
        ...state,
        activeSessionId: sessionId,
        activeAgentId: keep ? state.activeAgentId : (agentId ?? state.activeAgentId),
        activeAgentType: keep ? state.activeAgentType : (agentType ?? state.activeAgentType),
        attachedSessionType: null,
        attachedTaskTitle: null,
        ...clearRetiredSelection(),
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
        const descriptor: WorkspaceSessionDescriptor = {
          id: sessionId,
          type: priorSessionType ?? 'chat',
          title: priorTaskTitle,
          // The EFFECTIVE agent (post-write, read after the precedence-rule
          // set() above already ran) — not the raw hint. When a user pin
          // rejected the hint, remembering the raw hint would hand the same
          // losing value back on the next attach.
          agentId: get().activeAgentId ?? null,
        }
        set((state) => ({
          sessionByWorkspace: { ...state.sessionByWorkspace, [wsId]: descriptor },
        }))
        // Persist so a REAL reload of this browser restores exactly this
        // decision instead of re-asking the server "what's most recent in
        // the workspace" — see the cross-context session-bleed fix above.
        persistSessionByWorkspaceEntry(wsId, descriptor)
      }
    }
  },

  setActiveAgentType: (type) => {
    set({ activeAgentType: type })
  },

  attachedSessionType: null,
  attachedTaskTitle: null,

  attachToSession: (sessionId, type, title, agentId) => {
    // A hidden document is a prefetch, not a foreground open (FR-013).
    if (typeof document !== 'undefined' && document.hidden === true) return false
    const { connection } = useConnectionStore.getState()
    const entryWasBlocked = workspaceEntryBlocksSend(get(), useWorkspacesStore.getState().activeWorkspaceId)

    if (connection) {
      // #823 §6.1: send the bucket's cursor; only session_snapshot wipes now (Q3).
      const sent = connection.send({ type: 'attach_session', session_id: sessionId, ...attachSessionCursorFields(sessionId) })
      if (!sent) {
        // Wave-1 Bug 2: leave ALL state, pending Auto choice included, untouched.
        useConnectionStore.getState().setConnectionError(
          'Could not attach to session — connection dropped. Please reconnect and try again.'
        )
        return false
      }
      clearPendingAutoApproveOnSessionChange() // ADR-092: a sent attach IS a chat change (see the doc above)
      set((state) => ({
        activeSessionId: sessionId,
        attachedSessionType: type,
        attachedTaskTitle: title ?? null,
        // The argument is the immutable session owner (session.agent_id).
        activeAgentId: agentId ?? state.activeAgentId,
        workspaceEntry: null,
        ...clearRetiredSelection(),
      }))
      // Record under current workspace.
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        const descriptor: WorkspaceSessionDescriptor = {
          id: sessionId,
          type,
          title: title ?? null,
          // Effective agent, not the raw hint — see setActiveSession.
          agentId: get().activeAgentId ?? null,
        }
        set((state) => ({
          sessionByWorkspace: { ...state.sessionByWorkspace, [wsId]: descriptor },
        }))
        persistSessionByWorkspaceEntry(wsId, descriptor)
      }
      syncForeground()
      rememberMainPointer(get, set, sessionId)
      noteForegroundAttach(sessionId)
      setChatReplaying(true)
      if (entryWasBlocked) queueMicrotask(resumeWorkspaceEntryQueue)
      return true
    } else {
      clearPendingAutoApproveOnSessionChange() // unlike failed-send, this branch changes state
      console.warn('[session] attachToSession: no connection — attach_session not sent')
      logDiagnostic('sessionAttachToSessionNoConnection', { sessionId, type })
      // The click would otherwise look like it "succeeded" with no replay ever
      // arriving — surface it. Reattach retries automatically on reconnect
      // (WsLifecycle.onConnected → reattachActiveSession).
      useUiStore.getState().addToast({
        message: 'Not connected — this session will finish loading once your connection is restored.',
        variant: 'warning',
      })
      set((state) => ({
        activeSessionId: sessionId,
        attachedSessionType: type,
        attachedTaskTitle: title ?? null,
        activeAgentId: agentId ?? state.activeAgentId,
        workspaceEntry: null,
        ...clearRetiredSelection(),
      }))
      // Record under current workspace (offline path).
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        const descriptor: WorkspaceSessionDescriptor = {
          id: sessionId,
          type,
          title: title ?? null,
          // Effective agent, not the raw hint — see setActiveSession.
          agentId: get().activeAgentId ?? null,
        }
        set((state) => ({
          sessionByWorkspace: { ...state.sessionByWorkspace, [wsId]: descriptor },
        }))
        persistSessionByWorkspaceEntry(wsId, descriptor)
      }
      syncForeground()
      rememberMainPointer(get, set, sessionId)
      noteForegroundAttach(sessionId)
      if (entryWasBlocked) queueMicrotask(resumeWorkspaceEntryQueue)
      return true
    }
  },

  startNewSession: (agentId, agentType) => {
    runStartNewSession(agentId, agentType)
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
    persistSessionByWorkspaceEntry(workspaceId, descriptor)
  },

  pruneSessionDescriptor: (sessionId) => {
    const changedWorkspaceIds: string[] = []
    set((state) => {
      const next: Record<string, WorkspaceSessionDescriptor | null> = {}
      for (const [workspaceId, descriptor] of Object.entries(state.sessionByWorkspace)) {
        if (descriptor?.id === sessionId) {
          next[workspaceId] = null
          changedWorkspaceIds.push(workspaceId)
        } else {
          next[workspaceId] = descriptor
        }
      }
      return { sessionByWorkspace: next }
    })
    // Persist each nulled workspace too — otherwise a reload right after
    // deleting the active session would re-hydrate the STALE persisted
    // descriptor (still pointing at the now-dead session id) and try to
    // attach to it again.
    for (const workspaceId of changedWorkspaceIds) {
      persistSessionByWorkspaceEntry(workspaceId, null)
    }
    // Mirrors the inline check this replaces: deleting the currently-attached
    // session must not leave chat pointed at a now-dead session id.
    if (get().activeSessionId === sessionId) {
      get().setActiveSession(null, null, null)
    }
  },

  sessionByWorkspace: {},
  resolvingSessionForWorkspace: {},

  enterWorkspaceChat: (workspaceId: string) => runWorkspaceEntry(workspaceId),
}))
