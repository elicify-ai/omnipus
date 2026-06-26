import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Robot, ArrowsClockwise, CaretDown, PencilSimpleLine } from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents, fetchWorkspaces, isWorker, workspacesQueryKeys } from '@/lib/api'

/** Human-readable token count: 44.0k / 1.2M. Values under 1000 show as-is. */
export function formatTokens(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M`
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}k`
  return tokens.toString()
}

export function SessionBar() {
  const { activeAgentId, activeSessionId, setActiveSession } = useSessionStore()
  const { sessionTokens, isStreaming } = useChatStore()
  const navigate = useNavigate()

  // New Chat just navigates to "/". RootChatScreen owns the new-session
  // lifecycle: it clears the active session on mount and only advances the URL
  // when a session is genuinely minted there, so a stale activeSessionId can no
  // longer bounce the URL back to the old conversation (#417). This is identical
  // to clicking the sidebar "Chat" link — the route is the single source of truth.
  const handleNewChat = () => {
    void navigate({ to: '/' })
  }

  const { data: agents = [], isError: agentsError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // The active workspace scopes the chat agent picker to that workspace's team
  // (core_team). When no workspace is active (e.g. a bare /sessions deep-link)
  // we fall back to the full roster.
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
    enabled: !!activeWorkspaceId,
  })
  const activeWorkspace = workspaces.find((w) => w.id === activeWorkspaceId)
  // core_team is the workspace's roster. Empty/absent → no scoping (full roster),
  // so a freshly-created workspace with no team yet still lets the user chat.
  const teamIds = activeWorkspace?.core_team

  // Only show agents that are ready to chat (active or idle — not draft) and
  // exclude workers: a worker (type === 'worker') is a delegation-only labour
  // agent, never a chat target, so it must never appear in the switcher nor be
  // auto-selected as the active chat persona below. When a workspace team is
  // configured, additionally scope to that team.
  const chatAgents = agents
    .filter((a) => (a.status === 'active' || a.status === 'idle') && !isWorker(a))
    .filter((a) => !teamIds || teamIds.length === 0 || teamIds.includes(a.id))

  // Auto-select the first ready agent if none is active yet.
  // Done in useEffect (not during render) to avoid calling setState mid-render,
  // which causes infinite loops in React 18+ strict mode.
  useEffect(() => {
    if (!activeAgentId && chatAgents.length > 0) {
      const first = chatAgents[0]
      setActiveSession(null, first.id, first.type)
    }
  }, [activeAgentId, chatAgents, setActiveSession])

  if (agentsError) {
    return (
      <div className="flex items-center gap-2 px-2">
        <span className="text-xs text-[var(--color-error)]">Could not load agents</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[10px]"
          onClick={() => window.location.reload()}
        >
          Retry
        </Button>
      </div>
    )
  }

  // When all agents exist but are still drafts, guide the user instead of showing an empty dropdown
  if (chatAgents.length === 0 && agents.length > 0) {
    return (
      <div className="flex items-center gap-2 px-2 min-w-0">
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
    // Preserve the current session when switching agents. Passing null as
    // sessionId clears activeSessionId while the route hash may still point
    // at /#/sessions/<id>; the route hook then re-attaches → isReplaying
    // flips true → composer disables → done frame → isReplaying flips false
    // → composer re-enables. The textarea remounts on every flip, which
    // detaches Playwright's locator between fill() and press(Enter) and
    // causes T26 (audit log after cancel) to time out. Forward the current
    // sessionId so the in-session agent switch is a single store update.
    setActiveSession(activeSessionId, agentId, selected?.type ?? null)
  }

  return (
    <div className="flex items-center gap-3 min-w-0 w-full">
      {/* Agent selector + New chat icon */}
      <div className="flex items-center gap-0.5">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            className="flex items-center gap-2 h-7 px-2 text-xs font-medium max-w-[280px]"
            title={activeAgent?.description || activeAgent?.name || 'Select agent'}
          >
            <div
              className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
              style={{
                backgroundColor: activeAgent?.color ?? 'var(--color-surface-3)',
              }}
            >
              {activeAgent
                ? activeAgent.icon
                  ? <IconRenderer icon={activeAgent.icon} size={11} />
                  : activeAgent.name.charAt(0).toUpperCase()
                : <Robot size={11} />}
            </div>
            <span className="truncate">
              {activeAgent
                ? activeAgent.name
                : 'Select agent'}
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

      {/* New Chat — icon-only on mobile, icon+text on desktop */}
      <button
        type="button"
        onClick={handleNewChat}
        title="New chat"
        className="sm:hidden w-7 h-7 rounded-md flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors"
      >
        <PencilSimpleLine size={15} />
      </button>
      </div>

      <Button
        variant="ghost"
        size="sm"
        className="hidden sm:flex h-7 px-2 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] gap-1"
        onClick={handleNewChat}
        title="New chat"
      >
        <PencilSimpleLine size={13} />
        <span>New Chat</span>
      </Button>

      {/* Model */}
      {activeAgent?.model && (
        <span className="hidden sm:inline text-xs text-[var(--color-muted)] font-mono truncate max-w-[120px]">
          {activeAgent.model}
        </span>
      )}

      {/* Separator */}
      <div className="h-4 w-px bg-[var(--color-border)] hidden sm:block" />

      {/* Token counter */}
      <div
        className="hidden sm:flex items-center gap-1 text-xs text-[var(--color-muted)]"
        data-testid="session-token-counter"
        aria-label={`${sessionTokens} tokens used`}
      >
        <ArrowsClockwise
          size={11}
          className={isStreaming ? 'animate-spin text-[var(--color-accent)]' : ''}
          aria-hidden="true"
        />
        <span
          className={`font-mono tabular-nums${isStreaming ? ' text-[var(--color-secondary)]' : ''}`}
          data-testid="session-token-value"
        >
          {formatTokens(sessionTokens)} tokens
        </span>
      </div>

    </div>
  )
}
