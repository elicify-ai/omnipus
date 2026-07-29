// LibraryPanel — app-root wrapper around LibraryExplorer (library-spec.md
// D-4). A SINGLE global instance mounted in AppShell.tsx, mirroring
// BrowserLivePanel.tsx exactly: state lives in the `ui` store
// (`libraryPanel`) so either entry point (D-3 — sidebar virtual root, or a
// future workspace-scoped opener) can open it without prop drilling, and the
// panel survives virtualized-list row remounts elsewhere in the tree.
//
// Open = ALWAYS docked: a plain `<aside>` flex sibling of the main content
// column inside AppShell's outer `flex` row — never a Radix Sheet/modal (that
// variant was retired 2026-07-16 by operator direction; "do not reintroduce
// without an ADR" — see BrowserLivePanel.tsx's identical note). The only
// other layout is the fullscreen `/#/library` pop-out tab (see handlePopOut).
//
// DELIBERATE DIFFERENCE FROM BrowserLivePanel: popping out does NOT close
// this docked panel. BrowserLivePanel closes on pop-out because the live
// browser view has an exclusive, first-come/no-preempt control lock — two
// simultaneous viewers fight over "who's driving." The Library has no such
// lock: browsing (or even editing, once D-5 lands) the same workspace's files
// from two tabs at once is harmless, so keeping both open is strictly more
// useful than forcing a hand-over. The BroadcastChannel handoff
// (libraryHandoff.ts) still exists as a SAFETY NET for the case where the
// user closes the DOCKED panel manually while a pop-out is still open, then
// later closes that pop-out too — without this, that sequence would leave no
// Library surface open anywhere. See libraryHandoff.ts's own doc comment.
import { useEffect } from 'react'
import { useUiStore } from '@/store/ui'
import { onLibraryPopoutClosed } from '@/lib/libraryHandoff'
import { LibraryExplorer } from './LibraryExplorer'

export function LibraryPanel() {
  const libraryPanel = useUiStore((s) => s.libraryPanel)
  const closeLibraryPanel = useUiStore((s) => s.closeLibraryPanel)

  // Re-dock (open) this panel if a Library pop-out announces closing AND
  // nothing is currently docked — guarded on fresh store state at delivery
  // time (not the `libraryPanel` closed over from this render), mirroring
  // BrowserLivePanel's identical guard, so a stale/duplicate broadcast can
  // never clobber a panel the user has since reopened for something else.
  useEffect(() => {
    return onLibraryPopoutClosed((workspaceId) => {
      if (useUiStore.getState().libraryPanel === null) {
        useUiStore.getState().openLibraryPanel(workspaceId)
      }
    })
  }, [])

  if (!libraryPanel) return null

  // Arrow function expression (not a `function` declaration) so TypeScript's
  // control-flow narrowing of `libraryPanel` from the early-return above
  // actually carries into this closure — a hoisted function declaration
  // does NOT inherit that narrowing, since TS must assume it could be
  // invoked independent of the narrowing check's control flow.
  const handlePopOut = () => {
    // Auth is the same-origin `omnipus-session` HttpOnly cookie (ADR-044) —
    // a same-origin window.open'd tab inherits it automatically, no token
    // hand-off needed.
    const params = new URLSearchParams()
    if (libraryPanel.workspaceId) params.set('workspace', libraryPanel.workspaceId)
    const qs = params.toString()
    // Hash routing: the route + search MUST live in the `#/` fragment or the
    // router falls back to the default route (same caveat as browser-live).
    window.open(`/#/library${qs ? `?${qs}` : ''}`, '_blank', 'noopener,noreferrer')
    // Deliberately do NOT call closeLibraryPanel() here — see module doc.
  }

  return (
    <aside
      data-testid="library-panel-docked"
      aria-label="Library panel"
      className="flex h-full w-full min-w-0 sm:w-[45%] sm:min-w-[320px] sm:max-w-[720px] flex-shrink-0 flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-0)]"
    >
      <LibraryExplorer
        // Keys the mount to the initial target so a second "open Library"
        // click with a DIFFERENT initial workspace (e.g. sidebar → virtual
        // root after the panel was already scoped to one workspace) starts
        // fresh navigation state instead of leaving stale path/selection from
        // the previous target.
        key={libraryPanel.workspaceId ?? 'root'}
        initialWorkspaceId={libraryPanel.workspaceId}
        onClose={closeLibraryPanel}
        onPopOut={handlePopOut}
      />
    </aside>
  )
}
