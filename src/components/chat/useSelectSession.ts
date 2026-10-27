import { useNavigate } from '@tanstack/react-router'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { isSidebarOverlayOpen, useSidebarStore } from '@/store/sidebar'
import { supersedeNavigationIntent } from '@/store/session/workspaceEntryFlow'
import type { Agent, Session, Workspace } from '@/lib/api'

export interface UseSelectSessionOptions {
  /** Agent list — used to resolve the active agent's type for the composer. */
  agents: Agent[]
  /** Active workspaces — used to decide whether a session's workspace_id exists
   *  and thus warrants a workspace switch + navigation. */
  workspaces: Workspace[]
  /** Called after the session has been attached — closes the sidebar / panel /
   *  modal the caller rendered the session list in. */
  onClose: () => void
  /**
   * Called only when this selection will attach the session: after a successful
   * attach, or when navigating to an unfiled session whose route attaches it.
   * A failed attach does not call this. Sessions and History no longer pass
   * it: Open goes straight to the chat and requests no Activity panel
   * (founder ruling "Straight to the chat").
   */
  onSelected?: (session: Session) => void
  /**
   * Replace the current history entry instead of pushing (default: push).
   * Only redirect-style callers want this: a pushed dead route would trap
   * the back button, bouncing off the redirect forever. Sidebar
   * rows and the search modal keep the default push — a real click there is
   * a place the user came from, and Back should return to it.
   */
  replace?: boolean
}

/**
 * Reusable session-selection logic.
 *
 * 1. Resolves `agentId = session.agent_id` — the immutable owner. `active_agent_id`
 *    is who last spoke, not where the next message goes (FR-009).
 * 2. If the session's `workspace_id` belongs to a different *existing* workspace,
 *    switches the active workspace before attaching.
 * 3. For a session with a KNOWN workspace (the currently-active one, or one
 *    we're about to switch to), calls `attachToSession` (sends the WS attach
 *    frame so the replay pipeline emits tool-call / subagent frames — without
 *    this, chat sessions render only the filtered REST payload and tool-call
 *    history is silently dropped). An "Unfiled" session (no `workspace_id`,
 *    or one pointing at a deleted workspace) is deliberately NOT attached
 *    here — see step 6.
 * 4. Seeds the token counter from the persisted total (known-workspace
 *    sessions only — seeding before an Unfiled session's real attach happens
 *    would write into whatever session bucket is CURRENTLY active, i.e. the
 *    wrong one).
 * 5. Sets the active agent type for non-task sessions (composer behaviour;
 *    known-workspace sessions only, same reasoning as step 4).
 * 6. Navigates — to the workspace chat route whenever the session belongs to a
 *    known workspace (attach already happened above), or to the standalone
 *    `/sessions/$sessionId` route when it does not, WITHOUT attaching first.
 *    That route owns the single attach for the Unfiled case (mirrors how the
 *    known-workspace path leaves `enterWorkspaceChat` to no-op when its
 *    descriptor already matches `activeSessionId`, session.ts Bug-1 fix) — so
 *    an "Unfiled" session never attaches silently with nowhere to render it
 *    (previously: selecting one from a non-chat route like /agents or
 *    /settings attached in the background and navigated nowhere), AND never
 *    double-attaches (previously: this hook attached with the real
 *    type/title, then the route's own effect attached AGAIN on mount with a
 *    hardcoded 'chat'/undefined type/title, wiping and re-labelling the
 *    session a second time).
 * 7. Calls `onClose` to dismiss the sidebar / panel / modal, and drops the
 *    sidebar OVERLAY when it is open — runs BEFORE `navigate` in every
 *    branch. A caller's `onClose` only closes its own surface: the search
 *    modal (Sessions, and an agent row's History) opens over an unpinned
 *    sidebar, and closing just the modal left the overlay and its
 *    click-outside layer covering the chat (founder Q81). Choosing a session
 *    is choosing a destination, which closes the overlay like every sidebar
 *    row does — also for a same-workspace switch, where the route path does
 *    not change. A docked sidebar is not an overlay and stays.
 *
 * `attachToSession` can fail even while the WS looks "connected": the
 * underlying `connection.send()` call can itself return false (e.g. mid
 * reconnect-backoff). When that happens it returns `false` and leaves store
 * state untouched (no `activeSessionId` switch, no bucket reset). This hook
 * checks that return value and, on failure, shows a toast and ABORTS before
 * seeding tokens / setting agent type / navigating / closing — a failed
 * attach must never masquerade as a successful session switch (previously:
 * the click was silently discarded, the user stayed on the OLD session with
 * no feedback, and on reconnect the OLD session got reattached).
 *
 * Does NOT call `setActiveSession` — it would call `resetChatSession()` a second
 * time, wiping the state `attachToSession` just initialized (including
 * `isReplaying=true` and `attachedSessionType`). See W2-1 regression test.
 */
export function useSelectSession(options: UseSelectSessionOptions) {
  const { agents, workspaces, onClose, onSelected, replace } = options
  const navigate = useNavigate()
  const { attachToSession, setActiveAgentType } = useSessionStore()
  const seedSessionTokens = useChatStore((s) => s.seedSessionTokens)
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const setActiveWorkspaceId = useWorkspacesStore((s) => s.setActiveWorkspaceId)
  const addToast = useUiStore((s) => s.addToast)

  // Shared attach -> seed-tokens -> set-agent-type tail. Every known-workspace
  // branch below needs this exact sequence, so it exists once here rather
  // than duplicated per branch. Returns whether the attach itself succeeded —
  // callers MUST check this and abort the rest of the selection flow (no
  // onClose, no navigate) when it's false; see the module docblock.
  function attachAndSeed(session: Session, agentId: string | undefined): boolean {
    const attached = attachToSession(session.id, session.type, session.title, agentId)
    if (!attached) return false
    seedSessionTokens(session.total_tokens, session.id)
    if (session.type !== 'task') {
      const agent = agents.find((a) => a.id === agentId)
      if (agent?.type) {
        setActiveAgentType(agent.type)
      }
    }
    return true
  }

  function closeEntrySurfaces() {
    onClose()
    if (isSidebarOverlayOpen()) useSidebarStore.getState().close()
  }

  function reportAttachFailure() {
    addToast({
      message: "Connection lost — couldn't open that session. It will not be switched.",
      variant: 'error',
    })
  }

  return function selectSession(session: Session) {
    const agentId = session.agent_id ?? undefined

    const existingWorkspaceIds = new Set(workspaces.map((w) => w.id))
    const sessionWsId = session.workspace_id
    const knownWorkspaceId =
      sessionWsId && existingWorkspaceIds.has(sessionWsId) ? sessionWsId : null

    if (knownWorkspaceId && knownWorkspaceId !== activeWorkspaceId) {
      // Switch and attach together. A failed send rolls the workspace back so
      // the displayed session, the owner, and the pointer stay one tuple.
      const previousWorkspaceId = activeWorkspaceId
      setActiveWorkspaceId(knownWorkspaceId)
      if (!attachAndSeed(session, agentId)) {
        if (previousWorkspaceId !== knownWorkspaceId) setActiveWorkspaceId(previousWorkspaceId)
        reportAttachFailure()
        return
      }
      supersedeNavigationIntent()
      onSelected?.(session)
      closeEntrySurfaces()
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: knownWorkspaceId }, replace })
      return
    }

    if (knownWorkspaceId) {
      // Already the active workspace (or no workspace switch needed) — attach
      // in place, then land the user on the chat screen. The sidebar/search
      // modal entry points are globally reachable (not just from chat
      // routes), so always navigate rather than leaving them stranded on
      // /agents or /settings.
      if (!attachAndSeed(session, agentId)) {
        reportAttachFailure()
        return
      }
      supersedeNavigationIntent()
      onSelected?.(session)
      closeEntrySurfaces()
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: knownWorkspaceId }, replace })
      return
    }

    // "Unfiled" session (no workspace_id, or workspace_id points at a
    // deleted workspace) — do NOT attach here. Navigate to the standalone
    // session view, which owns the single attach on mount (see the module
    // docblock, step 6).
    onSelected?.(session)
    closeEntrySurfaces()
    void navigate({ to: '/sessions/$sessionId', params: { sessionId: session.id }, replace })
  }
}
