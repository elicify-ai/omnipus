// BrowserLivePanel — app-root wrapper around BrowserLiveView (ADR-038 D5).
// A SINGLE global instance mounted in AppShell.tsx, mirroring
// MediaLightbox.tsx: state lives in the `ui` store (`browserPanel`) so any
// "Watch live" affordance anywhere in the tree can open it without prop
// drilling, and the panel survives virtualized-list row remounts.
//
// Open = ALWAYS docked (operator direction, 2026-07-16 — amends ADR-040 D4):
// the ADR-040 pin/overlay split (unpinned right-side Sheet overlay + a 📌
// toggle to dock) is retired. This component's root <aside> is a plain
// docked flex column — a normal flex sibling of the chat region inside
// AppShell's outer `flex` row (`data-app-shell`), the same mechanism
// Sidebar.tsx's pinned <aside> uses. The chat region's `flex-1 min-w-0`
// (AppShell.tsx) shrinks automatically to share width. The only other
// layout is the fullscreen /#/browser-live pop-out tab (see onPopOut).
// Do NOT reintroduce a Sheet/overlay branch here without an ADR.
//
// CAVEAT — BrowserLiveView's WS connection is torn down and restarted when
// the (session, agent) pair changes while the panel is open: the `key={...}`
// below intentionally remounts BrowserLiveView so a second "Watch live"
// click tears down the stale WS and starts a fresh one for the new target.
// (The former second remount trigger — the pin toggle flipping the root
// element type — died with the pin.)
//
// BUG 2 fix (live UAT re-run) — popping out closes this docked panel (see
// handlePopOut below), but closing the pop-out now RE-opens it for the exact
// same (session, agent) via a same-origin BroadcastChannel signal
// (browserLiveHandoff.ts) — the pop-out and this panel are separate
// `window.open` documents with no direct JS reference to each other
// (`noopener`), so that channel is the only way either side learns the
// other's lifecycle. Previously nothing restored this panel at all: closing
// the pop-out left the user with a bare "Open browser" button, and that
// button (ChatControls.tsx) opens against whatever agent/session is
// globally active in chat, not necessarily the one the pop-out was actually
// showing.

import { useEffect } from 'react'
import { useUiStore } from '@/store/ui'
import { onPopoutClosed } from '@/lib/browserLiveHandoff'
import { BrowserLiveView } from './BrowserLiveView'

export function BrowserLivePanel() {
  const browserPanel = useUiStore((s) => s.browserPanel)
  const closeBrowserPanel = useUiStore((s) => s.closeBrowserPanel)

  // BUG 2 fix (live UAT re-run) — re-dock this panel for the EXACT
  // (sessionId, agentId) a pop-out was showing the instant that pop-out
  // closes (see browserLiveHandoff.ts's doc comment for the full mechanism
  // and its known cross-tab scoping limit). Subscribed unconditionally,
  // BEFORE the `!browserPanel` early return below, so a restore can land
  // even on a render where this component is currently returning null.
  // Guarded on `browserPanel` still being null AT DELIVERY TIME (read fresh
  // from the store inside the callback, not the `browserPanel` closed over
  // from this render) so a stale/duplicate broadcast never clobbers newer
  // state — e.g. the user already re-opened a DIFFERENT session's panel by
  // the time this arrives.
  useEffect(() => {
    return onPopoutClosed((sessionId, agentId) => {
      if (useUiStore.getState().browserPanel === null) {
        useUiStore.getState().openBrowserPanel(sessionId, agentId)
      }
    })
  }, [])

  if (!browserPanel) return null

  // Pop-out opens a fullscreen tab for the SAME session/agent — the one
  // remaining non-docked layout (the operator's "open in new tab" path).
  const handlePopOut = () => {
    // Auth is a same-origin `omnipus-session` HttpOnly cookie (ADR-044) —
    // the window.open'd tab is same-origin, so the browser attaches the
    // cookie to its requests and the WS handshake automatically. No
    // token hand-off is needed.
    const params = new URLSearchParams({
      session: browserPanel.sessionId,
      agent: browserPanel.agentId,
    })
    // The SPA uses HASH routing (#/…) — the route + search must live in
    // the fragment, not the path, or the router ignores it and falls to
    // the default route. Must be `/#/browser-live?…`, not `/browser-live?…`.
    window.open(`/#/browser-live?${params.toString()}`, '_blank', 'noopener,noreferrer')
    // UAT 2026-07-18 ("fullscreen inputs don't work"): the pop-out is a
    // SECOND viewer connection, and the control lock is first-come/no-preempt
    // (live.go TakeControl, ADR-038 D6). Leaving the docked panel mounted
    // kept its WS+PC alive — and if it held (or later took) the drive lock,
    // the pop-out was permanently "Someone else is driving": every
    // pointer/keyboard event bailed out (BrowserLiveView handlePointerDown's
    // other-driving guard + takeWheelIfNeeded's controlledByOtherRef guard).
    // Popping out therefore HANDS OVER the view: close the docked panel,
    // whose unmount cleanup detaches its viewer — and the server releases
    // the lock on detach (LiveViewRegistry.Detach: "releases control if
    // viewerID currently holds it") — so the pop-out can take the wheel.
    // (window.open with 'noopener' returns null by spec, so success can't be
    // checked here; a user-gesture open is not popup-blocked in practice,
    // and re-opening the panel is one "Watch live" click if it ever is.)
    closeBrowserPanel()
    // a11y fix-wave (F1): closeBrowserPanel() unmounts this docked <aside>,
    // which drops keyboard focus to <body> — stranding keyboard/SR users
    // with no indication of where focus went. Restore it to the chat
    // composer, the page's "home position" (see ChatScreen.tsx's own
    // focus-discipline comment on chatInputRef) — the one landmark
    // guaranteed to be mounted alongside this panel (BrowserLivePanel is
    // always docked beside the chat region, per this file's module doc).
    // A plain DOM query (not a ref/context) is deliberate: this is a single
    // global instance mounted outside ChatScreen's own component tree, so it
    // has no direct handle on ChatScreen's internal chatInputRef — querying
    // by the SAME `data-testid="chat-input"` that ref is threaded to is the
    // lightest coupling available. Optional chaining makes this a graceful
    // no-op if the composer isn't mounted (e.g. a future host without a chat
    // screen), rather than throwing.
    document.querySelector<HTMLTextAreaElement>('[data-testid="chat-input"]')?.focus()
  }

  return (
    // Docked flex column (see module doc). Width: phone-narrow viewports get
    // the full row (the chat's flex-1 min-w-0 collapses — a fullscreen
    // takeover, closable via the header X); from `sm` up it's a true
    // side-by-side split.
    <aside
      data-testid="browser-live-panel-docked"
      aria-label="Live browser panel"
      className="flex h-full w-full min-w-0 sm:w-[45%] sm:min-w-[320px] sm:max-w-[720px] flex-shrink-0 flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-0)]"
    >
      <BrowserLiveView
        // Keys the mount to the (session, agent) pair so switching targets
        // while the panel is already open (a second "Watch live" click) tears
        // down the old WS connection and starts a fresh one, rather than
        // leaving BrowserLiveView's internal WS effect pinned to the props it
        // captured on first mount.
        key={`${browserPanel.sessionId}:${browserPanel.agentId}`}
        sessionId={browserPanel.sessionId}
        agentId={browserPanel.agentId}
        onClose={closeBrowserPanel}
        // UAT finding FE-4 (ADR-039): annotate needs the chat (submitAnnotation
        // sends through useChatStore), which only this docked panel shares a
        // JS realm with — the fullscreen pop-out route (browser-live.tsx)
        // omits this so its "Annotate" button doesn't render at all.
        canAnnotate
        onPopOut={handlePopOut}
      />
    </aside>
  )
}
