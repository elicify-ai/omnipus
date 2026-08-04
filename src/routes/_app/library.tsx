// /library — fullscreen pop-out target for the Library panel (library-spec.md
// D-4). Opened via `window.open('/#/library?workspace=..')` from
// LibraryPanel.tsx's pop-out button. Renders the same LibraryExplorer core
// the docked panel uses, filling the AppShell content area.
//
// Nested under `/_app` so it reuses the existing onboarding/auth guard
// (`_app.tsx`'s beforeLoad). Auth rides the same-origin `omnipus-session`
// HttpOnly cookie (ADR-044), which a same-origin `window.open`'d tab inherits
// automatically — no token hand-off needed.
//
// `workspace` omitted → the virtual root (mirrors LibraryExplorer's own
// `initialWorkspaceId` contract exactly — undefined means "start at the
// virtual root", not an error state, unlike /browser-live's session/agent
// params which ARE required).
//
// UAT fix (2026-07, v1): the `workspace` search param only reflects what this
// TAB WAS OPENED WITH — LibraryExplorer manages in-tab navigation (drilling
// into a different workspace from the virtual root, or switching workspaces
// some other way) as its own internal state, never synced back to the URL.
// A `handlePageHide` closed over the initial `workspace` value would
// therefore announce a STALE workspace once the user had navigated
// elsewhere before closing the tab, and the docked panel would re-dock to
// the wrong place. `currentWorkspaceRef` — kept live via LibraryExplorer's
// `onWorkspaceChange` — is what's actually announced, so the re-dock always
// lands on whatever was on screen at close time.
//
// UAT fix (2026-07-30, v2 — that first fix did not actually work): Dana's
// re-verification found the docked panel still didn't update, because (a)
// the DOCKED side ignored the announcement whenever it was already open
// (fixed in LibraryPanel.tsx — see its doc comment), and (b) `pagehide` +
// BroadcastChannel is not a reliable delivery moment (a message posted
// during unload may never arrive). This route now ALSO calls
// `announceLibraryWorkspaceChanged` on every `onWorkspaceChange` — i.e.
// continuously, the moment navigation happens, not only at teardown — so
// the docked side already knows the latest workspace well before this tab
// ever closes. The `pagehide` → `announceLibraryPopoutClosed` call stays as
// the trigger signal (and a same-payload fallback), but is no longer the
// only carrier of the workspace itself.

import { useEffect, useRef } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { LibraryExplorer } from '@/components/library/LibraryExplorer'
import { announceLibraryPopoutClosed, announceLibraryWorkspaceChanged } from '@/lib/libraryHandoff'

const librarySearchSchema = z.object({
  workspace: z.string().min(1).optional(),
})

export const Route = createFileRoute('/_app/library')({
  validateSearch: librarySearchSchema,
  component: LibraryRoute,
})

function LibraryRoute() {
  const { workspace } = Route.useSearch()
  const currentWorkspaceRef = useRef<string | undefined>(workspace)

  // Tell the main app's docked panel (LibraryPanel.tsx) this pop-out went
  // away, so it can re-dock itself IF nothing is currently docked (see
  // libraryHandoff.ts's doc comment — this is a safety net, not a hand-over:
  // unlike /browser-live, the docked panel is never force-closed just
  // because this pop-out opened). `pagehide` fires for every teardown path
  // (native tab-close/Cmd+W, navigate-away, or this route's own unmount),
  // so this one listener covers all of them. Registered once (not keyed on
  // `workspace`) since it always reads the live ref at call time rather than
  // closing over a point-in-time value.
  useEffect(() => {
    const handlePageHide = () => announceLibraryPopoutClosed(currentWorkspaceRef.current)
    window.addEventListener('pagehide', handlePageHide)
    return () => window.removeEventListener('pagehide', handlePageHide)
  }, [])

  return (
    <LibraryExplorer
      key={workspace ?? 'root'}
      initialWorkspaceId={workspace}
      // Side-by-side here, stacked in the docked aside (operator direction,
      // 2026-08-04). A standalone tab has the width for a real split, and 60%
      // of it beats a half-height strip for reading and editing a file; the
      // narrow docked aside would be unusable cut in two.
      layout="split"
      onWorkspaceChange={(id) => {
        currentWorkspaceRef.current = id ?? undefined
        announceLibraryWorkspaceChanged(id ?? undefined)
      }}
      // onClose omitted: closing "the Library" from a standalone tab means
      // closing the tab itself, not returning to some other in-app view —
      // there's no Close-button affordance that makes sense here (mirrors
      // /browser-live's route only rendering a Close button because it
      // deliberately hands control back; the Library route has nothing
      // analogous to hand back).
      className="absolute inset-0"
    />
  )
}
