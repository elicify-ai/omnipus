import { useState, useRef, useEffect } from 'react'
import { createPortal } from 'react-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Circle, ListChecks, Clock, Heartbeat, Trash } from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { useUiStore } from '@/store/ui'
import { renameSession, deleteSession, isApiError } from '@/lib/api'
import type { Agent, Session } from '@/lib/api'
import { cn } from '@/lib/utils'
import { formatTokens } from '@/lib/formatTokens'

// ── Shared constants & helpers ────────────────────────────────────────────────

const UNTITLED_SESSION = 'Untitled Session'

function sessionButtonClass(isActive: boolean): string {
  return isActive
    ? 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
    : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]'
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

// ── Inline rename + delete session item ──────────────────────────────────────

export interface SessionItemProps {
  session: Session
  agents: Agent[]
  isActive: boolean
  isStreaming: boolean
  onSelect: (session: Session) => void
  onDeleted: (sessionId: string) => void
  // The owning agent sub-group's agent id, hidden from this row's participant
  // badges to avoid repeating the avatar shown in the group header.
  hideAgentId?: string
  // When true (heartbeat session with protected=true), the delete button is
  // hidden so users cannot attempt deletion that would return 409 (FR-021/028).
  deleteDisabled?: boolean
}

export function SessionItem({
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
          <Input
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
            onClick={() => onSelect(session)}
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
