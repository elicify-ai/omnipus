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

import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { z } from 'zod'
import { BrowserLiveView } from '@/components/browser/BrowserLiveView'

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
      // isPinned / onTogglePin (ADR-040 D4) are likewise deliberately
      // omitted — there is no Pin concept in the fullscreen pop-out (it's
      // already its own separate window; "dock beside the chat" doesn't
      // apply here), so the view's header renders no 📌 button at all in
      // this route. Same dead-end-proof-by-omission reasoning as canAnnotate
      // above — omitting the prop hides the control rather than wiring it to
      // a no-op.
      className="absolute inset-0"
    />
  )
}
