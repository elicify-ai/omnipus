import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Heartbeat,
  MagnifyingGlass,
  CaretDown,
  CaretRight,
  Folder,
} from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import {
  fetchAgents,
  fetchSessions,
  fetchWorkspaces,
  workspacesQueryKeys,
} from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'
import { SessionItem } from './SessionItem'
import { useSelectSession } from './useSelectSession'

const NO_WORKSPACE_KEY = '__no_workspace__'
const NO_AGENT_KEY = '__no_agent__'

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
  const { activeSessionId, setActiveSession } = useSessionStore()
  const sessionsById = useChatStore((s) => s.sessionsById)
  const queryClient = useQueryClient()

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

  // Fetch all workspaces (active) to build the name map and resolve orphaned sessions.
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    enabled: sessionPanelOpen,
    staleTime: 30_000,
  })

  const selectSession = useSelectSession({
    agents,
    workspaces,
    onClose: closeSessionPanel,
  })

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
        <SheetHeader>
          <SheetTitle>Sessions</SheetTitle>
        </SheetHeader>

        {/* Search input — no border-b: keeps the title flowing into content
            so the panel reads as one flat surface, not a boxed header zone. */}
        <div className="px-4 py-2">
          <div className="flex items-center gap-2 rounded-lg bg-[var(--color-surface-2)] border border-[var(--color-border)] px-3 py-1.5">
            <MagnifyingGlass size={13} className="text-[var(--color-muted)] shrink-0" />
            <Input
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
              className="pb-1 pt-2"
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
                    onSelect={() => selectSession(session)}
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
                            onSelect={() => selectSession(session)}
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
