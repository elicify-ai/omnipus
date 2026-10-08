import { useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Buildings, CaretDown, CaretRight, Plus } from '@phosphor-icons/react'
import type { Agent, Session, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { decideNewChat } from '@/lib/nav/extraChatGuard'
import type { EligibleRow } from '@/lib/nav/eligibleMains'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { IconButton } from '@/components/ui/icon-button'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import { SidebarAgentIcon } from './SidebarAgentIcon'
import { attentionCountLabel, useAttentionMotion } from './attentionCue'
import { beginPairExtra, currentNewChatInput, unconfirmedSendStillSelected } from './pairExtra'
import { workspaceMains, type RosterState } from './workspaceRoster'
import './attention.css'

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
  const missingMain = derived.some((item) => item.mains.result.reason === 'main-id-missing')
  const attentionUnknown = !missingMain && derived.some((item) => item.mains.unknown)
  const motion = useAttentionMotion()
  const selectSession = useSelectSession({ agents, workspaces: projects, onClose: onOverlayClose })

  return (
    <>
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
          aria-label={isExpanded ? `Collapse ${project.name} sessions` : `Expand ${project.name} sessions`}
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
      <div role="group" aria-label={project.name} className="pb-[var(--space-1)] ml-[var(--space-3)] border-l border-[var(--color-border)]">
        {isExpanded && rosterFailed && (
          <div className="flex items-center gap-[var(--space-2)] pl-[var(--space-2-5)] pr-[var(--space-3)] py-[var(--space-1)]">
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
        {rows.map((row) => (
          <AgentMainRow
            key={row.agentId}
            row={row}
            workspace={project}
            showHalo={isExpanded && signals[row.mainSessionId] === 'on'}
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
  workspace,
  showHalo,
  motion,
  sessions,
  selectSession,
  onOverlayClose,
}: {
  row: EligibleRow
  workspace: Workspace
  showHalo: boolean
  motion: 'loop' | 'static'
  sessions: Session[]
  selectSession: (session: Session) => void
  onOverlayClose: () => void
}) {
  const navigate = useNavigate()
  const [promptOpen, setPromptOpen] = useState(false)
  const [unresolved, setUnresolved] = useState(false)

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
      setPromptOpen(true)
      return
    }
    if (decision.action === 'start-extra') {
      beginPairExtra(workspace.id, row.agentId, navigate, onOverlayClose)
    }
  }

  const confirmExtra = () => {
    const decision = decideNewChat({ ...currentNewChatInput(row.mainSessionId), choice: 'confirm' })
    setPromptOpen(false)
    if (decision.action === 'confirm' && unconfirmedSendStillSelected()) {
      beginPairExtra(workspace.id, row.agentId, navigate, onOverlayClose)
    }
  }

  const declineExtra = () => {
    decideNewChat({ ...currentNewChatInput(row.mainSessionId), choice: 'decline' })
    setPromptOpen(false)
  }

  return (
    <div role="group" aria-label={row.name} className="py-[var(--space-1)] pl-[var(--space-2-5)] pr-[var(--space-3)]">
      <div className="flex items-center gap-[var(--space-2)]">
        <SidebarAgentIcon name={row.name} halo={showHalo} motion={motion} />
        <Button
          variant="ghost"
          onClick={openMain}
          className="h-auto min-w-0 flex-1 justify-start p-0 font-[var(--font-weight-regular)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] hover:bg-transparent hover:text-[var(--color-accent)]"
        >
          <span className="truncate">{row.name}</span>
        </Button>
      </div>
      <div className="mt-[var(--space-1)] flex flex-col gap-[var(--space-1)]">
        <Button
          variant="ghost"
          onClick={() => useUiStore.getState().openSearchModal(workspace.id, row.agentId)}
          className="h-auto w-full justify-start gap-[var(--space-1)] px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-accent)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
        >
          Past sessions
        </Button>
        <Button
          variant="ghost"
          onClick={startExtra}
          className="h-auto w-full justify-start gap-[var(--space-1)] px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-accent)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
        >
          <Plus size={12} aria-hidden="true" /> New chat
        </Button>
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
