import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import {
  Circle,
  ListChecks,
  Clock,
  Heartbeat,
  Trash,
  MagnifyingGlass,
  CaretDown,
  CaretRight,
  Folder,
} from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Badge } from '@/components/ui/badge'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import {
  fetchAgents,
  fetchSessions,
  fetchWorkspaces,
  renameSession,
  deleteSession,
  isApiError,
  workspacesQueryKeys,
} from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'
import { cn } from '@/lib/utils'
import { formatTokens } from '@/lib/formatTokens'

function sessionButtonClass(isActive: boolean): string {
  return isActive
    ? 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
    : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]'
}

const UNTITLED_SESSION = 'Untitled Session'
const NO_WORKSPACE_KEY = '__no_workspace__'
const NO_AGENT_KEY = '__no_agent__'

// ── Agent participation badges ────────────────────────────────────────────────

interface AgentBadgesProps {
  agentIds: string[]
  agents: Agent[]
  // Optionally drop one id (the row's owning agent sub-group) so single-agent
  // rows don't repeat the avatar already shown in their group header — only
  // co-participants remain.
  hideId?: string
}

function AgentBadges({ agentIds, agents, hideId }: AgentBadgesProps) {
  const visible = hideId ? agentIds.filter((id) => id !== hideId) : agentIds
  if (visible.length === 0) return null
  return (
    <div className="flex -space-x-1 shrink-0">
      {visible.map((id) => {
        const agent = agents.find((a) => a.id === id)
        return (
          <div
            key={id}
            className="w-4 h-4 rounded-full border border-[var(--color-primary)] flex items-center justify-center text-[7px]"
            style={{ backgroundColor: agent?.color ?? 'var(--color-surface-3)' }}
            title={agent?.name ?? '[removed agent]'}
          >
            {agent?.icon ? (
              <IconRenderer icon={agent.icon} size={8} />
            ) : (
              <span className="text-[var(--color-secondary)] font-bold">?</span>
            )}
          </div>
        )
      })}
    </div>
  )
}

// ── Inline rename + delete session item ──────────────────────────────────────

interface SessionItemProps {
  session: Session
  agents: Agent[]
  isActive: boolean
  isStreaming: boolean
  onSelect: () => void
  onDeleted: (sessionId: string) => void
  // The owning agent sub-group's agent id, hidden from this row's participant
  // badges to avoid repeating the avatar shown in the group header.
  hideAgentId?: string
  // When true (heartbeat session with protected=true), the delete button is
  // hidden so users cannot attempt deletion that would return 409 (FR-021/028).
  deleteDisabled?: boolean
}

const SESSION_TYPE_LABELS: Record<Session['type'], string> = {
  task: 'Task',
  scheduled: 'Scheduled',
  channel: 'Channel',
  heartbeat: 'Heartbeat',
  chat: 'Chat',
}

function taskStatusStyle(status: string | undefined): { color: string; label: string } {
  switch (status) {
    case 'archived':
      return { color: 'text-[var(--color-success)]', label: 'completed' }
    case 'interrupted':
      return { color: 'text-[var(--color-error)]', label: 'failed' }
    default:
      return { color: 'text-[var(--color-warning)]', label: 'running' }
  }
}

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  if (isNaN(date.getTime())) return ''
  const diffMs = Date.now() - date.getTime()
  const diffMins = Math.floor(diffMs / 60_000)
  if (diffMins < 1) return 'just now'
  if (diffMins < 60) return `${diffMins}m ago`
  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours}h ago`
  const diffDays = Math.floor(diffHours / 24)
  if (diffDays < 7) return `${diffDays}d ago`
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function formatAbsoluteTime(dateStr: string): string {
  const date = new Date(dateStr)
  if (isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// ── Rich hover details card (replaces the plain native title= tooltip) ────────

function HoverRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="shrink-0 text-[var(--color-muted)]">{label}</dt>
      <dd className="min-w-0 truncate text-right text-[var(--color-secondary)]">{children}</dd>
    </div>
  )
}

// Portal'd to document.body so the fixed card escapes the Sheet's transform +
// overflow context (a position:fixed child of a transformed/animated ancestor
// would be clipped or mis-anchored). pointer-events-none so it never steals the
// hover from the row beneath it. Opens to the LEFT of the right-anchored panel,
// or below the row when the panel is ~full-width (narrow / mobile).
function SessionHoverCard({
  session,
  agents,
  anchor,
}: {
  session: Session
  agents: Agent[]
  anchor: DOMRect
}) {
  const GAP = 8
  const CARD_W = 256
  const EST_H = 210

  const participantIds =
    session.agent_ids && session.agent_ids.length > 0
      ? session.agent_ids
      : session.agent_id
        ? [session.agent_id]
        : []
  const agentNames =
    participantIds.map((id) => agents.find((a) => a.id === id)?.name ?? '[removed]').join(', ') ||
    '—'

  const typeLabel = SESSION_TYPE_LABELS[session.type]
  // Status semantics (running/completed/failed) only apply to task runs.
  const statusLabel =
    session.type === 'task' && session.status ? taskStatusStyle(session.status).label : ''
  const channel = session.channel && session.channel !== 'webchat' ? session.channel : null

  const openLeft = anchor.left > CARD_W + GAP * 2
  const top = openLeft
    ? Math.min(Math.max(anchor.top, GAP), window.innerHeight - EST_H - GAP)
    : Math.min(anchor.bottom + GAP, window.innerHeight - EST_H - GAP)
  const style: React.CSSProperties = openLeft
    ? { top, right: window.innerWidth - (anchor.left - GAP), width: CARD_W }
    : { top, left: Math.max(GAP, anchor.right - CARD_W), width: CARD_W }

  return createPortal(
    <div
      role="tooltip"
      className="pointer-events-none fixed z-[60] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 shadow-xl"
      style={style}
    >
      <div className="mb-2 line-clamp-3 break-words text-xs font-medium leading-snug text-[var(--color-secondary)]">
        {session.title || UNTITLED_SESSION}
      </div>
      <dl className="space-y-1 text-[11px]">
        <HoverRow label="Type">
          {typeLabel}
          {statusLabel ? ` · ${statusLabel}` : ''}
        </HoverRow>
        <HoverRow label={participantIds.length > 1 ? 'Agents' : 'Agent'}>{agentNames}</HoverRow>
        <HoverRow label="Messages">{session.message_count}</HoverRow>
        {session.total_tokens != null && session.total_tokens > 0 && (
          <HoverRow label="Tokens">{formatTokens(session.total_tokens)}</HoverRow>
        )}
        {channel && <HoverRow label="Channel">{channel}</HoverRow>}
        <HoverRow label="Started">{formatAbsoluteTime(session.created_at)}</HoverRow>
        <HoverRow label="Last active">{formatRelativeTime(session.updated_at)}</HoverRow>
      </dl>
      <div className="mt-2 border-t border-[var(--color-border)] pt-1.5 text-[10px] text-[var(--color-muted)]">
        Click to open · double-click the title to rename
      </div>
    </div>,
    document.body,
  )
}

function SessionItem({
  session,
  agents,
  isActive,
  isStreaming,
  onSelect,
  onDeleted,
  hideAgentId,
  deleteDisabled = false,
}: SessionItemProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [isEditing, setIsEditing] = useState(false)
  const [editValue, setEditValue] = useState(session.title || UNTITLED_SESSION)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // Rich hover/focus details card. Anchored to the title button's live rect so
  // the portal'd card can position itself relative to the row.
  const titleBtnRef = useRef<HTMLButtonElement>(null)
  const [hoverAnchor, setHoverAnchor] = useState<DOMRect | null>(null)
  const openCard = () => setHoverAnchor(titleBtnRef.current?.getBoundingClientRect() ?? null)
  const closeCard = () => setHoverAnchor(null)

  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus()
      inputRef.current.select()
    }
  }, [isEditing])

  // Keep edit value in sync when session title changes externally
  useEffect(() => {
    if (!isEditing) {
      setEditValue(session.title || UNTITLED_SESSION)
    }
  }, [session.title, isEditing])

  const { mutate: doRename, isPending: isRenaming } = useMutation({
    mutationFn: (title: string) => renameSession(session.id, title),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
    },
    onError: (err: unknown) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Rename failed'
      addToast({ message: `Could not rename session: ${msg}`, variant: 'error' })
      setEditValue(session.title || UNTITLED_SESSION)
    },
    onSettled: () => setIsEditing(false),
  })

  const { mutate: doDelete, isPending: isDeleting } = useMutation({
    mutationFn: () => deleteSession(session.id),
    onSuccess: () => {
      onDeleted(session.id)
    },
    onError: (err: unknown) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Delete failed'
      addToast({ message: `Could not delete session: ${msg}`, variant: 'error' })
      setConfirmDelete(false)
    },
  })

  function commitRename() {
    const trimmed = editValue.trim()
    if (!trimmed || trimmed === session.title) {
      setIsEditing(false)
      setEditValue(session.title || UNTITLED_SESSION)
      return
    }
    doRename(trimmed)
  }

  function handleTitleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') {
      e.preventDefault()
      commitRename()
    }
    if (e.key === 'Escape') {
      setIsEditing(false)
      setEditValue(session.title || UNTITLED_SESSION)
    }
  }

  // Resolve which agent IDs to show — use agent_ids if present, fall back to [agent_id]
  const participantIds =
    session.agent_ids && session.agent_ids.length > 0
      ? session.agent_ids
      : session.agent_id
        ? [session.agent_id]
        : []

  const isTask = session.type === 'task'
  const isScheduled = session.type === 'scheduled'
  const isHeartbeat = session.type === 'heartbeat'

  if (confirmDelete) {
    return (
      <div className="flex items-center gap-1 px-4 py-2 text-xs">
        <span className="text-[var(--color-secondary)] flex-1 truncate">Delete?</span>
        <button
          type="button"
          disabled={isDeleting}
          onClick={() => doDelete()}
          className="px-1.5 py-0.5 rounded text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-colors disabled:opacity-50"
        >
          {isDeleting ? '...' : 'Yes'}
        </button>
        <button
          type="button"
          onClick={() => setConfirmDelete(false)}
          className="px-1.5 py-0.5 rounded text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] transition-colors"
        >
          No
        </button>
      </div>
    )
  }

  return (
    <div
      className={cn(
        'group/item flex items-center gap-2 px-3 py-2 rounded-sm transition-colors',
        sessionButtonClass(isActive),
      )}
    >
      {/* Agent participation badges (co-participants only when nested under an agent group) */}
      <AgentBadges agentIds={participantIds} agents={agents} hideId={hideAgentId} />

      {/* Title / rename input */}
      <div className="flex-1 min-w-0">
        {isEditing ? (
          <input
            ref={inputRef}
            value={editValue}
            onChange={(e) => setEditValue(e.target.value)}
            onBlur={commitRename}
            onKeyDown={handleTitleKeyDown}
            disabled={isRenaming}
            className="w-full text-xs bg-transparent border-b border-[var(--color-accent)]/50 outline-none text-[var(--color-secondary)] disabled:opacity-50"
          />
        ) : (
          <button
            ref={titleBtnRef}
            type="button"
            onClick={onSelect}
            onDoubleClick={(e) => {
              e.preventDefault()
              setIsEditing(true)
            }}
            onMouseEnter={openCard}
            onMouseLeave={closeCard}
            onFocus={openCard}
            onBlur={closeCard}
            aria-label={`Open session: ${session.title || UNTITLED_SESSION}`}
            className="w-full text-left"
          >
            <div className="flex items-center gap-1.5 min-w-0">
              {isActive && (
                <Circle size={5} weight="fill" className="text-[var(--color-success)] shrink-0" />
              )}
              {isTask && (
                <ListChecks size={10} className="text-[var(--color-accent)] shrink-0" />
              )}
              {isScheduled && (
                <Clock
                  size={10}
                  className="text-[var(--color-muted)] shrink-0"
                  aria-label="Scheduled session"
                />
              )}
              {isHeartbeat && (
                <Heartbeat
                  size={10}
                  weight="fill"
                  className="text-[var(--color-accent)] shrink-0"
                  aria-label="Heartbeat session"
                />
              )}
              <span className="truncate text-xs">{session.title || UNTITLED_SESSION}</span>
              {isStreaming && !isActive && (
                // Background session is generating — pulse dot so the user knows work is in progress.
                <span className="ml-auto shrink-0 w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-pulse" aria-label="Generating" />
              )}
            </div>
          </button>
        )}
        {hoverAnchor && !isEditing && (
          <SessionHoverCard session={session} agents={agents} anchor={hoverAnchor} />
        )}
      </div>

      {/* Right side: task status badge + token chip + relative time + delete */}
      {!isEditing && (
        <div className="flex items-center gap-1.5 shrink-0">
          {isTask && (
            <Badge
              variant="outline"
              className={cn('text-[9px] h-4 px-1', taskStatusStyle(session.status).color)}
            >
              {taskStatusStyle(session.status).label}
            </Badge>
          )}
          {isScheduled && (
            <Badge
              variant="outline"
              className="text-[9px] h-4 px-1 text-[var(--color-muted)]"
            >
              Scheduled
            </Badge>
          )}
          {isHeartbeat && (
            <Badge
              variant="outline"
              className="text-[9px] h-4 px-1"
              style={{ color: 'var(--color-accent)', borderColor: 'var(--color-accent)' }}
            >
              Heartbeat
            </Badge>
          )}
          {session.total_tokens != null && session.total_tokens > 0 && (
            <span
              data-testid="session-token-chip"
              className="text-[9px] font-mono text-[var(--color-muted)] tabular-nums"
              aria-label={`${session.total_tokens} tokens`}
            >
              {formatTokens(session.total_tokens)}
            </span>
          )}
          <span className="text-[10px] text-[var(--color-muted)] tabular-nums">
            {formatRelativeTime(session.updated_at)}
          </span>
          {!deleteDisabled && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                setConfirmDelete(true)
              }}
              className="p-1 rounded opacity-0 group-hover/item:opacity-100 [@media(hover:none)]:opacity-100 text-[var(--color-muted)] hover:text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-all"
              aria-label={`Delete session: ${session.title || UNTITLED_SESSION}`}
              title="Delete session"
            >
              <Trash size={11} />
            </button>
          )}
        </div>
      )}
    </div>
  )
}

// ── Workspace group header ────────────────────────────────────────────────────

interface WorkspaceGroupProps {
  label: string
  count: number
  isCollapsed: boolean
  onToggle: () => void
  children: React.ReactNode
}

function WorkspaceGroup({ label, count, isCollapsed, onToggle, children }: WorkspaceGroupProps) {
  return (
    <div>
      <button
        type="button"
        onClick={onToggle}
        className="w-full flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-[var(--color-muted)] uppercase tracking-wider hover:text-[var(--color-secondary)] transition-colors"
        aria-expanded={!isCollapsed}
        aria-label={`${label} workspace sessions, ${isCollapsed ? 'expand' : 'collapse'}`}
      >
        {isCollapsed ? (
          <CaretRight size={10} className="shrink-0 transition-transform" />
        ) : (
          <CaretDown size={10} className="shrink-0 transition-transform" />
        )}
        <Folder size={14} className="shrink-0" />
        <span className="flex-1 text-left truncate">{label}</span>
        <Badge variant="secondary" className="text-[9px] h-4 px-1.5 rounded-full shrink-0">
          {count}
        </Badge>
      </button>
      {!isCollapsed && (
        <div className="space-y-0.5 px-2 pb-1">
          {children}
        </div>
      )}
    </div>
  )
}

// ── Workspace session grouping helpers ────────────────────────────────────────

interface WorkspaceSessionGroup {
  key: string           // workspace id or NO_WORKSPACE_KEY
  label: string         // workspace name or "No workspace"
  sessions: Session[]
}

/**
 * Build workspace-scoped groups from a flat session list.
 *
 * When `activeWorkspaceId` is set, produces at most two groups:
 *   1. The active workspace's own sessions (sessions where workspace_id matches).
 *   2. A "No workspace" group at the bottom for sessions that have no workspace_id
 *      OR whose workspace_id does not resolve to any known active workspace
 *      (deleted-workspace / orphaned / scheduled sessions).
 *
 * Sessions belonging to OTHER existing workspaces are intentionally excluded
 * so each workspace view is scoped to its own sessions only.
 *
 * When `activeWorkspaceId` is null (no active workspace), falls back to a
 * single "No workspace" group containing all sessions.
 */
function buildWorkspaceGroups(
  sessions: Session[],
  workspaces: Workspace[],
  activeWorkspaceId: string | null,
): WorkspaceSessionGroup[] {
  const workspaceById = new Map(workspaces.map((w) => [w.id, w]))
  const existingWorkspaceIds = new Set(workspaces.map((w) => w.id))

  // No active workspace: put all sessions in the "No workspace" fallback group.
  if (!activeWorkspaceId) {
    if (sessions.length === 0) return []
    return [{ key: NO_WORKSPACE_KEY, label: 'No workspace', sessions }]
  }

  const activeSessions: Session[] = []
  const orphanSessions: Session[] = []

  for (const s of sessions) {
    if (s.workspace_id === activeWorkspaceId) {
      // Belongs to the active workspace.
      activeSessions.push(s)
    } else if (!s.workspace_id || !existingWorkspaceIds.has(s.workspace_id)) {
      // No workspace_id, or workspace_id points to a deleted/unknown workspace.
      orphanSessions.push(s)
    }
    // Sessions belonging to OTHER existing workspaces are intentionally excluded.
  }

  const result: WorkspaceSessionGroup[] = []

  if (activeSessions.length > 0) {
    result.push({
      key: activeWorkspaceId,
      label: workspaceById.get(activeWorkspaceId)?.name ?? activeWorkspaceId,
      sessions: activeSessions,
    })
  }

  if (orphanSessions.length > 0) {
    result.push({
      key: NO_WORKSPACE_KEY,
      label: 'No workspace',
      sessions: orphanSessions,
    })
  }

  return result
}

// ── Agent sub-group (nested INSIDE each workspace group) ──────────────────────

interface AgentSubGroupData {
  agentId: string // first agent of the conversation, or NO_AGENT_KEY
  agent: Agent | undefined
  sessions: Session[]
}

/**
 * Group a workspace's sessions by the FIRST agent of each conversation.
 *
 * The first agent is `agent_ids[0]` — the backend maintains AgentIDs in
 * participation order (appending each new agent), so index 0 is the agent that
 * opened the conversation; it falls back to the legacy single `agent_id`.
 *
 * Group order follows first appearance in the already-desc-by-updated_at list,
 * so the agent with the most-recently-active conversation floats to the top.
 */
function buildAgentSubGroups(sessions: Session[], agents: Agent[]): AgentSubGroupData[] {
  const order: string[] = []
  const byAgent = new Map<string, Session[]>()
  for (const s of sessions) {
    const first = (s.agent_ids && s.agent_ids.length > 0 ? s.agent_ids[0] : s.agent_id) || NO_AGENT_KEY
    let bucket = byAgent.get(first)
    if (!bucket) {
      bucket = []
      byAgent.set(first, bucket)
      order.push(first)
    }
    bucket.push(s)
  }
  return order.map((agentId) => ({
    agentId,
    agent: agents.find((a) => a.id === agentId),
    sessions: byAgent.get(agentId)!,
  }))
}

interface AgentSubGroupProps {
  agent: Agent | undefined
  agentId: string
  count: number
  isCollapsed: boolean
  onToggle: () => void
  children: React.ReactNode
}

function AgentSubGroup({ agent, agentId, count, isCollapsed, onToggle, children }: AgentSubGroupProps) {
  const name = agent?.name ?? (agentId === NO_AGENT_KEY ? 'No agent' : '[removed agent]')
  return (
    <div>
      <button
        type="button"
        onClick={onToggle}
        className="w-full flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
        aria-expanded={!isCollapsed}
        aria-label={`${name} conversations, ${isCollapsed ? 'expand' : 'collapse'}`}
      >
        {isCollapsed ? (
          <CaretRight size={9} className="shrink-0" />
        ) : (
          <CaretDown size={9} className="shrink-0" />
        )}
        <span
          className="w-4 h-4 rounded-full border border-[var(--color-primary)] flex items-center justify-center text-[7px] shrink-0"
          style={{ backgroundColor: agent?.color ?? 'var(--color-surface-3)' }}
        >
          {agent?.icon ? (
            <IconRenderer icon={agent.icon} size={8} />
          ) : (
            <span className="text-[var(--color-secondary)] font-bold">
              {name.charAt(0).toUpperCase()}
            </span>
          )}
        </span>
        <span className="flex-1 text-left truncate">{name}</span>
        <Badge variant="secondary" className="text-[9px] h-4 px-1.5 rounded-full shrink-0">
          {count}
        </Badge>
      </button>
      {!isCollapsed && <div className="space-y-0.5 pl-3">{children}</div>}
    </div>
  )
}

// ── Main panel ────────────────────────────────────────────────────────────────

export function SessionPanel() {
  const { sessionPanelOpen, closeSessionPanel } = useUiStore()
  const { activeSessionId, setActiveSession, attachToSession, setActiveAgentType } = useSessionStore()
  const sessionsById = useChatStore((s) => s.sessionsById)
  const seedSessionTokens = useChatStore((s) => s.seedSessionTokens)
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const [searchValue, setSearchValue] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Collapsed workspace group keys; default: all expanded (empty set)
  const [collapsedWorkspaces, setCollapsedWorkspaces] = useState<Set<string>>(() => new Set())
  // Collapsed agent sub-group keys (`${workspaceKey}::${agentId}`); default expanded.
  const [collapsedAgentGroups, setCollapsedAgentGroups] = useState<Set<string>>(() => new Set())

  const handleSearchChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value
    setSearchValue(val)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => setDebouncedSearch(val), 300)
  }, [])

  useEffect(() => {
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [])

  const { data: agents = [], isError: agentsError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: sessionPanelOpen,
  })

  const { data: sessions = [], isError: sessionsError } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    enabled: sessionPanelOpen,
  })

  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const setActiveWorkspaceId = useWorkspacesStore((s) => s.setActiveWorkspaceId)

  // Fetch all workspaces (active) to build the name map and resolve orphaned sessions.
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    enabled: sessionPanelOpen,
    staleTime: 30_000,
  })

  const handleSelectSession = (session: Session) => {
    // Always trigger the WS attach_session flow so the replay pipeline
    // emits tool_call_start / tool_call_result / subagent_start / subagent_end
    // frames. Without this, chat sessions render only the filtered REST payload
    // (user/assistant text) and tool-call history is silently dropped
    // (ChatScreen.tsx filters non-text entries from historyData).
    //
    // Do NOT also call setActiveSession — it calls resetChatSession() a second
    // time, wiping the state attachToSession just initialized (including
    // isReplaying=true and attachedSessionType).
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
      closeSessionPanel()
      void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: sessionWsId } })
      return
    }

    // Same workspace, no workspace, or deleted-workspace session — attach in place.
    attachToSession(session.id, session.type, session.title, agentId)
    // Seed the token counter from the persisted total so historic sessions
    // show their total immediately rather than starting at 0.
    if (session.total_tokens && session.total_tokens > 0) {
      seedSessionTokens(session.total_tokens)
    }
    if (session.type !== 'task') {
      // Track the active agent type for composer behavior (chat-only concern).
      // Set directly via the store — no reset, no double-attach.
      const agent = agents.find((a) => a.id === agentId)
      if (agent?.type) {
        // Use the dedicated store action instead of direct setState bypass.
        setActiveAgentType(agent.type)
      }
    }
    closeSessionPanel()
  }

  const handleSessionDeleted = (deletedId: string) => {
    queryClient.invalidateQueries({ queryKey: ['sessions'] })
    if (activeSessionId === deletedId) {
      setActiveSession(null, null, null)
    }
  }

  // Sort all sessions descending. Scoping to the active workspace + "No workspace"
  // orphans is handled inside buildWorkspaceGroups — no pre-filtering here.
  const sortedSessions = [...sessions].sort(
    (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
  )

  // Apply search filter: match title, or any participating agent's name.
  const searchLower = debouncedSearch.toLowerCase().trim()

  const searchFilteredSessions = searchLower
    ? sortedSessions.filter((session) => {
        const titleMatch = (session.title ?? '').toLowerCase().includes(searchLower)
        if (titleMatch) return true
        // Also match any participating agent name
        const participantIds =
          session.agent_ids && session.agent_ids.length > 0
            ? session.agent_ids
            : session.agent_id ? [session.agent_id] : []
        return participantIds.some((id) =>
          agents.find((a) => a.id === id)?.name.toLowerCase().includes(searchLower),
        )
      })
    : sortedSessions

  // Partition: heartbeat sessions are pinned above all groups; they are excluded
  // from the workspace-scoped groups to avoid duplication (FR-021, FR-028).
  const { heartbeatSessions, nonHeartbeatSessions } = useMemo(() => {
    const hb: Session[] = []
    const rest: Session[] = []
    for (const s of searchFilteredSessions) {
      if (s.type === 'heartbeat') {
        hb.push(s)
      } else {
        rest.push(s)
      }
    }
    return { heartbeatSessions: hb, nonHeartbeatSessions: rest }
  }, [searchFilteredSessions])

  // Build workspace-scoped groups. Only the active workspace's sessions and a
  // "No workspace" group for orphaned sessions are shown. Other workspaces'
  // sessions are excluded (buildWorkspaceGroups enforces this).
  // Heartbeat sessions are already extracted above so they don't appear twice.
  const workspaceGroups = useMemo(
    () => buildWorkspaceGroups(nonHeartbeatSessions, workspaces, activeWorkspaceId),
    [nonHeartbeatSessions, workspaces, activeWorkspaceId],
  )

  // Flat list of visible sessions across all groups (for empty-state check).
  // Include heartbeat sessions in the total count.
  const filteredSessions = useMemo(
    () => [...heartbeatSessions, ...workspaceGroups.flatMap((g) => g.sessions)],
    [heartbeatSessions, workspaceGroups],
  )

  // Always show workspace group headers as long as there is at least one group.
  const showGroups = workspaceGroups.length > 0

  const toggleWorkspace = (key: string) => {
    setCollapsedWorkspaces((prev) => {
      const next = new Set(prev)
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
      }
      return next
    })
  }

  const toggleAgentGroup = (key: string) => {
    setCollapsedAgentGroups((prev) => {
      const next = new Set(prev)
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
      }
      return next
    })
  }

  return (
    <Sheet open={sessionPanelOpen} onOpenChange={(open) => !open && closeSessionPanel()}>
      <SheetContent side="right" className="w-[90vw] sm:w-[22.5rem] p-0 flex flex-col" overlay={false}>
        <SheetHeader className="px-4 pt-5 pb-3 border-b border-[var(--color-border)]">
          <SheetTitle>Sessions</SheetTitle>
        </SheetHeader>

        {/* Search input */}
        <div className="px-4 py-2 border-b border-[var(--color-border)]">
          <div className="flex items-center gap-2 rounded-lg bg-[var(--color-surface-2)] border border-[var(--color-border)] px-3 py-1.5">
            <MagnifyingGlass size={13} className="text-[var(--color-muted)] shrink-0" />
            <input
              type="text"
              value={searchValue}
              onChange={handleSearchChange}
              placeholder="Search sessions..."
              aria-label="Search sessions"
              className="flex-1 bg-transparent text-xs text-[var(--color-secondary)] placeholder:text-[var(--color-muted)] outline-none"
            />
          </div>
        </div>

        <div className="flex-1 overflow-y-auto">
          {(agentsError || sessionsError) && (
            <div className="px-4 py-3 text-xs text-[var(--color-error)]">
              Could not load sessions.
            </div>
          )}

          {/* Pinned heartbeat sessions — always rendered above workspace groups (FR-021/028) */}
          {heartbeatSessions.length > 0 && (
            <div
              className="border-b border-[var(--color-border)] pb-1 pt-2"
              aria-label="Pinned heartbeat sessions"
            >
              <div className="px-3 pb-1 flex items-center gap-1.5">
                <Heartbeat size={11} weight="fill" className="text-[var(--color-accent)]" />
                <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
                  Heartbeat
                </span>
              </div>
              <div className="space-y-0.5 px-2">
                {heartbeatSessions.map((session) => (
                  <SessionItem
                    key={session.id}
                    session={session}
                    agents={agents}
                    isActive={session.id === activeSessionId}
                    isStreaming={sessionsById[session.id]?.isStreaming ?? false}
                    onSelect={() => handleSelectSession(session)}
                    onDeleted={handleSessionDeleted}
                    deleteDisabled={session.protected === true}
                  />
                ))}
              </div>
            </div>
          )}

          {filteredSessions.length === 0 ? (
            <div className="px-4 py-6 text-xs text-[var(--color-muted)] text-center">
              {searchLower
                ? 'No results.'
                : activeWorkspaceId
                  ? 'No conversations in this workspace yet. Start a chat to begin.'
                  : 'No sessions yet. Start a conversation to begin.'}
            </div>
          ) : showGroups ? (
            // Grouped view: collapsible workspace-group headers + sessions within each group.
            // Always rendered when there are sessions — including the single-workspace case
            // so "My Workspace" is always visible as a collapsible header.
            <div className="py-1">
              {workspaceGroups.map((group) => (
                <WorkspaceGroup
                  key={group.key}
                  label={group.label}
                  count={group.sessions.length}
                  isCollapsed={collapsedWorkspaces.has(group.key)}
                  onToggle={() => toggleWorkspace(group.key)}
                >
                  {buildAgentSubGroups(group.sessions, agents).map((sub) => {
                    const subKey = `${group.key}::${sub.agentId}`
                    return (
                      <AgentSubGroup
                        key={subKey}
                        agent={sub.agent}
                        agentId={sub.agentId}
                        count={sub.sessions.length}
                        isCollapsed={collapsedAgentGroups.has(subKey)}
                        onToggle={() => toggleAgentGroup(subKey)}
                      >
                        {sub.sessions.map((session) => (
                          <SessionItem
                            key={session.id}
                            session={session}
                            agents={agents}
                            hideAgentId={sub.agentId === NO_AGENT_KEY ? undefined : sub.agentId}
                            isActive={session.id === activeSessionId}
                            isStreaming={sessionsById[session.id]?.isStreaming ?? false}
                            onSelect={() => handleSelectSession(session)}
                            onDeleted={handleSessionDeleted}
                          />
                        ))}
                      </AgentSubGroup>
                    )
                  })}
                </WorkspaceGroup>
              ))}
            </div>
          ) : null}
        </div>
      </SheetContent>
    </Sheet>
  )
}
