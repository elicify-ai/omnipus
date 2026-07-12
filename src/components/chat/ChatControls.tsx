import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useLocation, useNavigate } from '@tanstack/react-router'
import { Robot, ArrowsClockwise, CaretDown, PencilSimpleLine, CaretLeft, Monitor, SpinnerGap } from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { ModelSelector } from '@/components/ui/model-selector'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { createSession, fetchAgents, fetchWorkspaces, fetchProviders, isWorker, workspacesQueryKeys } from '@/lib/api'
import { formatTokens } from '@/lib/formatTokens'
import { cn } from '@/lib/utils'

// Re-export so the token test file's `import { formatTokens } from './ChatControls'` works.
export { formatTokens }

// Matches /workspaces/<id>/chat so New Chat can stay in-workspace.
const WORKSPACE_CHAT_RE = /^\/workspaces\/[^/]+\/chat$/

interface ChatControlsProps {
  className?: string
}

/**
 * ChatControls — single inline cluster for the workspace top-bar.
 *
 * Layout (left → right): New Chat · Agent · Model · Tokens · Sessions
 *
 * Responsive rules (container-query variants, relative to the @container
 * top-bar in WorkspaceTabContainer):
 *   - New Chat:    always icon + "New Chat" label
 *   - Agent:       always visible, name truncates
 *   - Model:       always visible, flexible width w-[160px] @4xl:w-[200px]
 *   - Token counter: hidden @2xl (hidden below 42rem / 672px container)
 *   - Sessions:    always visible; "Sessions" text label shown at @2xl and up
 *
 * Touch targets: pointer-coarse:min-h-[44px] on all interactive controls
 * (WCAG 2.5.8 / Fitts — 44px on coarse pointers).
 *
 * No-clip safety: cluster uses min-w-0 + truncate; overflow-x-auto with
 * hidden scrollbar as a last-resort guard against extreme viewport/name sizes.
 */
export function ChatControls({ className }: ChatControlsProps) {
  const { activeAgentId, activeSessionId, setActiveSession } = useSessionStore()
  const startNewSession = useSessionStore((s) => s.startNewSession)
  const { sessionTokens, isStreaming } = useChatStore()
  const messages = useChatStore((s) => s.messages)
  const nextModel = useChatStore((s) => s.nextModel)
  const setNextModel = useChatStore((s) => s.setNextModel)
  const addToast = useUiStore((s) => s.addToast)
  const openSessionPanel = useUiStore((s) => s.openSessionPanel)
  const modelSelectorOpen = useUiStore((s) => s.modelSelectorOpen)
  const setModelSelectorOpen = useUiStore((s) => s.setModelSelectorOpen)
  const agentSelectorOpen = useUiStore((s) => s.agentSelectorOpen)
  const setAgentSelectorOpen = useUiStore((s) => s.setAgentSelectorOpen)

  const navigate = useNavigate()
  const location = useLocation()

  // When inside a workspace chat tab, New Chat stays in-place instead of
  // navigating away, preserving the workspace context and the active agent.
  const isWorkspaceChat = WORKSPACE_CHAT_RE.test(location.pathname)

  const handleNewChat = () => {
    if (isWorkspaceChat) {
      startNewSession(activeAgentId, null)
      return
    }
    void navigate({ to: '/' })
  }

  // ADR-039 D-A1: persistent "Open browser" launcher. The backend
  // BrowserManager.Session() lazily creates a blank tab on WS attach, so
  // opening before the agent has browsed anything yields a ready blank
  // browser. Not gated on the active agent actually having browser tools
  // (GET /agents' list response never populates tools_cfg — see
  // pkg/gateway/rest.go's listAgents — so that capability isn't cheaply
  // knowable client-side); the panel's own browser_status(error) surface
  // already handles a no-manager-for-agent response for agents without
  // browser tools (Mia/Ava by seed).
  //
  // UAT finding FE-1: the live view attaches by agentId alone — the WS
  // handshake's session_id is only used for logging/echo on the backend
  // (browser_ws.go's handleAttach always binds to browser.DefaultSessionID,
  // never frame.SessionId — see that file's doc comment), but the frame
  // schema still requires a non-empty session_id, so a brand-new chat with
  // zero messages (activeSessionId === null) could never open the panel at
  // all — the "Open browser" launcher errored on the very case ADR-039
  // designed it for. Fixed by ensuring a real session exists first, mirroring
  // attachment-adapter.ts's ensureSession(): create one via the same
  // POST /sessions the composer's first-send path uses, and adopt it as the
  // active session, before opening the panel. BrowserTool.tsx's
  // handleWatchLive (the "Watch live" affordance on an in-transcript tool
  // call) still requires activeSessionId to already exist there instead — a
  // running tool call implies a session already exists, so it never hits
  // this codepath.
  const [creatingBrowserSession, setCreatingBrowserSession] = useState(false)
  const handleOpenBrowser = async () => {
    if (!activeAgentId) {
      addToast({ message: 'Select an agent before opening the live browser.', variant: 'error' })
      return
    }
    // '__pending' is chat.ts's transient placeholder bucket key for a
    // just-sent first message whose real session_started ack hasn't landed
    // yet (see sendMessage's no-active-session branch) — not a real backend
    // session the browser WS can usefully attach against. Same check as
    // attachment-adapter.ts's ensureSession().
    if (activeSessionId && activeSessionId !== '__pending') {
      useUiStore.getState().openBrowserPanel(activeSessionId, activeAgentId)
      return
    }
    if (creatingBrowserSession) return
    setCreatingBrowserSession(true)
    try {
      const created = await createSession(activeAgentId)
      setActiveSession(created.id, created.agent_id, null)
      useUiStore.getState().openBrowserPanel(created.id, created.agent_id)
    } catch (err) {
      addToast({
        message: err instanceof Error ? err.message : 'Could not start a browser session — try again.',
        variant: 'error',
      })
    } finally {
      setCreatingBrowserSession(false)
    }
  }

  const { data: agents = [], isError: agentsError } = useQuery({
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

  // Auto-select the first ready agent if none is active yet.
  useEffect(() => {
    if (!activeAgentId && chatAgents.length > 0) {
      const first = chatAgents[0]
      setActiveSession(null, first.id, first.type)
    }
  }, [activeAgentId, chatAgents, setActiveSession])

  // ── Model selector data ────────────────────────────────────────────────────
  const { data: providers = [] } = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  const connectedProviders = providers.filter((p) => p.status === 'connected')
  const availableModels = connectedProviders.flatMap((p) => p.models ?? [])
  const providerGroups = connectedProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({ providerName: p.display_name ?? p.name ?? p.id, models: p.models ?? [] }))

  const transcriptLastModel = useMemo<string | null>(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i]
      if (m.role !== 'assistant') continue
      const model = m.model
      if (typeof model === 'string' && model.trim().length > 0) {
        return model.trim()
      }
    }
    return null
  }, [messages])

  const activeAgentModel = useMemo<string | null>(() => {
    const agent = agents.find((a) => a.id === activeAgentId)
    if (!agent || typeof agent.model !== 'string') return null
    const trimmed = agent.model.trim()
    return trimmed.length > 0 ? trimmed : null
  }, [agents, activeAgentId])

  // Re-seed nextModel when the user NAVIGATES to a different session/agent, so
  // the selector shows that session's last-used model. Combined with the chat
  // store no longer clearing nextModel after a send, this makes a model pick
  // sticky within a conversation.
  const lastSeedKey = useRef<string>('')
  useEffect(() => {
    const seedKey = `${activeSessionId ?? ''}::${activeAgentId ?? ''}`
    if (seedKey === lastSeedKey.current) return
    const prevSession = lastSeedKey.current.split('::')[0]
    lastSeedKey.current = seedKey
    // Materialization guard: a fresh chat's first send creates a transient
    // '__pending' bucket that then becomes the real session id
    // ('' → '__pending' → realId). That is the SAME conversation continuing,
    // NOT a navigation to a different session, so it must not clobber the model
    // the user just picked for this message (which would otherwise reset to the
    // default and fail to carry to the next message). Genuine navigation —
    // opening/switching to a real session, or New Chat ('' target) — still
    // re-seeds from that target's last-used model.
    if (activeSessionId === '__pending' || prevSession === '__pending') return
    setNextModel(transcriptLastModel ?? activeAgentModel ?? null)
  }, [activeSessionId, activeAgentId, transcriptLastModel, activeAgentModel, setNextModel])

  function onPickerChange(model: string) {
    setNextModel(model || null)
    if (model === '') {
      addToast({ message: 'Reverted to agent default for next message', variant: 'default' })
    }
  }

  // ── Agent selector helpers ────────────────────────────────────────────────
  const effectiveAgentId = activeAgentId || chatAgents[0]?.id
  const activeAgent = chatAgents.find((a) => a.id === effectiveAgentId)

  const handleAgentSelect = (agentId: string) => {
    const selected = agents.find((a) => a.id === agentId)
    setActiveSession(activeSessionId, agentId, selected?.type ?? null)
  }

  if (agentsError) {
    return (
      <div className={cn('flex items-center gap-2 px-2', className)}>
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

  if (chatAgents.length === 0 && agents.length > 0) {
    return (
      <div className={cn('flex items-center gap-2 px-2 min-w-0', className)}>
        <span className="text-xs text-[var(--color-muted)] truncate">
          All agents are in draft status. Configure an agent to start chatting.
        </span>
      </div>
    )
  }

  return (
    <div
      className={cn(
        // Single inline cluster — never wraps; overflow-x-auto scrolls rather
        // than clips on extreme sizes (≤320px or very long agent/model names).
        'flex items-center gap-1.5 min-w-0 overflow-x-auto',
        className,
      )}
      style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
    >
      {/* 1. New Chat — always icon + label */}
      <button
        type="button"
        onClick={handleNewChat}
        aria-label="New chat"
        className={cn(
          'flex items-center gap-1.5 shrink-0 px-2 h-8 rounded-md text-xs',
          'text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)]',
          'transition-colors whitespace-nowrap',
          'pointer-coarse:min-h-[44px] pointer-coarse:px-3',
        )}
      >
        <PencilSimpleLine size={15} />
        <span>New Chat</span>
      </button>

      {/* 2. Agent picker — always visible, name truncates */}
      <DropdownMenu open={agentSelectorOpen} onOpenChange={setAgentSelectorOpen}>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            data-testid="agent-picker-trigger"
            className={cn(
              'flex items-center gap-2 h-8 px-2 text-xs font-medium max-w-[200px] min-w-0',
              'pointer-coarse:min-h-[44px] pointer-coarse:px-3',
            )}
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

      {/* 3. Model selector — flexible width, always visible */}
      <div
        className={cn(
          'min-w-0 w-[160px] @4xl:w-[200px] shrink-0',
          'pointer-coarse:min-h-[44px]',
        )}
      >
        <ModelSelector
          variant="ghost"
          models={availableModels}
          value={nextModel ?? ''}
          onChange={onPickerChange}
          placeholder={activeAgentModel ?? 'Select a model…'}
          providerGroups={providerGroups}
          triggerTestId="composer-model-selector"
          constrainToCatalog
          emptyCatalogHint="Connect a provider to pick a model"
          open={modelSelectorOpen}
          onOpenChange={setModelSelectorOpen}
        />
      </div>

      {/* 4. Token counter — status, not a control; hidden below @2xl (42rem/672px) */}
      <div
        className="hidden @2xl:flex items-center gap-1 text-xs text-[var(--color-muted)] shrink-0"
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

      {/* 5. Sessions — always visible; "Sessions" label at @2xl+, icon-only below */}
      <button
        type="button"
        onClick={openSessionPanel}
        aria-label="Open sessions panel"
        className={cn(
          'flex items-center justify-center shrink-0 px-2 h-8 gap-1 rounded-md',
          'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
          'transition-colors text-xs',
          'pointer-coarse:min-h-[44px] pointer-coarse:px-3',
        )}
      >
        <span className="hidden @2xl:inline">Sessions</span>
        <CaretLeft size={13} className="rotate-180" />
      </button>

      {/* 6. Open browser — ADR-039 D-A1: user-initiated live browser session,
          independent of any agent tool call. */}
      <button
        type="button"
        onClick={() => void handleOpenBrowser()}
        disabled={creatingBrowserSession}
        aria-label="Open browser"
        aria-busy={creatingBrowserSession}
        title="Open a live browser session"
        className={cn(
          'flex items-center justify-center shrink-0 px-2 h-8 gap-1.5 rounded-md',
          'text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)]',
          'transition-colors text-xs whitespace-nowrap',
          'disabled:cursor-not-allowed disabled:opacity-50',
          'pointer-coarse:min-h-[44px] pointer-coarse:px-3',
        )}
      >
        {creatingBrowserSession ? <SpinnerGap size={15} className="animate-spin" /> : <Monitor size={15} />}
        <span className="hidden @2xl:inline">Open browser</span>
      </button>
    </div>
  )
}
