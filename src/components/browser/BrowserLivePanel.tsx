// BrowserLivePanel — app-root wrapper around BrowserLiveView (ADR-038 D5),
// redesigned per ADR-040 D4 ("Take the wheel" — Pin / side-by-side). A
// SINGLE global instance mounted in AppShell.tsx, mirroring MediaLightbox.tsx:
// state lives in the `ui` store (`browserPanel`) so any "Watch live"
// affordance anywhere in the tree can open it without prop drilling, and the
// panel survives virtualized-list row remounts.
//
// Two layouts, switched by the `ui` store's `browserPanelPinned` (the header
// 📌 Pin toggle lives inside BrowserLiveView — the OTHER owner in the
// ADR-040 seam — but the backing boolean + AppShell layout are this file's
// job):
//   - Unpinned (default) — the right-side non-modal `Sheet` overlay, chat
//     stays visible + clickable behind it. UNCHANGED from ADR-038/039.
//   - Pinned — a plain docked flex column, NOT a Sheet. This component's own
//     root element (the <aside> below) becomes a normal flex sibling of the
//     chat region inside AppShell's outer `flex` row (`data-app-shell` in
//     AppShell.tsx) — no extra wrapper needed there, the same mechanism
//     Sidebar.tsx's pinned `<aside>` already uses. The chat region's existing
//     `flex-1 min-w-0` (AppShell.tsx) shrinks automatically to share width
//     with this column; the Sheet branch, by contrast, portals its content
//     out of `data-app-shell`'s flow entirely (Radix's Dialog.Portal), so it
//     never participated in the flex row to begin with.
//
// CAVEAT — BrowserLiveView's WS connection is torn down and restarted in TWO
// cases, both deliberate or deliberately-accepted, not one:
//   1. The (session, agent) pair changes while the panel is open — the
//      `key={...}` below (see its own comment further down) intentionally
//      remounts BrowserLiveView so a second "Watch live" click tears down
//      the stale WS and starts a fresh one for the new target.
//   2. The pin toggle flips WHILE a session is open — the Sheet branch and
//      the docked branch below are different root element TYPES (Radix's
//      Dialog/Portal vs. a plain <aside>), both returned from THIS
//      component's own conditional render (not a fixed position inside
//      AppShell's children — AppShell just mounts <BrowserLivePanel/> once
//      and this component alone decides which branch to render). React
//      always fully unmounts + remounts a subtree when the root element
//      type changes, regardless of any inner element's `key` — `key` only
//      preserves identity across siblings of the SAME element type, not
//      across two structurally different trees. So toggling 📌 re-runs
//      BrowserLiveView's WS connect/detach lifecycle effect (a fresh
//      `browser_attach`) even though neither the session nor the agent
//      changed. ADR-040 D4 accepts this explicitly ("preserve … where
//      practical") rather than forcing a portal-based identity hack into
//      Radix's Sheet/Dialog machinery.
// Only when NEITHER the (session, agent) pair NOR the pin state changes does
// the WS connection survive a re-render.

import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { useUiStore } from '@/store/ui'
import { BrowserLiveView } from './BrowserLiveView'

export function BrowserLivePanel() {
  const browserPanel = useUiStore((s) => s.browserPanel)
  const closeBrowserPanel = useUiStore((s) => s.closeBrowserPanel)
  const browserPanelPinned = useUiStore((s) => s.browserPanelPinned)
  const toggleBrowserPanelPinned = useUiStore((s) => s.toggleBrowserPanelPinned)

  // Pop-out opens a fullscreen tab for the SAME session/agent — redundant
  // (and visually confusing) once the panel is already docked side-by-side
  // with the chat, so it's conditionally omitted below when pinned.
  const handlePopOut = browserPanel
    ? () => {
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
    : undefined

  // Shared between both layouts below — same (session, agent)-keyed mount,
  // same capability props, regardless of pin state (except `onPopOut` —
  // see below).
  const view = browserPanel && (
    <BrowserLiveView
      // Keys the mount to the (session, agent) pair so switching targets
      // while the panel is already open (a second "Watch live" click) tears
      // down the old WS connection and starts a fresh one, rather than
      // leaving BrowserLiveView's internal WS effect pinned to the props it
      // captured on first mount. Deliberately NOT also keyed by pin state —
      // see the module-level caveat above for why toggling pin remounts
      // anyway despite that.
      key={`${browserPanel.sessionId}:${browserPanel.agentId}`}
      sessionId={browserPanel.sessionId}
      agentId={browserPanel.agentId}
      onClose={closeBrowserPanel}
      // UAT finding FE-4 (ADR-039): annotate needs the chat (submitAnnotation
      // sends through useChatStore), which only this panel (docked or
      // overlay) shares a JS realm with — the fullscreen pop-out route
      // (browser-live.tsx) omits this so its "Annotate" button doesn't
      // render at all, mirroring the isPinned/onTogglePin omission there
      // rather than rendering a control that dead-ends.
      canAnnotate
      // ADR-040 D4 seam: this component owns the Pin control's backing
      // state and passes it through, plus the toggle. `isPinned`/
      // `onTogglePin` are display-only for the view either way, but
      // `onPopOut` below is CONDITIONALLY omitted while pinned — the view
      // gates its own Pop-out affordance purely on `onPopOut`'s presence
      // (see BrowserLiveView.tsx), so passing a handler here regardless of
      // pin state would render a Pop-out button that makes no sense from
      // inside an already-docked panel.
      isPinned={browserPanelPinned}
      onTogglePin={toggleBrowserPanelPinned}
      {...(browserPanelPinned ? {} : { onPopOut: handlePopOut })}
    />
  )

  // Pinned + open: dock as a normal flex column (ADR-040 D4) — see the
  // module doc comment above for how this participates in AppShell's flex
  // row with zero AppShell-side wiring, and the remount caveat.
  if (browserPanel && browserPanelPinned) {
    return (
      <aside
        data-testid="browser-live-panel-docked"
        aria-label="Live browser panel"
        className="flex h-full w-[45%] min-w-[320px] max-w-[720px] flex-shrink-0 flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-1)]"
      >
        {view}
      </aside>
    )
  }

  return (
    // UAT finding FE-3(b): this Sheet used Radix's default modal=true, which
    // sets `body{pointer-events:none}` while open — the chat pane behind it
    // (rendered visible via overlay={false} below) LOOKED usable but was
    // 100% unclickable, and after "Hand to agent" the user had no way to
    // reach the now-prefilled composer's Send button without first closing
    // this panel. modal={false} lifts that CSS lockout so watching-while-
    // chatting works as the ADR-038 design intends. Radix's non-modal
    // Dialog.Content still dismisses on ANY outside pointerdown/focus by
    // default though (see @radix-ui/react-dialog's DialogContentNonModal —
    // it forwards to onInteractOutside and only preventDefault()s for
    // clicks on its own trigger) — without onInteractOutside below, the
    // very first click on the chat composer would immediately close this
    // panel, defeating the point. Escape (onEscapeKeyDown, untouched) and
    // the built-in X button (DialogPrimitive.Close, untouched) still close
    // it explicitly.
    //
    // `!browserPanelPinned` is folded into both `open` and the content gate
    // below so this branch fully steps aside once pinned — the docked
    // branch above takes over instead of the two ever rendering at once.
    // When browserPanelPinned is false (the default, and the ONLY state this
    // branch ever runs in the common case) both conditions reduce to exactly
    // the original ADR-038/039 conditions — unpinned behaviour is unchanged.
    <Sheet
      open={browserPanel !== null && !browserPanelPinned}
      onOpenChange={(open) => !open && closeBrowserPanel()}
      modal={false}
    >
      {browserPanel && !browserPanelPinned && (
        <SheetContent
          side="right"
          overlay={false}
          onInteractOutside={(e) => e.preventDefault()}
          // The shared SheetContent hardcodes aria-modal="true" (correct for
          // every OTHER Sheet consumer, which are all still modal={true}) —
          // {...props} spreads after that hardcoded attribute in sheet.tsx,
          // so this override lands only here, matching the modal={false}
          // set on the Root above without touching the shared component.
          aria-modal={false}
          className="w-[70vw] max-w-[56rem] p-0 flex flex-col"
        >
          {/* Radix requires a DialogTitle descendant for a11y (screen-reader
              announcement on open). BrowserLiveView already renders a visible
              "Live Browser" <h2> in its own header — this sr-only SheetTitle
              supplies the accessible name without duplicating that heading
              visually. */}
          <SheetTitle className="sr-only">Live Browser</SheetTitle>
          {/* UAT finding FE-9: Radix warns "Missing Description or
              aria-describedby for DialogContent" without this — a11y only,
              never rendered visually. */}
          <SheetDescription className="sr-only">
            Real-time view of the agent&apos;s live browser session. Take control to drive it yourself, or
            annotate a region to discuss it with the agent.
          </SheetDescription>
          {view}
        </SheetContent>
      )}
    </Sheet>
  )
}
