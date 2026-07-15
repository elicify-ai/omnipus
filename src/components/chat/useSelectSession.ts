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
 * 6. Navigates to the workspace chat route — only when switching workspaces.
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
      onClose()
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: sessionWsId } })
      return
    }

    // Same workspace, no workspace, or deleted-workspace session — attach in place.
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
    onClose()
    // Always navigate to the workspace chat route if the session has a known
    // workspace — the sidebar/search-modal entry points are globally reachable
    // (not just from chat routes), so without this the user would stay stranded
    // on /agents or /settings after selecting a same-workspace session.
    if (sessionWsId && existingWorkspaceIds.has(sessionWsId)) {
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: sessionWsId } })
    }
  }
}
