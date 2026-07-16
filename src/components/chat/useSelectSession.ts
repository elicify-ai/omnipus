import { useNavigate } from '@tanstack/react-router'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
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
}

/**
 * Reusable session-selection logic.
 *
 * 1. Resolves `agentId = session.active_agent_id ?? session.agent_id`.
 * 2. If the session's `workspace_id` belongs to a different *existing* workspace,
 *    switches the active workspace before attaching.
 * 3. Calls `attachToSession` (sends the WS attach frame so the replay pipeline
 *    emits tool-call / subagent frames — without this, chat sessions render only
 *    the filtered REST payload and tool-call history is silently dropped).
 * 4. Seeds the token counter from the persisted total.
 * 5. Sets the active agent type for non-task sessions (composer behaviour).
 * 6. Navigates — to the workspace chat route whenever the session belongs to a
 *    known workspace, or to the standalone `/sessions/$sessionId` route when it
 *    does not (no `workspace_id`, or `workspace_id` points at a deleted
 *    workspace). That route's own "fallback inline path" (see
 *    `routes/_app/sessions.$sessionId.tsx`) renders the attached session with
 *    no workspace required — the one screen built for exactly this case — so
 *    an "Unfiled" session never attaches silently with nowhere to render it
 *    (previously: selecting one from a non-chat route like /agents or
 *    /settings attached in the background and navigated nowhere).
 * 7. Calls `onClose` to dismiss the sidebar / panel / modal.
 *
 * Does NOT call `setActiveSession` — it would call `resetChatSession()` a second
 * time, wiping the state `attachToSession` just initialized (including
 * `isReplaying=true` and `attachedSessionType`). See W2-1 regression test.
 */
export function useSelectSession(options: UseSelectSessionOptions) {
  const { agents, workspaces, onClose } = options
  const navigate = useNavigate()
  // useSessionStore() without a selector — matches the original SessionPanel
  // pattern. The token-test mock returns the full state object regardless of
  // selector, so destructuring is the compatible approach.
  const { attachToSession, setActiveAgentType } = useSessionStore()
  const seedSessionTokens = useChatStore((s) => s.seedSessionTokens)
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const setActiveWorkspaceId = useWorkspacesStore((s) => s.setActiveWorkspaceId)

  // Shared attach -> seed-tokens -> set-agent-type tail. Every branch below
  // needs this exact sequence, so it exists once here rather than duplicated
  // per branch (previously duplicated verbatim across the workspace-switch and
  // in-place branches).
  function attachAndSeed(session: Session, agentId: string | undefined) {
    attachToSession(session.id, session.type, session.title, agentId)
    if (session.total_tokens && session.total_tokens > 0) {
      seedSessionTokens(session.total_tokens)
    }
    if (session.type !== 'task') {
      const agent = agents.find((a) => a.id === agentId)
      if (agent?.type) {
        setActiveAgentType(agent.type)
      }
    }
  }

  return function selectSession(session: Session) {
    const agentId = session.active_agent_id ?? session.agent_id

    // If the session belongs to a different existing workspace, switch to it
    // before attaching. The workspace container's enterWorkspaceChat preserves
    // an already-active session, so it will NOT reset the one we just attached.
    const existingWorkspaceIds = new Set(workspaces.map((w) => w.id))
    const sessionWsId = session.workspace_id
    if (
      sessionWsId &&
      existingWorkspaceIds.has(sessionWsId) &&
      sessionWsId !== activeWorkspaceId
    ) {
      setActiveWorkspaceId(sessionWsId)
      attachAndSeed(session, agentId)
      onClose()
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: sessionWsId } })
      return
    }

    // Same workspace, no workspace, or deleted-workspace session — attach in place.
    attachAndSeed(session, agentId)
    onClose()

    if (sessionWsId && existingWorkspaceIds.has(sessionWsId)) {
      // Known workspace — the sidebar/search-modal entry points are globally
      // reachable (not just from chat routes), so always navigate to land the
      // user on the chat screen rather than leaving them stranded on
      // /agents or /settings.
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: sessionWsId } })
    } else {
      // "Unfiled" session (no workspace_id, or workspace_id points at a
      // deleted workspace) — there is no workspace-scoped route to land on.
      // Route to the standalone session view so the attach is always visible.
      void navigate({ to: '/sessions/$sessionId', params: { sessionId: session.id } })
    }
  }
}
