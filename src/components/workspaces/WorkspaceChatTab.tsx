import { useEffect, useRef } from 'react'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { useSessionStore } from '@/store/session'

interface WorkspaceChatTabProps {
  workspaceId: string
}

/**
 * The Chat tab — the workspace's default front view.
 *
 * Chat is reframed as a workspace view: on entering a workspace we land on a
 * clean composer (a fresh session), scoped to the workspace. The active
 * workspace is already bound by WorkspaceTabContainer, which drives:
 *   - the agent picker (SessionBar) → scoped to the workspace's team
 *   - the Sessions panel (SessionPanel) → filtered by this workspace_id
 *
 * Unlike the old global "/" chat route, this view keeps the URL on
 * /workspaces/$id/chat (deep-linkable tab) rather than bouncing to
 * /sessions/$id. Opening a past conversation attaches it in place via the
 * SessionPanel; the workspace tab stays active.
 */
export function WorkspaceChatTab(_props: WorkspaceChatTabProps) {
  const startNewSession = useSessionStore((s) => s.startNewSession)
  // Start a fresh session once on mount so entering the workspace lands on a
  // clean composer rather than the previous conversation. Run-once: the store
  // action is a stable Zustand reference. A ref guards against double-invoke in
  // React strict mode.
  const startedRef = useRef(false)
  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true
    startNewSession()
  }, [startNewSession])

  return <ChatScreen />
}
