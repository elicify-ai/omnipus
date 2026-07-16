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

import { useUiStore } from '@/store/ui'
import { BrowserLiveView } from './BrowserLiveView'

export function BrowserLivePanel() {
  const browserPanel = useUiStore((s) => s.browserPanel)
  const closeBrowserPanel = useUiStore((s) => s.closeBrowserPanel)

  if (!browserPanel) return null

  // Pop-out opens a fullscreen tab for the SAME session/agent — the one
  // remaining non-docked layout (the operator's "open in new tab" path).
  const handlePopOut = () => {
    // The auth token lives in sessionStorage (per-tab, for XSS hygiene)
    // which window.open'd tabs do NOT inherit — so the pop-out would
    // land on the login screen. Briefly mirror the token into
    // localStorage as a same-origin hand-off; the /browser-live route
    // migrates it back into sessionStorage and purges this copy on
    // mount (see browser-live.tsx).
    const token = sessionStorage.getItem('omnipus_auth_token')
    if (token) localStorage.setItem('omnipus_auth_token', token)
    const params = new URLSearchParams({
      session: browserPanel.sessionId,
      agent: browserPanel.agentId,
    })
    // The SPA uses HASH routing (#/…) — the route + search must live in
    // the fragment, not the path, or the router ignores it and falls to
    // the default route. Must be `/#/browser-live?…`, not `/browser-live?…`.
    window.open(`/#/browser-live?${params.toString()}`, '_blank', 'noopener,noreferrer')
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
