import { useState } from 'react'
import { useLocation, useNavigate } from '@tanstack/react-router'
import { PencilSimpleLine, CaretLeft, Monitor, SpinnerGap } from '@phosphor-icons/react'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { createSession } from '@/lib/api'
import { cn } from '@/lib/utils'

// Matches /workspaces/<id>/chat so New Chat can stay in-workspace.
const WORKSPACE_CHAT_RE = /^\/workspaces\/[^/]+\/chat$/

interface ChatControlsProps {
  className?: string
}

/**
 * ChatControls — workspace top-bar cluster: New Chat · Sessions · Open browser.
 *
 * The Agent picker, Model selector, and Token counter used to live here but
 * moved into the composer's context row, above the card (src/components/chat/composer/
 * {AgentPicker,ModelPicker,TokenCounter}.tsx) so they sit next to the input
 * they scope, per the Composer Redesign (variant A1).
 *
 * Touch targets: pointer-coarse:min-h-[44px] on all interactive controls
 * (WCAG 2.5.8 / Fitts — 44px on coarse pointers).
 *
 * No-clip safety: cluster uses min-w-0; overflow-x-auto with hidden
 * scrollbar as a last-resort guard against extreme viewport sizes.
 */
export function ChatControls({ className }: ChatControlsProps) {
  const { activeAgentId, activeSessionId, setActiveSession } = useSessionStore()
  const startNewSession = useSessionStore((s) => s.startNewSession)
  const addToast = useUiStore((s) => s.addToast)

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

  return (
    <div
      className={cn(
        // Single inline cluster — never wraps; overflow-x-auto scrolls rather
        // than clips on extreme sizes (≤320px).
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

      {/* 2. Search — opens the cross-workspace session search modal (replaces the old Sessions panel,
          which is now merged into the sidebar accordion). */}
      <button
        type="button"
        onClick={() => useUiStore.getState().openSearchModal()}
        aria-label="Search sessions"
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

      {/* 3. Open browser — ADR-039 D-A1: user-initiated live browser session,
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
