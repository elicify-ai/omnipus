import { ChatScreen } from '@/components/chat/ChatScreen'

interface WorkspaceChatTabProps {
  workspaceId: string
}

/**
 * The Chat tab — the workspace's default front view.
 *
 * Session lifecycle is managed by WorkspaceTabContainer via enterWorkspaceChat,
 * which runs once per workspace (keyed on workspace.id). That action either
 * restores the workspace's last conversation or starts a fresh composer.
 *
 * This component no longer calls startNewSession() on mount — doing so was
 * Bug 1: every Chat tab return wiped the active conversation because the
 * useEffect ran again on re-mount.
 */
export function WorkspaceChatTab(_props: WorkspaceChatTabProps) {
  return <ChatScreen />
}
