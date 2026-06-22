import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import {
  Circle,
  ListChecks,
  Trash,
  MagnifyingGlass,
  ChatCircle,
  PaperPlaneTilt,
  DiscordLogo,
  SlackLogo,
  WhatsappLogo,
  Hash,
  Terminal,
  GoogleLogo,
  ChatDots,
  CaretDown,
  CaretRight,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
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
  fetchWorkspaceSessions,
  fetchWorkspaces,
  renameSession,
  deleteSession,
  isApiError,
  workspacesQueryKeys,
} from '@/lib/api'
import type { Agent, Session } from '@/lib/api'
import { cn } from '@/lib/utils'

function sessionButtonClass(isActive: boolean): string {
  return isActive
    ? 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
    : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]'
}

const UNTITLED_SESSION = 'Untitled Session'

// ── Channel metadata helper ────────────────────────────────────────────────────

interface ChannelMeta {
  icon: Icon
  label: string
}

function getChannelMeta(channel: string): ChannelMeta {
  switch (channel) {
    case 'webchat':
      return { icon: ChatCircle, label: 'Web Chat' }
    case 'telegram':
      return { icon: PaperPlaneTilt, label: 'Telegram' }
    case 'discord':
      return { icon: DiscordLogo, label: 'Discord' }
    case 'slack':
      return { icon: SlackLogo, label: 'Slack' }
    case 'whatsapp_native':
      return { icon: WhatsappLogo, label: 'WhatsApp' }
    case 'matrix':
      return { icon: Hash, label: 'Matrix' }
    case 'irc':
      return { icon: Terminal, label: 'IRC' }
    case 'google-chat':
      return { icon: GoogleLogo, label: 'Google Chat' }
    default: {
      // Capitalise first letter, replace hyphens/underscores with spaces
      const label = channel
        .replace(/[-_]/g, ' ')
        .replace(/^\w/, (c) => c.toUpperCase())
      return { icon: ChatDots, label }
    }
  }
}

// ── Agent participation badges ────────────────────────────────────────────────

interface AgentBadgesProps {
  agentIds: string[]
  agents: Agent[]
}

function AgentBadges({ agentIds, agents }: AgentBadgesProps) {
  if (agentIds.length === 0) return null
  return (
    <div className="flex -space-x-1 shrink-0">
      {agentIds.map((id) => {
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

function SessionItem({ session, agents, isActive, isStreaming, onSelect, onDeleted }: SessionItemProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [isEditing, setIsEditing] = useState(false)
  const [editValue, setEditValue] = useState(session.title || UNTITLED_SESSION)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

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
      {/* Agent participation badges */}
      <AgentBadges agentIds={participantIds} agents={agents} />

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
            type="button"
            onClick={onSelect}
            onDoubleClick={(e) => {
              e.preventDefault()
              setIsEditing(true)
            }}
            aria-label={`Open session: ${session.title || UNTITLED_SESSION}`}
            title="Click to open, double-click to rename"
            className="w-full text-left"
          >
            <div className="flex items-center gap-1.5 min-w-0">
              {isActive && (
                <Circle size={5} weight="fill" className="text-[var(--color-success)] shrink-0" />
              )}
              {isTask && (
                <ListChecks size={10} className="text-[var(--color-accent)] shrink-0" />
              )}
              <span className="truncate text-xs">{session.title || UNTITLED_SESSION}</span>
              {isStreaming && !isActive && (
                // Background session is generating — pulse dot so the user knows work is in progress.
                <span className="ml-auto shrink-0 w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-pulse" aria-label="Generating" />
              )}
            </div>
          </button>
        )}
      </div>

      {/* Right side: task status badge + relative time + delete */}
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
          <span className="text-[10px] text-[var(--color-muted)] tabular-nums">
            {formatRelativeTime(session.updated_at)}
          </span>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              setConfirmDelete(true)
            }}
            className="p-1 rounded opacity-0 group-hover/item:opacity-100 text-[var(--color-muted)] hover:text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-all"
            aria-label={`Delete session: ${session.title || UNTITLED_SESSION}`}
            title="Delete session"
          >
            <Trash size={11} />
          </button>
        </div>
      )}
    </div>
  )
}

// ── Main panel ────────────────────────────────────────────────────────────────

export function SessionPanel() {
  const { sessionPanelOpen, closeSessionPanel } = useUiStore()
  const { activeSessionId, activeAgentId, setActiveSession, attachToSession, setActiveAgentType } = useSessionStore()
  const sessionsById = useChatStore((s) => s.sessionsById)
  const queryClient = useQueryClient()

  const [searchValue, setSearchValue] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Collapsed channel group keys; default: all expanded (empty set)
  const [collapsedChannels, setCollapsedChannels] = useState<Set<string>>(() => new Set())

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

  // Scope the panel to the active workspace's conversations (IA reframe): the
  // history opened from a workspace chat shows only that project's sessions,
  // resolved via the workspace→session links. When no workspace is active the
  // panel shows every session (global / deep-link case).
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)

  // The global "/" front door redirects into the DEFAULT workspace's chat, so
  // the active workspace is almost never null in practice — it is the default
  // workspace. The default workspace is the home/inbox: it must keep the
  // pre-IA "show every session" behaviour, otherwise global / deep-linked /
  // REST-created sessions (which carry no workspace→task link until the agent
  // creates a task) silently vanish from the only panel that can reach them.
  // We therefore scope strictly ONLY for non-default workspaces.
  const { data: workspaces } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    enabled: sessionPanelOpen && !!activeWorkspaceId,
    staleTime: 30_000,
  })
  const activeIsDefaultWorkspace = useMemo(() => {
    if (!activeWorkspaceId || !workspaces) return false
    const active = workspaces.find((w) => w.id === activeWorkspaceId)
    return active?.is_default === true
  }, [activeWorkspaceId, workspaces])

  // Only fetch (and apply) the workspace→session link set for a non-default
  // workspace. In the default workspace we never scope, so the link query is
  // unnecessary.
  const scopeToWorkspace = !!activeWorkspaceId && !activeIsDefaultWorkspace
  const { data: workspaceLinks } = useQuery({
    queryKey: workspacesQueryKeys.sessions(activeWorkspaceId ?? ''),
    queryFn: () => fetchWorkspaceSessions(activeWorkspaceId!),
    enabled: sessionPanelOpen && scopeToWorkspace,
    staleTime: 30_000,
  })
  // A session may be tagged with >1 workspace (cross-project edge case), so we
  // include any session whose id appears in this workspace's link set.
  const workspaceSessionIds = useMemo(
    () =>
      scopeToWorkspace && workspaceLinks
        ? new Set(workspaceLinks.map((l) => l.session_id))
        : null,
    [scopeToWorkspace, workspaceLinks],
  )

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
    attachToSession(session.id, session.type, session.title, agentId)
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

  // Scope to the active workspace's sessions first (when a workspace is active
  // and its links have loaded), then sort by updated_at descending.
  const scopedSessions = workspaceSessionIds
    ? sessions.filter((s) => workspaceSessionIds.has(s.id))
    : sessions
  const sortedSessions = [...scopedSessions].sort(
    (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
  )

  // Apply search filter: match title, or any participating agent's name
  const searchLower = debouncedSearch.toLowerCase().trim()

  const filteredSessions = searchLower
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

  // Group sessions by channel: webchat first, then other channels alphabetically.
  // Sessions without a channel field default to "webchat".
  const groupedSessions = useMemo(() => {
    const groups = new Map<string, Session[]>()
    for (const s of filteredSessions) {
      const ch = s.channel || 'webchat'
      const existing = groups.get(ch) ?? []
      groups.set(ch, [...existing, s])
    }
    // Sort: webchat first, then alphabetical
    return [...groups.entries()].sort(([a], [b]) => {
      if (a === 'webchat') return -1
      if (b === 'webchat') return 1
      return a.localeCompare(b)
    })
  }, [filteredSessions])

  const toggleChannel = (ch: string) => {
    setCollapsedChannels((prev) => {
      const next = new Set(prev)
      if (next.has(ch)) {
        next.delete(ch)
      } else {
        next.add(ch)
      }
      return next
    })
  }

  // Resolve the default agent ID (used for active indicator in header)
  const activeAgent = agents.find((a) => a.id === activeAgentId)

  // Whether to render grouped view: only when more than one distinct channel exists
  const showGroups = groupedSessions.length > 1

  return (
    <Sheet open={sessionPanelOpen} onOpenChange={(open) => !open && closeSessionPanel()}>
      <SheetContent side="right" className="w-72 p-0 flex flex-col" overlay={false}>
        <SheetHeader className="px-4 pt-5 pb-3 border-b border-[var(--color-border)]">
          <div className="flex items-center gap-2">
            <SheetTitle className="flex-1">Sessions</SheetTitle>
            {activeAgent && (
              <div
                className="w-5 h-5 rounded-full flex items-center justify-center shrink-0"
                style={{ backgroundColor: activeAgent.color ?? 'var(--color-surface-3)' }}
                title={`Active: ${activeAgent.name}`}
              >
                {activeAgent.icon ? (
                  <IconRenderer icon={activeAgent.icon} size={11} />
                ) : (
                  <span className="text-[9px] font-bold text-[var(--color-secondary)]">
                    {activeAgent.name.charAt(0).toUpperCase()}
                  </span>
                )}
              </div>
            )}
          </div>
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

          {filteredSessions.length === 0 ? (
            <div className="px-4 py-6 text-xs text-[var(--color-muted)] text-center">
              {searchLower
                ? 'No results.'
                : workspaceSessionIds
                  ? 'No conversations in this workspace yet. Start a chat to begin.'
                  : 'No sessions yet. Start a conversation to begin.'}
            </div>
          ) : showGroups ? (
            // Multi-channel view: collapsible group headers + sessions within each group
            <div className="py-1">
              {groupedSessions.map(([ch, groupSessions]) => {
                const meta = getChannelMeta(ch)
                const ChannelIcon = meta.icon
                const isCollapsed = collapsedChannels.has(ch)
                return (
                  <div key={ch}>
                    {/* Group header */}
                    <button
                      type="button"
                      onClick={() => toggleChannel(ch)}
                      className="w-full flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-[var(--color-muted)] uppercase tracking-wider hover:text-[var(--color-secondary)] transition-colors"
                      aria-expanded={!isCollapsed}
                      aria-label={`${meta.label} sessions, ${isCollapsed ? 'expand' : 'collapse'}`}
                    >
                      {isCollapsed ? (
                        <CaretRight size={10} className="shrink-0 transition-transform" />
                      ) : (
                        <CaretDown size={10} className="shrink-0 transition-transform" />
                      )}
                      <ChannelIcon size={14} className="shrink-0" />
                      <span className="flex-1 text-left truncate">{meta.label}</span>
                      <Badge variant="secondary" className="text-[9px] h-4 px-1.5 rounded-full shrink-0">
                        {groupSessions.length}
                      </Badge>
                    </button>

                    {/* Sessions within this group */}
                    {!isCollapsed && (
                      <div className="space-y-0.5 px-2 pb-1">
                        {groupSessions.map((session) => (
                          <SessionItem
                            key={session.id}
                            session={session}
                            agents={agents}
                            isActive={session.id === activeSessionId}
                            isStreaming={sessionsById[session.id]?.isStreaming ?? false}
                            onSelect={() => handleSelectSession(session)}
                            onDeleted={handleSessionDeleted}
                          />
                        ))}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          ) : (
            // Single-channel view: flat list, no headers (no regression)
            <div className="py-1 space-y-0.5 px-2">
              {filteredSessions.map((session) => (
                <SessionItem
                  key={session.id}
                  session={session}
                  agents={agents}
                  isActive={session.id === activeSessionId}
                  isStreaming={sessionsById[session.id]?.isStreaming ?? false}
                  onSelect={() => handleSelectSession(session)}
                  onDeleted={handleSessionDeleted}
                />
              ))}
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
