/**
 * The fixed Omnipus entry, above the workspaces (omnipus-system-agent
 * FR-100, FR-101; BDD-12.1, BDD-12.2, BDD-10.2/10.3/10.6; spec §6.3 rows
 * "Omnipus entry + attach" and "Attention marker"; F:D15, F:D16, F:D23).
 *
 * One row, reachable from every workspace, opening Omnipus's one chat without
 * changing the active workspace (useOpenOmnipus). Its attention is its main's
 * `needs_attention` read through the session-core seam: on → the halo plus a
 * text cue; unknown → an explicit "attention unknown" cue, never a silent
 * "nothing pending"; off → no marker. While the workspace list loads the row
 * is a skeleton; when Omnipus's main does not resolve the row says
 * "Omnipus unavailable" with Retry (the chat itself then blocks sending).
 */
import { useQueryClient } from '@tanstack/react-query'
import type { Agent, Session, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { OMNIPUS_AGENT_ID, omnipusMainSessionId } from '@/lib/omnipus'
import { sessionAttention } from '@/lib/nav/sessionCoreSeam'
import { useOpenOmnipus } from '@/hooks/useOpenOmnipus'
import { useSessionStore } from '@/store/session'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { SidebarAgentIcon } from './SidebarAgentIcon'
import { useAttentionMotion } from './attentionCue'
import './attention.css'

export function OmnipusEntry({
  workspaces,
  workspacesLoading,
  agents,
  sessions,
  onOpened,
}: {
  workspaces: Workspace[]
  workspacesLoading: boolean
  agents: Agent[]
  sessions: Session[]
  /** Closes the overlay sidebar after the entry opened the chat. */
  onOpened: () => void
}) {
  const queryClient = useQueryClient()
  const openOmnipus = useOpenOmnipus()
  const motion = useAttentionMotion()
  const mainId = omnipusMainSessionId(workspaces)
  const selected = useSessionStore((s) => !!mainId && s.activeSessionId === mainId)
  const omnipus = agents.find((agent) => agent.id === OMNIPUS_AGENT_ID)

  if (workspacesLoading) {
    return (
      <div className="px-[var(--space-3)] py-[var(--space-1)]" data-testid="omnipus-entry-loading">
        <Skeleton className="h-[var(--space-5)] w-full" />
      </div>
    )
  }

  const session = mainId ? sessions.find((candidate) => candidate.id === mainId) : undefined
  const attention = mainId ? sessionAttention(session) : 'unknown'
  const cue = !mainId
    ? null
    : attention === 'on'
      ? 'Needs your attention'
      : attention === 'unknown'
        ? 'Attention unknown'
        : null

  const retry = () => {
    void queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list({ status: 'active' }), exact: true })
    void queryClient.invalidateQueries({ queryKey: ['sessions'] })
  }

  return (
    <div className="px-[var(--space-2)] pb-[var(--space-1)]" role="group" aria-label="Omnipus">
      <div
        data-selected={selected}
        className="sidebar-agent-row flex items-center rounded-[var(--radius-medium)] hover:bg-[var(--color-surface-2)]"
      >
        <Button
          variant="ghost"
          data-testid="sidebar-omnipus-entry"
          aria-current={selected ? 'page' : undefined}
          aria-describedby={cue ? 'sidebar-omnipus-attention' : undefined}
          onClick={() => {
            if (openOmnipus() !== 'attach-failed') onOpened()
          }}
          className={cn(
            'h-auto min-w-0 flex-1 justify-start gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] hover:bg-transparent hover:text-[var(--color-secondary)] pointer-coarse:min-h-[var(--target-touch-minimum)]',
            selected ? 'font-semibold' : 'font-[var(--font-weight-regular)]',
          )}
        >
          <SidebarAgentIcon agent={omnipus} name="Omnipus" halo={attention === 'on'} motion={motion} />
          <span className="truncate">Omnipus</span>
        </Button>
        {cue && (
          <span
            id="sidebar-omnipus-attention"
            role="status"
            data-testid="sidebar-omnipus-attention"
            data-attention={attention}
            className="mr-[var(--space-2)] inline-flex shrink-0 items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
          >
            {attention === 'on' && (
              <span
                aria-hidden="true"
                className={cn(
                  'inline-block h-[var(--space-2)] w-[var(--space-2)] shrink-0 rounded-full bg-[var(--color-warning)]',
                  motion === 'loop' && 'sidebar-attention-dot',
                )}
              />
            )}
            {cue}
          </span>
        )}
      </div>
      {!mainId && (
        <div className="flex items-center gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-1)]">
          <span role="status" className="flex-1 text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
            Omnipus unavailable
          </span>
          <Button
            variant="ghost"
            aria-label="Retry loading Omnipus"
            data-testid="sidebar-omnipus-retry"
            onClick={retry}
            className="h-auto shrink-0 px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)]"
          >
            Retry
          </Button>
        </div>
      )}
    </div>
  )
}
