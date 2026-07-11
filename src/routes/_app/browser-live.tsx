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
// (`_app.tsx`'s beforeLoad) — the browser WS handshake needs a valid bearer
// token exactly like every other authenticated surface in the SPA.

import { useEffect } from 'react'
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

  // Consume the pop-out auth hand-off: the opener staged the bearer token in
  // localStorage because window.open'd tabs don't inherit sessionStorage (where
  // the token normally lives). Migrate it into this tab's sessionStorage and
  // purge the shared localStorage copy so the token doesn't persist there.
  // (The _app beforeLoad guard already accepted the localStorage fallback, so
  // this only cleans up.)
  useEffect(() => {
    const staged = localStorage.getItem('omnipus_auth_token')
    if (staged) {
      if (!sessionStorage.getItem('omnipus_auth_token')) {
        sessionStorage.setItem('omnipus_auth_token', staged)
      }
      localStorage.removeItem('omnipus_auth_token')
    }
  }, [])

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
      // Send could never reach the chat here (UAT finding FE-4). Same
      // reasoning as the already-omitted onHandToAgent prop above.
      className="absolute inset-0"
    />
  )
}
