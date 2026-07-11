// BrowserLivePanel — app-root overlay wrapper around BrowserLiveView
// (ADR-038 D5). A SINGLE global instance mounted in AppShell.tsx, mirroring
// MediaLightbox.tsx: state lives in the `ui` store (`browserPanel`) so any
// "Watch live" affordance anywhere in the tree can open it without prop
// drilling, and the Sheet survives virtualized-list row remounts.
//
// Right-side overlay with `overlay={false}` (chat stays visible behind it),
// mirroring ActivityPanel.tsx's Sheet usage.

import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { useUiStore } from '@/store/ui'
import { BrowserLiveView } from './BrowserLiveView'

export function BrowserLivePanel() {
  const browserPanel = useUiStore((s) => s.browserPanel)
  const closeBrowserPanel = useUiStore((s) => s.closeBrowserPanel)

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
    <Sheet open={browserPanel !== null} onOpenChange={(open) => !open && closeBrowserPanel()} modal={false}>
      {browserPanel && (
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
          <BrowserLiveView
            // Keys the mount to the (session, agent) pair so switching targets
            // while the panel is already open (a second "Watch live" click)
            // tears down the old WS connection and starts a fresh one, rather
            // than leaving BrowserLiveView's internal WS effect pinned to the
            // props it captured on first mount.
            key={`${browserPanel.sessionId}:${browserPanel.agentId}`}
            sessionId={browserPanel.sessionId}
            agentId={browserPanel.agentId}
            onClose={closeBrowserPanel}
            // UAT finding FE-4: annotate needs the chat (submitAnnotation
            // sends through useChatStore), which only this docked panel
            // shares a JS realm with — the fullscreen pop-out route
            // (browser-live.tsx) omits this so its "Annotate" button doesn't
            // render at all, mirroring the onHandToAgent pattern below
            // rather than rendering a control that dead-ends there.
            canAnnotate
            // Only the docked panel (this component) shares a JS realm with
            // ChatScreen/the composer-prefill bridge — the fullscreen
            // pop-out route (browser-live.tsx) deliberately omits this prop
            // so its "Hand to agent" button doesn't render at all (it would
            // otherwise silently no-op: writing composerPrefill from a
            // separate `window.open` document is invisible to the original
            // tab's chat). See BrowserLiveView's onHandToAgent doc comment.
            onHandToAgent={() => {
              // UAT finding FE-2: the original "Continue from the current
              // page: " hint was too weak — the agent would reply "I don't
              // have a browser page open" instead of actually reaching for
              // its browser tools, even though the shared tab genuinely was
              // still there. Spelling out WHICH tools to use first fixes it.
              useUiStore
                .getState()
                .setComposerPrefill(
                  "I've left a page open in your live browser session. Use your browser tools (take a screenshot and/or read the page text) to see what's currently loaded, then continue from there: ",
                )
              useUiStore.getState().addToast({
                message: 'Control released — a hint was added to the chat composer.',
                variant: 'default',
              })
              // UAT finding FE-3(a): auto-close so focus lands back on the
              // now-usable, now-prefilled composer instead of leaving the
              // user to discover they must close this panel by hand first.
              closeBrowserPanel()
            }}
            onPopOut={() => {
              // The auth token lives in sessionStorage (per-tab, for XSS
              // hygiene) which window.open'd tabs do NOT inherit — so the
              // pop-out would land on the login screen. Briefly mirror the
              // token into localStorage as a same-origin hand-off; the
              // /browser-live route migrates it back into sessionStorage and
              // purges this copy on mount (see browser-live.tsx).
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
            }}
          />
        </SheetContent>
      )}
    </Sheet>
  )
}
