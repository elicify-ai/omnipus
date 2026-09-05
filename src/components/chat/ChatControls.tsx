import { useState } from 'react'
import { Monitor, SpinnerGap, Files } from '@phosphor-icons/react'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { createSession } from '@/lib/api'
import { cn } from '@/lib/utils'

interface ChatControlsProps {
  className?: string
}

/**
 * ChatControls — workspace top-bar cluster; now solely the "Open browser"
 * launcher (New Chat moved to the sidebar row + /new; Sessions superseded
 * by the sidebar accordion + SearchModal).
 *
 * The Agent picker, Model selector, and Token counter used to live here but
 * moved into the composer's context row, above the card (src/components/chat/composer/
 * {AgentPicker,ModelPicker,TokenCounter}.tsx) so they sit next to the input
 * they scope, per the Composer Redesign (variant A1).
 *
 * Touch target: pointer-coarse:min-h-[44px] on the Open browser button
 * (WCAG 2.5.8 / Fitts — 44px on coarse pointers).
 *
 * No-clip safety: cluster uses min-w-0; overflow-x-auto with hidden
 * scrollbar as a last-resort guard against extreme viewport sizes.
 */
export function ChatControls({ className }: ChatControlsProps) {
  const { activeAgentId, activeSessionId, setActiveSession } = useSessionStore()
  const addToast = useUiStore((s) => s.addToast)
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)

  // library-spec D-3, second entry point. Opening from HERE passes the active
  // workspace id, so the explorer lands directly in that workspace's work/
  // tree. Opening from the sidebar passes nothing, which selects the virtual
  // root listing every workspace — same component, different initial
  // selection. Unlike the browser launcher above there is no session to
  // create first: the Library is a view over files on disk, not a live
  // attachment to a running agent, so it opens immediately.
  const handleOpenLibrary = () => {
    if (!activeWorkspaceId) {
      // Fall back to the virtual root rather than refusing outright — with no
      // active workspace, "all workspaces" is still a useful, correct view.
      useUiStore.getState().openLibraryPanel(undefined)
      return
    }
    useUiStore.getState().openLibraryPanel(activeWorkspaceId)
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
      // U2: the workspace this chat belongs to travels WITH the create.
      //
      // The panel about to open resolves which workspace's browser — and whose
      // live logins — it shows by reading the workspace off this very session's
      // meta, server-side (ADR-075 FR-016/FR-017); nothing on the attach frame
      // carries it, deliberately, so a client cannot ask to drive a workspace's
      // browser just by saying so. Creating the session with agent_id alone
      // therefore handed the panel a session that named no workspace, and an
      // agent on more than one workspace's team was refused as ambiguous —
      // advised to "open this panel from a chat that belongs to the workspace
      // you mean", which is exactly where the click came from. The workspace
      // was in the route and in this store the whole time; it just never made
      // the trip.
      //
      // `undefined` on the global/inbox chat is correct and stays correct: no
      // workspace is not the same as a default one, and the refusal is right
      // when there is genuinely nothing to disambiguate on.
      const created = await createSession(activeAgentId, activeWorkspaceId ?? undefined)
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
      {/* New Chat was removed from the header — three paths for one action was
          redundant (Hick's Law). It lives where the user already is: the
          sidebar's per-workspace "New chat" row and the /new slash command. */}

      {/* Open browser — ADR-039 D-A1: user-initiated live browser session,
          independent of any agent tool call. */}
      <button
        type="button"
        onClick={() => void handleOpenBrowser()}
        disabled={creatingBrowserSession}
        // Composer tab ring — full map (single source of truth; other spots
        // point back here): skip-link=1 (AppShell.tsx) → chat input=2
        // (ChatScreen.tsx) → agent=3 (composer/AgentPicker.tsx) → model=4
        // (composer/ModelPicker.tsx) → attach=5 (ChatScreen.tsx) → send=6
        // (ChatScreen.tsx) → browser=7 (this button), then natural DOM order
        // (the header tab menu). Deliberate positive tabIndex on this closed
        // 7-control set per operator direction.
        //
        // Slot 6 has THREE possible occupants in ChatScreen.tsx, mutually
        // exclusive by render condition so only one is ever mounted at a
        // time: (1) idle — ComposerPrimitive.Send (`chat-send`); (2)
        // streaming — the Stop button (`stop-btn`), which replaces Send in
        // this exact slot rather than defaulting to 0, so cancel is never
        // silently dropped from the ring while a turn runs; (3) mid-turn
        // steering (bugfixes3) — once Stop has swapped in, a SECOND slot-6
        // control, the plain mid-stream Send button (`chat-send-mid-stream`),
        // renders next to it (only once there's text to steer — see that
        // button's own doc comment) so cancel stays reachable in exactly one
        // keystroke even when steering is also available.
        tabIndex={7}
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

      {/* Open Library — library-spec D-3's second entry point. Scoped to the
          active workspace (the sidebar entry opens the all-workspaces virtual
          root instead). tabIndex 8 continues the closed composer ring
          documented on the browser button above; it sits after browser=7 so
          the existing 1-7 order is untouched. */}
      <button
        type="button"
        onClick={handleOpenLibrary}
        tabIndex={8}
        aria-label="Open library"
        title="Browse this workspace's files"
        className={cn(
          'flex items-center justify-center shrink-0 px-2 h-8 gap-1.5 rounded-md',
          'text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)]',
          'transition-colors text-xs whitespace-nowrap',
          'pointer-coarse:min-h-[44px] pointer-coarse:px-3',
        )}
      >
        <Files size={15} />
        <span className="hidden @2xl:inline">Library</span>
      </button>
    </div>
  )
}
