/**
 * OmnipusChatScreen — Omnipus's one conversation, reached from its fixed
 * sidebar entry from any workspace (omnipus-system-agent FR-100, FR-106;
 * spec §6.3 row "Omnipus entry + attach"; ADR-20261011 OSA-10 U1, U9).
 *
 * The entry click attaches the main itself (src/store/omnipusChat.ts); this
 * screen attaches only on a direct load or reload of /omnipus. States:
 * resolving → "Opening Omnipus…"; main does not resolve or the attach is
 * refused → "Omnipus unavailable" + Retry, with no composer (sending blocked);
 * attached → the ordinary chat screen, whose own connection line covers
 * "reconnecting". The header links to Settings → System agents, where
 * Omnipus's model stays fixable without a working model (FR-106).
 */
import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { GearSix } from '@phosphor-icons/react'
import { fetchAgents, fetchSessions } from '@/lib/api'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'
import { OMNIPUS_AGENT_ID, omnipusMainSessionId } from '@/lib/omnipus'
import { openOmnipusChat } from '@/store/omnipusChat'
import { useSessionStore } from '@/store/session'
import { ScreenHeader } from '@/components/layout/ScreenHeader'
import { AgentMark } from '@/components/agents/AgentMark'
import { ErrorState } from '@/components/ui/error-state'
import { IconButton } from '@/components/ui/icon-button'
import { ChatScreen } from './ChatScreen'
import { LibraryOpenNotice } from './LibraryOpenNotice'
import { useChatPresence } from '@/hooks/useChatPresence'

export function OmnipusChatScreen() {
  const navigate = useNavigate()
  const workspacesQuery = useQuery({
    ...activeWorkspacesQueryOptions,
    staleTime: 30_000,
  })
  const sessionsQuery = useQuery({ queryKey: ['sessions'], queryFn: () => fetchSessions(), staleTime: 15_000 })
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents, staleTime: 30_000 })
  const omnipus = agentsQuery.data?.find((agent) => agent.id === OMNIPUS_AGENT_ID)

  const mainId = omnipusMainSessionId(workspacesQuery.data ?? [])
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const attached = !!mainId && activeSessionId === mainId
  // chat-links "Watching": Omnipus's chat reports presence too, and records no
  // workspace for bare links (Omnipus works across every workspace).
  useChatPresence(attached ? mainId : null, null)
  // The id this screen last tried to attach, so a refused attach shows Retry
  // instead of retrying in a loop.
  const [attemptedId, setAttemptedId] = useState<string | null>(null)

  useEffect(() => {
    if (!mainId || attached || attemptedId === mainId) return
    setAttemptedId(mainId)
    openOmnipusChat({ workspaces: workspacesQuery.data ?? [], sessions: sessionsQuery.data ?? [] })
  }, [mainId, attached, attemptedId, workspacesQuery.data, sessionsQuery.data])

  const pending = workspacesQuery.isPending
  const unavailable = !attached && ((!pending && !mainId) || (!!mainId && attemptedId === mainId))

  const retry = () => {
    setAttemptedId(null)
    void workspacesQuery.refetch()
    void sessionsQuery.refetch()
  }

  return (
    <div className="absolute inset-0 flex flex-col" data-testid="omnipus-chat-screen">
      <ScreenHeader
        title="Omnipus"
        actions={
          <>
          <AgentMark agent={omnipus} name="Omnipus" size={18} decorative={false} />
          <IconButton
            aria-label="Omnipus settings"
            title="Omnipus settings"
            data-testid="omnipus-chat-settings"
            onClick={() => void navigate({ to: '/settings', search: { tab: 'system-agents' } })}
            className="h-auto w-auto rounded p-[var(--space-1)] text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
          >
            <GearSix size={16} />
          </IconButton>
          </>
        }
      />
      <div className="relative flex-1 min-h-0">
        {attached && mainId ? (
          <>
            <ChatScreen />
            <LibraryOpenNotice sessionId={mainId} />
          </>
        ) : unavailable ? (
          <div className="flex h-full items-center justify-center px-[var(--space-3)]">
            <ErrorState message="Omnipus unavailable" onRetry={retry} />
          </div>
        ) : (
          <div
            className="flex h-full flex-col items-center justify-center gap-[var(--space-2-5)]"
            aria-live="polite"
            data-testid="omnipus-chat-opening"
          >
            <div
              className="h-8 w-8 rounded-full border-2 border-[var(--color-accent)] border-t-transparent animate-spin"
              aria-hidden="true"
            />
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Opening Omnipus…</p>
          </div>
        )}
      </div>
    </div>
  )
}
