import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Robot, CaretDown } from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { fetchAgents, fetchWorkspaces, isWorker, workspacesQueryKeys } from '@/lib/api'
import { cn } from '@/lib/utils'

/**
 * AgentPicker — the workspace-team-scoped agent selector.
 *
 * Extracted from ChatControls so it can live inside the composer's context
 * row (next to the input it scopes) instead of the workspace header — logic
 * unchanged except the auto-select session-preservation fix (below) and the
 * error/read-only hardening. Calls the same Zustand stores + react-query
 * keys; same testid (`agent-picker-trigger`); same SC-005 `setActiveSession`
 * contract.
 *
 * Owns the active-workspace core_team scoping and the
 * auto-select-first-ready-agent effect — these are solely the picker's
 * concern (the agent-list query itself is shared: ModelPicker runs the
 * identical `['agents']` query and the two dedupe via React Query's cache).
 *
 * Side-effect contract: the auto-select effect writes to the global session
 * store (`setActiveSession`) as a side effect of mounting, so this component
 * is expected to be mounted exactly once per screen (in the composer's
 * context row) — mounting it twice would race two auto-select writers.
 */
export function AgentPicker({
  className,
  disabled = false,
}: {
  className?: string
  /** Read-only mode (e.g. agentRemoved) — disables the trigger and skips auto-select. */
  disabled?: boolean
}) {
  const { activeAgentId, activeSessionId, setActiveSession } = useSessionStore()
  const agentSelectorOpen = useUiStore((s) => s.agentSelectorOpen)
  const setAgentSelectorOpen = useUiStore((s) => s.setAgentSelectorOpen)

  const { data: agents = [], isError: agentsError, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // Scope the agent picker to the active workspace's core_team.
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
    enabled: !!activeWorkspaceId,
  })
  const activeWorkspace = workspaces.find((w) => w.id === activeWorkspaceId)
  const teamIds = activeWorkspace?.core_team

  // Only ready-to-chat, non-worker agents, optionally scoped to workspace team.
  const chatAgents = agents
    .filter((a) => (a.status === 'active' || a.status === 'idle') && !isWorker(a))
    .filter((a) => !teamIds || teamIds.length === 0 || teamIds.includes(a.id))

  // A background-refetch failure (e.g. gateway restart + tab refocus) must
  // not tear down an already-usable cached picker — only treat this as a
  // hard error when there is no cached agent data to fall back on.
  const hasHardError = agentsError && agents.length === 0
  // Draft-branch: agents exist, but none is ready to chat yet.
  const isDraftOnly = chatAgents.length === 0 && agents.length > 0

  // The `/agents` slash command sets agentSelectorOpen=true unconditionally
  // (useSlashMenu.ts), but the error/draft branches below return before the
  // DropdownMenu mounts — without this reset the flag would latch and the
  // menu would spontaneously pop open the instant the query recovers (e.g. a
  // background refetch resolves). Placed BEFORE the early returns so hook
  // order stays stable across renders.
  useEffect(() => {
    if ((hasHardError || isDraftOnly) && agentSelectorOpen) {
      setAgentSelectorOpen(false)
    }
  }, [hasHardError, isDraftOnly, agentSelectorOpen, setAgentSelectorOpen])

  // Auto-select the first ready agent if none is active yet.
  //
  // Preserve activeSessionId here — setActiveSession writes its first
  // argument to the store UNCONDITIONALLY (src/store/session.ts), so passing
  // null (as this used to) detaches whatever session is currently active.
  // ChatControls only ever rendered in the workspace header, where there was
  // rarely a deep-linked session to lose; the composer also renders on the
  // /sessions/{id} deep-link route (src/routes/_app/sessions.$sessionId.tsx),
  // where a legacy session with no agent fields would silently detach the
  // just-opened session the instant this effect ran.
  //
  // Reads FRESH state via getState() rather than trusting the closure's
  // activeAgentId/activeSessionId, and re-checks `!freshAgent` right before
  // writing — guards against a stale-closure race where this effect and
  // another writer both observed the pre-write activeAgentId===null and
  // would otherwise both fire a write.
  //
  // setActiveSession unconditionally clears attachedSessionType/
  // attachedTaskTitle (src/store/session.ts), which would otherwise silently
  // wipe the "Task:" banner off a deep-linked task session the instant
  // auto-select ran. Capture both BEFORE the write (they're about to be
  // nulled) and restore them via the store's own setAttachedContext setter
  // immediately after.
  useEffect(() => {
    if (disabled) return
    if (!activeAgentId && chatAgents.length > 0) {
      const {
        activeSessionId: freshSession,
        activeAgentId: freshAgent,
        attachedSessionType,
        attachedTaskTitle,
        setAttachedContext,
      } = useSessionStore.getState()
      if (freshAgent) return
      const first = chatAgents[0]
      setActiveSession(freshSession, first.id, first.type)
      if (attachedSessionType) {
        setAttachedContext(attachedSessionType, attachedTaskTitle)
      }
    }
  }, [activeAgentId, activeSessionId, chatAgents, setActiveSession, disabled])

  if (hasHardError) {
    return (
      <div className={cn('flex items-center gap-2 px-2 min-w-0', className)}>
        <span className="text-xs text-[var(--color-error)] truncate">Could not load agents</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[10px] shrink-0"
          onClick={() => refetch()}
        >
          Retry
        </Button>
      </div>
    )
  }

  if (isDraftOnly) {
    return (
      <div className={cn('flex items-center gap-2 px-2 min-w-0', className)}>
        <span className="text-xs text-[var(--color-muted)] truncate">
          All agents are in draft status. Configure an agent to start chatting.
        </span>
      </div>
    )
  }

  const effectiveAgentId = activeAgentId || chatAgents[0]?.id
  const activeAgent = chatAgents.find((a) => a.id === effectiveAgentId)

  const handleAgentSelect = (agentId: string) => {
    const selected = agents.find((a) => a.id === agentId)
    setActiveSession(activeSessionId, agentId, selected?.type ?? null)
  }

  return (
    <DropdownMenu open={agentSelectorOpen} onOpenChange={setAgentSelectorOpen}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          data-testid="agent-picker-trigger"
          disabled={disabled}
          className={cn(
            'flex items-center gap-2 h-8 px-2 text-xs font-medium max-w-[200px] min-w-0',
            'pointer-coarse:min-h-[44px] pointer-coarse:px-3',
            className,
          )}
          title={activeAgent?.description || activeAgent?.name || 'Select agent'}
          aria-label={`Select agent (current: ${activeAgent?.name ?? 'none'})`}
        >
          <div
            className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
            style={{ backgroundColor: activeAgent?.color ?? 'var(--color-surface-3)' }}
          >
            {activeAgent
              ? activeAgent.icon
                ? <IconRenderer icon={activeAgent.icon} size={11} />
                : activeAgent.name.charAt(0).toUpperCase()
              : <Robot size={11} />}
          </div>
          <span className="truncate">
            {activeAgent ? activeAgent.name : 'Select agent'}
          </span>
          <CaretDown size={11} className="shrink-0 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-96">
        {chatAgents.map((agent) => (
          <DropdownMenuItem
            key={agent.id}
            onClick={() => handleAgentSelect(agent.id)}
            className="flex items-center gap-2"
            title={agent.description || agent.name}
          >
            <div
              className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
              style={{ backgroundColor: agent.color ?? 'var(--color-surface-3)' }}
            >
              {agent.icon
                ? <IconRenderer icon={agent.icon} size={11} />
                : agent.name.charAt(0).toUpperCase()}
            </div>
            <span className="truncate">{agent.name}</span>
            {agent.id === effectiveAgentId && (
              <span className="ml-auto shrink-0 text-[var(--color-success)] text-[10px]">active</span>
            )}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
