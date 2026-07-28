import { ChatScreen } from '@/components/chat/ChatScreen'
import { useSessionStore } from '@/store/session'

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
 *
 * D4 fix: enterWorkspaceChat now resolves an unknown-local-state workspace's
 * most-recent session from the server (a cold reload wipes the in-memory
 * sessionByWorkspace pointer, but the server's sessions are still intact).
 * That round-trip is asynchronous — while it's in flight this renders a
 * loading state instead of ChatScreen, so the user never sees the
 * "Select an agent" Welcome screen flash before the real conversation
 * (or the genuinely-empty Welcome state) lands.
 */
export function WorkspaceChatTab({ workspaceId }: WorkspaceChatTabProps) {
  const isRestoringSession = useSessionStore(
    (s) => s.resolvingSessionForWorkspace[workspaceId] === true
  )

  if (isRestoringSession) {
    return <ChatRestoreSkeleton />
  }

  return <ChatScreen />
}

function ChatRestoreSkeleton() {
  return (
    <div
      className="flex flex-col absolute inset-0 items-center justify-center gap-3"
      data-testid="workspace-chat-restoring"
    >
      <div
        className="h-8 w-8 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin"
        aria-hidden="true"
      />
      <p className="text-xs text-[var(--color-muted)]">Restoring your conversation…</p>
    </div>
  )
}
