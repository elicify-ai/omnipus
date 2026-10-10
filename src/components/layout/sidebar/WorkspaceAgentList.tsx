import { useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Buildings, CaretDown, CaretRight, ClockCounterClockwise, Plus } from '@phosphor-icons/react'
import type { Agent, Session, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { decideNewChat } from '@/lib/nav/extraChatGuard'
import type { EligibleRow } from '@/lib/nav/eligibleMains'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { IconButton } from '@/components/ui/icon-button'
import { Tooltip } from '@/components/ui/tooltip'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import { SidebarAgentIcon } from './SidebarAgentIcon'
import { attentionCountLabel, useAttentionMotion } from './attentionCue'
import {
  beginPairExtra,
  captureUnconfirmedTarget,
  currentNewChatInput,
  pendingIdStillSelected,
  type CapturedPending,
} from './pairExtra'
import { workspaceMains, type RosterState } from './workspaceRoster'
import './attention.css'
import './agent-row.css'

function refreshWorkspaceRoster() {
  void queryClient.invalidateQueries({ queryKey: ['agents'] })
  void queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list({ status: 'active' }) })
}

function retryAttention() {
  void queryClient.invalidateQueries({ queryKey: ['sessions'] })
  void queryClient.invalidateQueries({ queryKey: ['agents'] })
}

export function WorkspaceAgentList({
  projects,
  expandedIds,
  activeId,
  agents,
  rosterState,
  sessions,
  onToggle,
  onOpen,
  onOverlayClose,
}: {
  projects: Workspace[]
  expandedIds: Set<string>
  activeId: string | null
  agents: Agent[]
  rosterState: RosterState
  sessions: Session[]
  onToggle: (workspaceId: string) => void
  onOpen: (workspace: Workspace) => void
  onOverlayClose: () => void
}) {
  const cacheRef = useRef<Map<string, EligibleRow[]>>(new Map())
  const derived = projects.map((project) => {
    const mains = workspaceMains(
      project,
      agents,
      sessions,
      rosterState,
      cacheRef.current.get(project.id) ?? [],
    )
    if (mains.result.status === 'ready') cacheRef.current.set(project.id, mains.result.rows)
    return { project, mains }
  })
  const staleRoster = derived.some((item) => item.mains.result.status === 'stale')
  const missingMain = derived.some((item) => item.mains.result.reason === 'main-id-missing')
  const attentionUnknown = !missingMain && derived.some((item) => item.mains.unknown)
  const motion = useAttentionMotion()
  const selectSession = useSelectSession({ agents, workspaces: projects, onClose: onOverlayClose })

  return (
    <>
      {staleRoster && (
        <ErrorState
          message="Could not refresh team — showing cached, out-of-date agents"
          onRetry={refreshWorkspaceRoster}
          className="px-[var(--space-3)] py-[var(--space-2)]"
        />
      )}
      {missingMain && (
        <UnavailableNote label="Main chat unavailable" onRetry={retryAttention} />
      )}
      {attentionUnknown && (
        <UnavailableNote label="Attention unavailable" onRetry={retryAttention} />
      )}
      {derived.map(({ project, mains }) => (
        <WorkspaceBlock
          key={project.id}
          project={project}
          isActive={activeId === project.id}
          isExpanded={expandedIds.has(project.id)}
          rows={mains.result.rows}
          signals={mains.signals}
          onCount={mains.onCount}
          countKnown={!mains.unknown && mains.result.status === 'ready'}
          rosterFailed={mains.result.reason === 'roster-failed'}
          motion={motion}
          agents={agents}
          sessions={sessions}
          selectSession={selectSession}
          onToggle={() => {
            refreshWorkspaceRoster()
            onToggle(project.id)
          }}
          onOpen={() => {
            refreshWorkspaceRoster()
            onOpen(project)
          }}
          onOverlayClose={onOverlayClose}
        />
      ))}
    </>
  )
}

function UnavailableNote({ label, onRetry }: { label: string; onRetry: () => void }) {
  return (
    <div className="flex items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-1)]">
      <span role="status" aria-label={label} className="flex-1 text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
        {label}
      </span>
      <Button
        variant="ghost"
        onClick={onRetry}
        className="h-auto shrink-0 px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)]"
      >
        Retry
      </Button>
    </div>
  )
}

function WorkspaceBlock({
  project,
  isActive,
  isExpanded,
  rows,
  signals,
  onCount,
  countKnown,
  rosterFailed,
  motion,
  agents,
  sessions,
  selectSession,
  onToggle,
  onOpen,
  onOverlayClose,
}: {
  project: Workspace
  isActive: boolean
  isExpanded: boolean
  rows: EligibleRow[]
  signals: Record<string, 'on' | 'off' | 'unknown'>
  onCount: number
  countKnown: boolean
  rosterFailed: boolean
  motion: 'loop' | 'static'
  agents: Agent[]
  sessions: Session[]
  selectSession: (session: Session) => void
  onToggle: () => void
  onOpen: () => void
  onOverlayClose: () => void
}) {
  const showCount = !isExpanded && countKnown && onCount > 0
  const countLabel = attentionCountLabel(onCount)
  return (
    <div>
      <div
        className={cn(
          'flex items-center gap-[var(--space-2)] w-full px-[var(--space-3)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] transition-colors text-left',
          isActive
            ? 'text-[var(--color-accent)] font-medium'
            : 'text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
        )}
      >
        <Button
          variant="ghost"
          onClick={onOpen}
          aria-current={isActive ? 'page' : undefined}
          className={cn(
            'h-auto flex-1 min-w-0 justify-start gap-[var(--space-2)] p-0 text-left font-[var(--font-weight-regular)] hover:bg-transparent',
            isActive
              ? 'text-[var(--color-accent)] font-medium hover:text-[var(--color-accent)]'
              : 'text-[var(--color-secondary)] hover:text-[var(--color-secondary)]',
          )}
        >
          <Buildings
            size={14}
            weight={isActive ? 'fill' : 'regular'}
            aria-hidden="true"
            className={cn('flex-shrink-0', isActive ? 'text-[var(--color-accent)]' : 'text-[var(--color-muted)]')}
          />
          <span className="flex-1 truncate">{project.name}</span>
        </Button>
        <IconButton
          onClick={(event) => {
            event.stopPropagation()
            onToggle()
          }}
          aria-expanded={isExpanded}
          aria-label={isExpanded ? `Hide ${project.name} agents` : `Show ${project.name} agents`}
          className="h-auto w-auto shrink-0 rounded p-[var(--space-1)] -m-[var(--space-1)] text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
        >
          {isExpanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
        </IconButton>
        {showCount && (
          <span
            role="status"
            aria-label={countLabel}
            data-cue-px="8"
            data-attention-motion={motion}
            className="inline-flex max-w-[40%] items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
          >
            <span
              aria-hidden="true"
              className={cn(
                'inline-block h-[var(--space-2)] w-[var(--space-2)] shrink-0 rounded-full bg-[var(--color-warning)]',
                motion === 'loop' && 'sidebar-attention-dot',
              )}
            />
            <span className="truncate">{countLabel}</span>
          </span>
        )}
      </div>
      <div
        role="group"
        aria-label={project.name}
        className={isExpanded ? 'mt-[var(--space-1)] mr-[var(--space-2)] mb-[var(--space-2)] ml-[var(--space-4)] pl-[var(--space-2-5)] border-l border-[var(--color-border)]' : undefined}
      >
        {isExpanded && rosterFailed && (
          <div className="flex items-center gap-[var(--space-2)] pr-[var(--space-3)] py-[var(--space-1)]">
            <span className="flex-1 text-[length:var(--type-caption-size)] text-[var(--color-error)]">Could not load team</span>
            <Button
              variant="ghost"
              onClick={() => void queryClient.invalidateQueries({ queryKey: ['agents'] })}
              className="h-auto shrink-0 px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)]"
            >
              Retry
            </Button>
          </div>
        )}
        {isExpanded && rows.map((row) => (
          <AgentMainRow
            key={row.agentId}
            row={row}
            agent={agents.find((candidate) => candidate.id === row.agentId)}
            workspace={project}
            showHalo={signals[row.mainSessionId] === 'on'}
            motion={motion}
            sessions={sessions}
            selectSession={selectSession}
            onOverlayClose={onOverlayClose}
          />
        ))}
      </div>
    </div>
  )
}

function AgentMainRow({
  row,
  agent,
  workspace,
  showHalo,
  motion,
  sessions,
  selectSession,
  onOverlayClose,
}: {
  row: EligibleRow
  agent: Agent | undefined
  workspace: Workspace
  showHalo: boolean
  motion: 'loop' | 'static'
  sessions: Session[]
  selectSession: (session: Session) => void
  onOverlayClose: () => void
}) {
  const navigate = useNavigate()
  const [promptOpen, setPromptOpen] = useState(false)
  const [abandonTarget, setAbandonTarget] = useState<CapturedPending | null>(null)
  const [unresolved, setUnresolved] = useState(false)
  const activeWorkspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  const ownsSelectedChat = useSessionStore((state) => state.activeAgentId === row.agentId)
  const selected = activeWorkspaceId === workspace.id && ownsSelectedChat
  const pastLabel = `Past sessions with ${row.name}`
  const extraLabel = `New chat with ${row.name}`

  const openMain = () => {
    const session = sessions.find((candidate) => candidate.id === row.mainSessionId)
    if (!session) {
      setUnresolved(true)
      return
    }
    setUnresolved(false)
    selectSession(session)
  }

  const startExtra = () => {
    const decision = decideNewChat(currentNewChatInput(row.mainSessionId))
    if (decision.action === 'prompt') {
      setAbandonTarget(captureUnconfirmedTarget())
      setPromptOpen(true)
      return
    }
    if (decision.action === 'start-extra') {
      beginPairExtra(workspace.id, row.agentId, navigate, onOverlayClose)
    }
  }

  const confirmExtra = () => {
    const target = abandonTarget
    const decision = decideNewChat({ ...currentNewChatInput(row.mainSessionId), choice: 'confirm' })
    setAbandonTarget(null)
    setPromptOpen(false)
    // Abandon only the id this dialog opened against. A newer message that
    // reused the pending slot is a different delivery.
    if (
      target
      && decision.action === 'confirm'
      && decision.abandonedClientMessageId === target.clientMessageId
      && pendingIdStillSelected(target)
    ) {
      beginPairExtra(workspace.id, row.agentId, navigate, onOverlayClose, 'confirm', target)
    }
  }

  const declineExtra = () => {
    decideNewChat({ ...currentNewChatInput(row.mainSessionId), choice: 'decline' })
    setAbandonTarget(null)
    setPromptOpen(false)
  }

  return (
    <div role="group" aria-label={row.name} data-selected={selected}>
      <div
        data-selected={selected}
        className="sidebar-agent-row flex items-center rounded-[var(--radius-medium)] hover:bg-[var(--color-surface-2)]"
      >
        <Button
          variant="ghost"
          onClick={openMain}
          aria-current={selected ? 'true' : undefined}
          className={cn(
            'h-auto min-w-0 flex-1 justify-start gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] hover:bg-transparent hover:text-[var(--color-secondary)] pointer-coarse:min-h-[var(--target-touch-minimum)]',
            selected ? 'font-semibold' : 'font-[var(--font-weight-regular)]',
          )}
        >
          <SidebarAgentIcon agent={agent} name={row.name} halo={showHalo} motion={motion} />
          <span className="truncate">{row.name}</span>
        </Button>
        <div className="sidebar-agent-actions flex shrink-0 pr-[var(--space-2)]">
          <Tooltip content={pastLabel} interactive>
            <IconButton
              size="sm"
              aria-label={pastLabel}
              title={pastLabel}
              onClick={() => useUiStore.getState().openSearchModal(workspace.id, row.agentId)}
              className="h-[var(--icon-size-feature)] w-[var(--icon-size-feature)] shrink-0 font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
            >
              <ClockCounterClockwise size={14} weight="regular" aria-hidden="true" />
            </IconButton>
          </Tooltip>
          <Tooltip content={extraLabel} interactive>
            <IconButton
              size="sm"
              aria-label={extraLabel}
              title={extraLabel}
              onClick={startExtra}
              className="h-[var(--icon-size-feature)] w-[var(--icon-size-feature)] shrink-0 font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
            >
              <Plus size={14} weight="regular" aria-hidden="true" />
            </IconButton>
          </Tooltip>
        </div>
      </div>
      {unresolved && (
        <p className="py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
          Main chat unavailable
        </p>
      )}
      <ConfirmDialog
        open={promptOpen}
        onOpenChange={(open) => { if (!open) declineExtra() }}
        title="Start a new chat?"
        description="Delivery not confirmed. Copy your message before starting a new chat."
        cancelLabel="Keep this chat"
        confirmLabel="Start a new chat"
        emphasis="cancel"
        onConfirm={confirmExtra}
      />
    </div>
  )
}
