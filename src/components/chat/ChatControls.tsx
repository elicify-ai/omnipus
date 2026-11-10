import { useState } from 'react'
import { Monitor, SpinnerGap } from '@phosphor-icons/react'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { createSession } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'

interface ChatControlsProps {
  className?: string
}

/**
 * ChatControls — workspace top-bar cluster; now solely the "Open browser"
 * launcher (New Chat moved to the sidebar row + /new; Sessions superseded
 * by the sidebar accordion + SearchModal).
 *
 * The model selector and token counter live in the composer's context row
 * (src/components/chat/composer/{ModelPicker,TokenCounter}.tsx). The agent
 * picker that used to sit with them is gone (FR-007).
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
  // CRIT-001/MIN-206: opening the Browser REPLACES whatever panel is open
  // (SP-7), so the outgoing panel's leave gate (the Library's unsaved-edit
  // confirmation) runs BEFORE the store moves — and before session creation:
  // a Cancel aborts with no request sent. The open itself happens inside the
  // gate's continuation (synchronously when clean).
  const handleOpenBrowser = () => {
    if (!activeAgentId) {
      addToast({ message: 'Select an agent before opening the live browser.', variant: 'error' })
      return
    }
    // '__pending' is chat.ts's transient placeholder bucket key for a
    // just-sent first message whose real session_started ack hasn't landed
    // yet (see sendMessage's no-active-session branch) — not a real backend
    // session the browser WS can usefully attach against. Same check as
    // attachment-adapter.ts's ensureSession(). Captured now so the values the
    // gate approved are the values that open, even after a dialog round-trip.
    const sessionId = activeSessionId && activeSessionId !== '__pending' ? activeSessionId : null
    const agentId = activeAgentId
    const workspaceId = activeWorkspaceId
    const approvedPanel = useUiStore.getState().activePanel
    leaveGateThen(approvedPanel?.id ?? null, () => {
      if (sessionId) {
        useUiStore.getState().openPanel('browser', { sessionId, agentId })
        return
      }
      if (creatingBrowserSession) return
      const panelAtCreationStart = useUiStore.getState().activePanel
      setCreatingBrowserSession(true)
      void (async () => {
        try {
          // ADR-075 — Browser tools: workspace-scoped, and usable by an
          // agent (D1.13; pkg/gateway/browser_ws.go::handleAttach): the
          // workspace this chat belongs to travels WITH the create.
          //
          // The panel about to open resolves which workspace's browser — and whose
          // live logins — it shows by reading the workspace off this very session's
          // meta, server-side (ADR-075 D1.13; browser_ws.go::handleAttach); nothing on the attach frame
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
          const created = await createSession(agentId, workspaceId ?? undefined)
          setActiveSession(created.id, created.agent_id, null)
          const current = useUiStore.getState().activePanel
          if (current !== panelAtCreationStart) return
          leaveGateThen(current?.id ?? null, () => {
            if (useUiStore.getState().activePanel !== current) return
            useUiStore.getState().openPanel('browser', {
              sessionId: created.id,
              agentId: created.agent_id,
            })
          })
        } catch (err) {
          addToast({
            message: err instanceof Error ? err.message : 'Could not start a browser session — try again.',
            variant: 'error',
          })
        } finally {
          setCreatingBrowserSession(false)
        }
      })()
    })
  }

  return (
    <div
      className={cn(
        // Single inline cluster — never wraps; overflow-x-auto scrolls rather
        // than clips on extreme sizes (≤320px).
        'flex items-center gap-[var(--space-1)] min-w-0 overflow-x-auto',
        className,
      )}
      style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as React.CSSProperties}
    >
      {/* New Chat was removed from the header. The composer no longer handles
          /new either (FR-007). A fresh chat is started from the sidebar. */}

      {/* Open browser — ADR-039 D-A1: user-initiated live browser session,
          independent of any agent tool call. */}
      <Button
        type="button"
        variant="ghost"
        onClick={() => void handleOpenBrowser()}
        disabled={creatingBrowserSession}
        // Composer tab ring — full map (single source of truth; other spots
        // point back here): skip-link=1 (AppShell.tsx) → chat input=2
        // (ChatScreen.tsx) → model=4 (composer/ModelPicker.tsx) → attach=5
        // (ChatScreen.tsx) → send=6 (ChatScreen.tsx) → browser=7 (this button),
        // then natural DOM order (the header tab menu). Slot 3 was the agent
        // picker; that control is gone (FR-007) and the remaining indexes are
        // not renumbered. Deliberate positive tabIndex on this closed set.
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
        data-panel-trigger="browser"
        aria-busy={creatingBrowserSession}
        title="Open a live browser session"
        className={cn(
          'shrink-0 px-[var(--space-2)] h-8 gap-[var(--space-1)]',
          'text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)]',
          'text-[length:var(--type-utility-xs-size)] whitespace-nowrap',
          'disabled:cursor-not-allowed',
          'pointer-coarse:min-h-[44px] pointer-coarse:px-[var(--space-2-5)]',
        )}
      >
        {creatingBrowserSession ? <SpinnerGap size={15} className="animate-spin" /> : <Monitor size={15} />}
        <span className="hidden @2xl:inline">Open browser</span>
      </Button>
    </div>
  )
}
