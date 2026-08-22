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
// Search params (ADR-067 FR-012 — deep-linking): `workspace` scopes the tab
// to one workspace (omitted → the virtual root, which mirrors
// LibraryExplorer's own contract exactly: undefined means "start at the
// virtual root", not an error state, unlike /browser-live's session/agent
// params which ARE required), and `path` names the SELECTED FILE inside it.
// Together they are LibraryExplorer's `address`, and this route is the thing
// that turns that address into a URL and back. That makes the selected file
// bookmarkable, shareable and reachable by the back button — and it is the
// same mechanism later waves point wikilink clicks, search results, backlinks
// and agent-supplied links at, so those need no navigation of their own.
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
//
// IMPORTANT — deep-linking (2026-08) changed the FIRST half of the v1 note — the
// `workspace` param is now written on every in-tab workspace change, so it
// is no longer merely what the tab was opened with. It did NOT change what
// v1 and v2 fixed, and must not: the pop-out still announces its workspace
// from `currentWorkspaceRef` (fed by `onWorkspaceChange`) and still
// announces CONTINUOUSLY, never from the search param and never only at
// `pagehide`. Reading the announcement off the URL instead would reintroduce
// exactly the v2 failure — the param is written by a router navigation that
// settles a tick later than the navigation itself, and at `pagehide` there
// is no later tick.

import { useEffect, useRef } from 'react'
import { createFileRoute, useBlocker, useNavigate } from '@tanstack/react-router'
import { z } from 'zod'
import { LibraryExplorer } from '@/components/library/LibraryExplorer'
import { confirmDiscardLibraryEdits } from '@/components/library/preview/unsavedGuard'
import { announceLibraryPopoutClosed, announceLibraryWorkspaceChanged } from '@/lib/libraryHandoff'

const librarySearchSchema = z.object({
  workspace: z.string().min(1).optional(),
  path: z.string().min(1).optional(),
})

export const Route = createFileRoute('/_app/library')({
  validateSearch: librarySearchSchema,
  component: LibraryRoute,
})

function LibraryRoute() {
  const { workspace, path } = Route.useSearch()
  const navigate = useNavigate()
  const currentWorkspaceRef = useRef<string | undefined>(workspace)

  // The unsaved-edits guard, extended to the one navigation LibraryExplorer's
  // own handlers cannot see: the browser's back/forward buttons. In-app
  // clicks still call confirmDiscardLibraryEdits() inside the explorer, and
  // that call CLEARS the dirty flag when the user agrees to discard — so by
  // the time the resulting navigation reaches this blocker there is nothing
  // left to prompt about, and the operator is never asked twice for one
  // action. Reacting after the fact instead (an effect watching the search
  // params) could not work: React has already unmounted the editor by then,
  // and the editor clears the dirty flag as it goes, so the guard would find
  // nothing unsaved every time.
  useBlocker({
    shouldBlockFn: () => !confirmDiscardLibraryEdits(),
    // unsavedGuard.ts registers its own `beforeunload` for tab close/reload;
    // a second one here would be a second native prompt for one event.
    enableBeforeUnload: false,
  })

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
      // No `key` here, deliberately. It used to be `key={workspace ?? 'root'}`
      // — a remount to re-seed `initialWorkspaceId` whenever the param
      // changed. With the address controlled, a param change IS the state
      // change, and remounting on every navigation would throw away the
      // browsed folder, the loaded listing and the open preview each time.
      address={{ workspaceId: workspace, path }}
      onAddressChange={(next) => {
        // Pushed, not replaced: each selected file is a place the back button
        // should return to (US-3 AS-4).
        void navigate({
          to: '/library',
          search: { workspace: next.workspaceId, path: next.path },
        })
      }}
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
