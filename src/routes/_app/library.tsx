// /library — the retained standalone Library page used by bookmarks,
// backlinks, and direct entry points. Side-panel Expand is owned by the
// chrome-less /panel/library route; this page remains under AppShell.
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

import { useEffect, useRef } from 'react'
import { createFileRoute, useBlocker, useNavigate } from '@tanstack/react-router'
import { z } from 'zod'
import { LibraryExplorer } from '@/components/library/LibraryExplorer'
import { confirmDiscardLibraryEdits } from '@/components/library/preview/unsavedGuard'
import { announcePanelTabPresence, type PanelPresenceAnnouncement } from '@/lib/panelTabPresence'
import { generateId } from '@/lib/constants'

const librarySearchSchema = z.object({
  workspace: z.string().min(1).optional(),
  path: z.string().min(1).optional(),
  // C4 (library-b-c-design-2026-09-07.md "fullscreen carries the
  // selection"): the folder the docked panel had open with NOTHING
  // selected, so LibraryPanel's pop-out (which cannot always name a
  // selected FILE) can still land the new tab in the right place. Read only
  // as LibraryExplorer's `address.folder` — a one-time initial seed, never
  // re-emitted by this route's own `onAddressChange` below (LibraryAddress's
  // own doc comment explains why that's safe: `goTo()` never reports it
  // back, so it can't drift out of sync with `path`).
  folder: z.string().min(1).optional(),
  popout: z.string().min(1).optional(),
})

export const Route = createFileRoute('/_app/library')({
  validateSearch: librarySearchSchema,
  component: LibraryRoute,
})

function LibraryRoute() {
  const { workspace, path, folder, popout } = Route.useSearch()
  const navigate = useNavigate()
  const popoutIdRef = useRef(popout ?? generateId())
  const presenceAnnouncementRef = useRef<PanelPresenceAnnouncement | null>(null)

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
    shouldBlockFn: async () => !(await confirmDiscardLibraryEdits()),
    // unsavedGuard.ts registers its own `beforeunload` for tab close/reload;
    // a second one here would be a second native prompt for one event.
    enableBeforeUnload: false,
  })

  useEffect(() => {
    const announcement = announcePanelTabPresence({ panelId: 'library', workspaceId: workspace })
    presenceAnnouncementRef.current = announcement
    return () => {
      if (presenceAnnouncementRef.current === announcement) presenceAnnouncementRef.current = null
      announcement.stop()
    }
  }, [])

  return (
    <LibraryExplorer
      // No `key` here, deliberately. It used to be `key={workspace ?? 'root'}`
      // — a remount to re-seed `initialWorkspaceId` whenever the param
      // changed. With the address controlled, a param change IS the state
      // change, and remounting on every navigation would throw away the
      // browsed folder, the loaded listing and the open preview each time.
      address={{ workspaceId: workspace, path, folder }}
      onAddressChange={(next) => {
        // Pushed, not replaced: each selected file is a place the back button
        // should return to (US-3 AS-4).
        void navigate({
          to: '/library',
          search: { workspace: next.workspaceId, path: next.path, popout: popoutIdRef.current },
        })
      }}
      // Side-by-side here, stacked in the docked aside (operator direction,
      // 2026-08-04). A standalone tab has the width for a real split, and 60%
      // of it beats a half-height strip for reading and editing a file; the
      // narrow docked aside would be unusable cut in two.
      layout="split"
      onWorkspaceChange={(id) => {
        presenceAnnouncementRef.current?.update({ panelId: 'library', workspaceId: id ?? undefined })
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
