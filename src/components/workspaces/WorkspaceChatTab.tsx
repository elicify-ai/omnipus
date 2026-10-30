import { UsersThree } from '@phosphor-icons/react'
import { ChatScreen } from '@/components/chat/ChatScreen'
import { LibraryOpenNotice } from '@/components/chat/LibraryOpenNotice'
import { useChatPresence } from '@/hooks/useChatPresence'
import { useSessionStore } from '@/store/session'
import { useOpenOmnipus } from '@/hooks/useOpenOmnipus'
import { Button } from '@/components/ui/button'
import { useActiveWorkspace } from './WorkspaceTabContainer'

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

  // omnipus-system-agent FR-107 / BDD-12.11: a team with no conversation
  // agent opens the empty state with "Set up a team" (send stays off).
  const emptyTeam = useSessionStore((s) => s.workspaceEntry?.status === 'empty-team')

  if (isRestoringSession) {
    return <ChatRestoreSkeleton />
  }

  if (emptyTeam) return <EmptyTeamEntry />

  return <WorkspaceChatView workspaceId={workspaceId} />
}

/**
 * The mounted chat view (chat-links spec "Watching"): reports presence for the
 * active session and hosts its "Open in Library" notice. Bare chat links
 * resolve against this workspace only while the active session is the one
 * this workspace recorded — during a workspace switch the previous
 * workspace's session is briefly still active, and its links must not
 * resolve against the new workspace.
 */
function WorkspaceChatView({ workspaceId }: WorkspaceChatTabProps) {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const recordedHere = useSessionStore(
    (s) => s.activeSessionId !== null && s.sessionByWorkspace[workspaceId]?.id === s.activeSessionId,
  )
  useChatPresence(sessionId, recordedHere ? workspaceId : null)
  return (
    <>
      <ChatScreen />
      {sessionId && <LibraryOpenNotice sessionId={sessionId} />}
    </>
  )
}

function EmptyTeamEntry() {
  const workspace = useActiveWorkspace()
  const openOmnipus = useOpenOmnipus()
  const start = (workflow: 'W1' | 'W18') =>
    openOmnipus({ workflow, workspaceId: workspace.id, workspaceName: workspace.name })
  return (
    <div
      className="absolute inset-0 flex flex-col items-center justify-center gap-[var(--space-2-5)] p-[var(--space-5)] text-center"
      data-testid="workspace-entry-empty-team"
    >
      <UsersThree size={32} weight="duotone" className="text-[var(--color-muted)]" aria-hidden="true" />
      <div>
        <p className="font-headline text-[length:var(--type-body-compact-size)] font-bold text-[var(--color-secondary)]">
          No agents in {workspace.name} yet
        </p>
        <p className="mt-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          Omnipus can set up a whole team for this workspace, or create one agent.
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-center gap-[var(--space-2)]">
        <Button data-testid="workspace-entry-set-up-team" onClick={() => start('W18')}>Set up a team</Button>
        <Button variant="outline" data-testid="workspace-entry-add-agent" onClick={() => start('W1')}>Add agent</Button>
      </div>
    </div>
  )
}

function ChatRestoreSkeleton() {
  return (
    <div
      className="flex flex-col absolute inset-0 items-center justify-center gap-[var(--space-2-5)]"
      data-testid="workspace-chat-restoring"
    >
      <div
        className="h-8 w-8 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin"
        aria-hidden="true"
      />
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Restoring your conversation…</p>
    </div>
  )
}
