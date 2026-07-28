import { create } from 'zustand'
import { fetchSessions } from '@/lib/api'
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

function readPersistedSessionByWorkspace(): PersistedSessionByWorkspace {
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
  setActiveSession: (
    sessionId: string | null,
    agentId?: string | null,
    agentType?: AgentKind | null
  ) => void
  setActiveAgentType: (type: AgentKind | null) => void
  attachedSessionType: 'chat' | 'task' | 'channel' | 'scheduled' | 'heartbeat' | null
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

export function resetChatBucketForReplay(sessionId: string): void {
  _chatResetForReplay?.(sessionId)
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
          agentId: (agentId ?? get().activeAgentId) ?? null,
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
        activeAgentId: agentId ?? state.activeAgentId,
      }))
      // Record under current workspace.
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        const descriptor: WorkspaceSessionDescriptor = {
          id: sessionId,
          type,
          title: title ?? null,
          agentId: (agentId ?? get().activeAgentId) ?? null,
        }
        set((state) => ({
          sessionByWorkspace: { ...state.sessionByWorkspace, [wsId]: descriptor },
        }))
        persistSessionByWorkspaceEntry(wsId, descriptor)
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
        activeAgentId: agentId ?? state.activeAgentId,
      }))
      // Record under current workspace (offline path).
      const wsId = useWorkspacesStore.getState().activeWorkspaceId
      if (wsId) {
        const descriptor: WorkspaceSessionDescriptor = {
          id: sessionId,
          type,
          title: title ?? null,
          agentId: (agentId ?? get().activeAgentId) ?? null,
        }
        set((state) => ({
          sessionByWorkspace: { ...state.sessionByWorkspace, [wsId]: descriptor },
        }))
        persistSessionByWorkspaceEntry(wsId, descriptor)
      }
      syncForeground()
      return true
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
    // Mark this workspace as intentionally fresh (null). Persisted too — a
    // reload immediately after "New chat" must honour this choice rather
    // than asking the server "what's most recent", which would resurrect
    // the exact session the user just walked away from.
    const wsId = useWorkspacesStore.getState().activeWorkspaceId
    if (wsId) {
      set((state) => ({
        sessionByWorkspace: {
          ...state.sessionByWorkspace,
          [wsId]: null,
        },
      }))
      persistSessionByWorkspaceEntry(wsId, null)
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

  enterWorkspaceChat: async (workspaceId: string) => {
    const state = get()
    const descriptor = state.sessionByWorkspace[workspaceId]

    if (descriptor === undefined) {
      // No LOCAL (in-memory) descriptor for this workspace — this is
      // ambiguous between "genuine first-ever visit to this workspace in
      // THIS browser" and "a reload just wiped the in-memory pointer while
      // THIS browser's own earlier decision is still intact". Those are NOT
      // the same thing, and only the localStorage-persisted marker (see the
      // "Cross-context session-bleed fix" block near the top of this file)
      // can tell them apart — asking the server "what's most recent in this
      // workspace" cannot, because that question is answerable regardless of
      // whether THIS browser has ever opened the workspace, and a positive
      // answer there previously caused a brand-new browser/tab/e2e-context
      // sharing a workspace with a prior conversation to silently inherit
      // it (chat.spec.ts (d), open-in-chat.spec.ts (b), delegation-hidden,
      // handoff, subagent — every e2e spec shares one gateway per shard).
      //
      // Clear a stale pointer from a DIFFERENT workspace/session context
      // immediately (mirrors the pre-fix synchronous startNewSession() call)
      // — WorkspaceChatTab's resolvingSessionForWorkspace gate hides
      // ChatScreen itself while a server round-trip is in flight, but
      // activeSessionId / activeAgentId also drive header/composer chrome
      // outside that gate, and must not keep pointing at the PREVIOUS
      // workspace's conversation.
      if (state.activeSessionId !== null) {
        set({ activeSessionId: null, attachedSessionType: null, attachedTaskTitle: null })
        syncForeground()
      }

      const persisted = readPersistedSessionByWorkspace()
      if (!Object.prototype.hasOwnProperty.call(persisted, workspaceId)) {
        // This browser has NEVER decided anything for this workspace —
        // land on a blank composer, exactly like a fresh install, WITHOUT
        // asking the server. Record the decision (via setWorkspaceSessionDescriptor,
        // which also persists) so a REAL reload of this SAME browser
        // restores correctly from here on; a brand-new browser/tab/test
        // context starts with no localStorage entry at all and takes this
        // same fresh path again — which is exactly the point.
        get().setWorkspaceSessionDescriptor(workspaceId, null)
        return
      }

      const remembered = persisted[workspaceId]
      if (remembered === null) {
        // This browser explicitly started fresh here (startNewSession)
        // before the reload — honour that choice. Must NOT ask the server,
        // which would happily hand back whatever session is now
        // most-recently-updated in the workspace, resurrecting the exact
        // conversation the user just left via "New chat".
        get().setWorkspaceSessionDescriptor(workspaceId, null)
        return
      }

      // This browser previously attached to a REAL session here — verify it
      // still exists server-side (it may have been deleted since, from
      // another tab or device) and restore EXACTLY that one. Deliberately
      // does NOT call startNewSession() first — that would ALSO write
      // sessionByWorkspace[workspaceId] = null, which would make
      // resolveRememberedSessionFromServer's "someone already claimed this"
      // guard see its own write and abort before ever verifying.
      await resolveRememberedSessionFromServer(workspaceId, remembered)
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

// ── D4 fix, revised: browser-scoped session restore ──────────────────────────
//
// sessionByWorkspace is a plain in-memory Zustand slice — a reload wipes it
// to `{}` even though the server's day-partitioned JSONL sessions are still
// fully intact, so enterWorkspaceChat's `descriptor === undefined` branch
// used to treat every cold reload identically to a genuine first-ever visit
// and silently landed on a blank Welcome screen (RCA: 3 UAT testers, D4).
//
// The ORIGINAL D4 fix resolved that by asking the server which session the
// WORKSPACE was most recently showing (fetchSessions(), filter by
// workspace_id, sort by updated_at, take the top one) — a question answerable
// regardless of whether THIS BROWSER had ever opened the workspace. That is
// exactly what let a brand-new browser/tab/e2e-context sharing a workspace
// with an existing conversation silently inherit it: every e2e spec file
// shares one gateway/workspace per CI shard (`playwright.config.ts`
// `workers: 1`), so a later spec's fresh context would restore an EARLIER
// spec's conversation (chat.spec.ts (d), open-in-chat.spec.ts (b),
// delegation-hidden.spec.ts, handoff.spec.ts, subagent.spec.ts).
//
// This revision keeps the network round-trip only for the case it actually
// needs to happen: THIS browser previously attached to a KNOWN session id in
// this workspace (see the localStorage-persisted `readPersistedSessionByWorkspace`
// near the top of this file), and we're verifying it still exists rather than
// guessing which is "most recent" workspace-wide.

// Module-level de-dupe: guards against concurrent duplicate server
// round-trips when enterWorkspaceChat fires more than once for the same
// workspace before the first resolution lands (e.g. a fast back-and-forth
// workspace switch, or an effect re-run before the fetch settles).
const inFlightWorkspaceResolutions = new Set<string>()

async function resolveRememberedSessionFromServer(
  workspaceId: string,
  remembered: WorkspaceSessionDescriptor,
): Promise<void> {
  if (inFlightWorkspaceResolutions.has(workspaceId)) return
  inFlightWorkspaceResolutions.add(workspaceId)
  useSessionStore.setState((state) => ({
    resolvingSessionForWorkspace: { ...state.resolvingSessionForWorkspace, [workspaceId]: true },
  }))

  try {
    const sessions = await fetchSessions()

    // The workspace may have already been claimed by another code path while
    // this request was in flight — a deep-link redirect
    // (setWorkspaceSessionDescriptor in sessions.$sessionId.tsx), a manual
    // session pick (useSelectSession / attachToSession), or the user
    // starting a fresh conversation (startNewSession, which writes `null`).
    // Whichever got there first wins; this resolution must not clobber it.
    if (useSessionStore.getState().sessionByWorkspace[workspaceId] !== undefined) {
      return
    }

    // Verify the REMEMBERED session (the one THIS browser was last looking
    // at) still exists — not "whatever's most recent in the workspace",
    // which could belong to a different browser/tab/test that touched the
    // same workspace more recently.
    const stillExists = sessions.find((s) => s.id === remembered.id)

    if (!stillExists) {
      // Deleted since our last visit (this browser via another tab, another
      // device, or an admin/API action) — gracefully fall back to fresh
      // rather than attaching to a dead id. Record it explicitly (not left
      // `undefined`) so a same-tab re-entry doesn't re-fetch every time.
      useSessionStore.getState().setWorkspaceSessionDescriptor(workspaceId, null)
      return
    }

    const agentId = stillExists.active_agent_id ?? stillExists.agent_id

    // Write the descriptor by explicit key first (mirrors the deep-link
    // route's race-free handoff in sessions.$sessionId.tsx) so it's recorded
    // even if the user has since navigated away from this workspace — see
    // the activeWorkspaceId check below.
    useSessionStore.getState().setWorkspaceSessionDescriptor(workspaceId, {
      id: stillExists.id,
      type: stillExists.type,
      title: stillExists.title,
      agentId,
    })

    // Only actually attach (send attach_session, reset the chat bucket, flip
    // isReplaying) if this workspace is STILL the one being viewed —
    // attachToSession records under whatever workspace is CURRENTLY active
    // (useWorkspacesStore.getState().activeWorkspaceId), so attaching after
    // the user has moved on would corrupt a DIFFERENT workspace's pointer.
    // If they've moved on, the descriptor written above is enough — the next
    // enterWorkspaceChat for this workspace (returning to it) will find it
    // and attach then.
    if (useWorkspacesStore.getState().activeWorkspaceId !== workspaceId) {
      return
    }
    if (useSessionStore.getState().activeSessionId === stillExists.id) {
      return
    }
    useSessionStore.getState().attachToSession(stillExists.id, stillExists.type, stillExists.title, agentId)
  } catch (err) {
    // Fetch failed (network/5xx) — deliberately leave
    // sessionByWorkspace[workspaceId] unset (still `undefined`, NOT `null`)
    // so the NEXT enterWorkspaceChat for this workspace (re-entering it, or
    // a manual reload) retries rather than permanently mislabeling a real
    // session as "explicitly fresh". Welcome renders in the meantime — a
    // retryable gap, not a silent dead end. The localStorage-persisted
    // "remembered" entry is untouched, so the retry has something to verify
    // against again.
    console.warn('[session] resolveRememberedSessionFromServer failed', err)
    logDiagnostic('sessionResolveWorkspaceSessionFailed', { workspaceId, error: String(err) })
    useUiStore.getState().addToast({
      message: 'Could not restore your last conversation — starting a new one.',
      variant: 'warning',
    })
  } finally {
    inFlightWorkspaceResolutions.delete(workspaceId)
    useSessionStore.setState((state) => {
      const next = { ...state.resolvingSessionForWorkspace }
      delete next[workspaceId]
      return { resolvingSessionForWorkspace: next }
    })
  }
}
