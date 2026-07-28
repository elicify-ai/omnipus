// /browser-live — fullscreen pop-out target for the live interactive browser
// panel (ADR-038 D5). Opened via `window.open('/browser-live?session=..&agent=..')`
// from BrowserLivePanel.tsx's "Pop out" button. Renders the same shared
// BrowserLiveView core used by the Sheet overlay, filling the AppShell
// content area (no separate chrome of its own beyond BrowserLiveView's
// header) — frames are pixels (JPEG), not an embedded copy of the target
// site, so this stays a main-origin route with no isolated preview origin
// needed (see ADR-038 "Security" under D6).
//
// Nested under `/_app` so it reuses the existing onboarding/auth guard
// (`_app.tsx`'s beforeLoad). The browser WS handshake authenticates via the
// same-origin `omnipus-session` HttpOnly cookie (ADR-044), which a
// same-origin `window.open`'d tab inherits automatically — no token hand-off
// is needed.

import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { z } from 'zod'
import { BrowserLiveView } from '@/components/browser/BrowserLiveView'
import { announcePopoutClosed } from '@/lib/browserLiveHandoff'

const browserLiveSearchSchema = z.object({
  session: z.string().min(1).optional(),
  agent: z.string().min(1).optional(),
})

export const Route = createFileRoute('/_app/browser-live')({
  validateSearch: browserLiveSearchSchema,
  component: BrowserLiveRoute,
})

function BrowserLiveRoute() {
  const { session, agent } = Route.useSearch()
  const navigate = useNavigate()

  // BUG 2 fix (live UAT re-run 2026-07-28) — tell the main app's docked
  // panel (BrowserLivePanel.tsx) to re-dock itself for THIS EXACT
  // (session, agent) the moment this pop-out goes away, so "close the
  // pop-out" hands the view back instead of leaving the main app with a bare
  // "Open browser" button (and, if the user then clicks it, whatever agent
  // happens to be globally active in chat — not necessarily the one that
  // actually owns this live session). `pagehide` fires for every teardown
  // path — the in-app Close button's `window.close()` below, a native
  // browser tab-close/Cmd+W, and a manual navigate-away — so this ONE
  // listener covers all of them without depending on the Close button ever
  // being clicked. See browserLiveHandoff.ts's doc comment for why
  // BroadcastChannel (not a direct window reference) is the only signal
  // available between these two `noopener` windows.
  useEffect(() => {
    if (!session || !agent) return undefined
    const handlePageHide = () => announcePopoutClosed(session, agent)
    window.addEventListener('pagehide', handlePageHide)
    return () => window.removeEventListener('pagehide', handlePageHide)
  }, [session, agent])

  if (!session || !agent) {
    return (
      <div className="absolute inset-0 flex items-center justify-center p-6 text-center text-sm text-[var(--color-muted)]">
        Missing session or agent — open this page via the "Watch live" / pop-out action in the app.
      </div>
    )
  }

  return (
    <BrowserLiveView
      key={`${session}:${agent}`}
      sessionId={session}
      agentId={agent}
      onClose={() => {
        // Announced synchronously here too (belt and braces alongside the
        // `pagehide` listener above) so the main app's re-dock isn't left
        // waiting on the browser's teardown-event timing for the ONE path
        // (the in-app Close button) this component controls directly —
        // announcing twice for the same close is harmless, see
        // announcePopoutClosed's own doc comment.
        announcePopoutClosed(session, agent)
        // A script-opened pop-out can close itself; a directly-navigated tab
        // cannot (window.close() is a silent no-op there), so fall back to
        // navigating back into the app.
        window.close()
        navigate({ to: '/' })
      }}
      // canAnnotate deliberately omitted (defaults to false) — this route is
      // a separate `window.open` document with no chat store, so annotate's
      // Send could never reach the chat here (UAT finding FE-4).
      //
      // (The former isPinned / onTogglePin props died with the ADR-040 D4
      // pin/overlay split — the docked panel is now the only in-app layout,
      // so the view has no Pin concept anywhere anymore.)
      //
      // BUG 1 fix: fill the pop-out window instead of shrink-wrapping the
      // media's intrinsic size — see BrowserLiveView's `fillContainer` prop
      // doc comment. Safe to pair with `canAnnotate` omitted here (see that
      // same doc comment for why the two together would need more work).
      fillContainer
      className="absolute inset-0"
    />
  )
}
