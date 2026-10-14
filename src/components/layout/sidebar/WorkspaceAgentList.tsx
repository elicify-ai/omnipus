import { useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Buildings, CaretDown, CaretRight, Plus } from '@phosphor-icons/react'
import type { Agent, Session, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import type { EligibleRow } from '@/lib/nav/eligibleMains'
import { useSelectSession } from '@/components/chat/useSelectSession'
import { useOpenOmnipus } from '@/hooks/useOpenOmnipus'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { IconButton } from '@/components/ui/icon-button'
import { useSessionStore } from '@/store/session'
import { dismissNewChatPrompt } from '@/store/session/newChatFlow'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { cn } from '@/lib/utils'
import { AgentRowMenu } from './AgentRowMenu'
import { invalidateWorkspaceRoster, setHiddenFromSidebar, surfaceActionError } from './agentRowActions'
import { SidebarAgentIcon } from './SidebarAgentIcon'
import { WorkspaceGroupChats } from './WorkspaceGroupChats'
import { ROOMS_ENABLED } from '@/lib/featureSwitches'
import { attentionCountLabel, useAttentionMotion } from './attentionCue'
import { beginPairExtra } from './pairExtra'
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
          rows={mains.visible}
          hiddenRows={mains.hidden}
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
  hiddenRows,
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
  hiddenRows: EligibleRow[]
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
  const showHidden = () => {
    void setHiddenFromSidebar(project, hiddenRows.map((row) => row.agentId), false)
      .then(invalidateWorkspaceRoster)
      .catch((err: unknown) => surfaceActionError(`Could not show hidden ${project.name} agents`, err))
  }
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
        {isExpanded && !rosterFailed && (
          <AddAgentRow workspace={project} onOverlayClose={onOverlayClose} />
        )}
        {isExpanded && hiddenRows.length > 0 && (
          <div className="flex items-center gap-[var(--space-2)] px-[var(--space-2)] py-[var(--space-1)]">
            <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
              {hiddenRows.length} hidden
            </span>
            <Button
              variant="ghost"
              onClick={showHidden}
              className="h-auto shrink-0 px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
            >
              Show
            </Button>
          </div>
        )}
        {/* FR-174: Group chats under the agent rows — hidden until WU-15 (stage 3), #1314. */}
        {isExpanded && ROOMS_ENABLED && <WorkspaceGroupChats workspace={project} onOverlayClose={onOverlayClose} />}
      </div>
    </div>
  )
}

/**
 * omnipus-system-agent FR-045, BDD-4.1 (F:D17): the sidebar's "Add agent"
 * opens Omnipus's chat with W1 started for THIS workspace; the active
 * workspace does not change. There is no manual create form (F:D18).
 */
function AddAgentRow({ workspace, onOverlayClose }: { workspace: Workspace; onOverlayClose: () => void }) {
  const openOmnipus = useOpenOmnipus()
  return (
    <Button
      variant="ghost"
      data-testid={`sidebar-add-agent-${workspace.id}`}
      aria-label={`Add agent to ${workspace.name}`}
      onClick={() => {
        const outcome = openOmnipus({ workflow: 'W1', workspaceId: workspace.id, workspaceName: workspace.name })
        if (outcome !== 'attach-failed') onOverlayClose()
      }}
      className="h-auto w-full justify-start gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] pointer-coarse:min-h-[var(--target-touch-minimum)]"
    >
      <Plus size={12} aria-hidden="true" />
      Add agent
    </Button>
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
  const [unresolved, setUnresolved] = useState(false)
  const activeWorkspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  const ownsSelectedChat = useSessionStore((state) => state.activeAgentId === row.agentId)
  // M7: the "Start a new chat?" question lives in the store and newChatFlow
  // is its only decision owner. This row renders the dialog when the flow
  // asked its question for THIS agent; confirm executes exactly what was
  // asked (the prompt's own clientMessageId), and a state change while the
  // dialog was open is answered by the flow visibly, never silently.
  // D7: the row's New chat BUTTON is gone; the dialog and the capability stay.
  const prompt = useSessionStore((state) => state.newChatPrompt)
  const promptOpen = prompt?.action === 'prompt' && prompt.agentId === row.agentId
  const selected = activeWorkspaceId === workspace.id && ownsSelectedChat

  const openMain = () => {
    const session = sessions.find((candidate) => candidate.id === row.mainSessionId)
    if (!session) {
      setUnresolved(true)
      return
    }
    setUnresolved(false)
    selectSession(session)
  }

  const confirmExtra = () => {
    const asked = useSessionStore.getState().newChatPrompt
    beginPairExtra(workspace.id, row.agentId, navigate, onOverlayClose, 'confirm', asked?.clientMessageId)
  }

  const declineExtra = () => {
    dismissNewChatPrompt()
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
          <AgentRowMenu workspace={workspace} agent={agent} agentId={row.agentId} agentName={row.name} />
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
