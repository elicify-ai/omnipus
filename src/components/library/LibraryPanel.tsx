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
// useful than forcing a hand-over.
//
// UAT fix (Dana, re-verified v8 — "pop-out re-dock STILL does not restore
// the workspace"): the ORIGINAL version of this re-dock reaction only ever
// fired when NOTHING was currently docked (a pure safety net). Dana's exact
// repro — pop out from "My Workspace" (docked panel stays OPEN the whole
// time), navigate the pop-out to "Dana Workspace B", close it — never hit
// that branch at all: the docked panel was never null, so the broadcast was
// treated as a no-op by design, regardless of whether the message plumbing
// itself worked. That guard, not the BroadcastChannel wiring, was the actual
// bug. This now applies the pop-out's last-known workspace UNCONDITIONALLY
// on close — updating an already-docked panel's workspace, not only
// re-opening a closed one — since the docked and popped-out surfaces are the
// SAME Library, and the user's last action (navigate in the pop-out, then
// close it) is what should be reflected back rather than silently discarded.
//
// `lastKnownPopoutWorkspaceRef` is fed CONTINUOUSLY by
// `onLibraryWorkspaceChanged` (every in-tab navigation in the pop-out, not
// only at teardown — see libraryHandoff.ts's module doc for why relying on
// a single message posted at `pagehide` is unreliable: BroadcastChannel
// delivery during unload is asynchronous and may never arrive). By the time
// `popout-closed` fires, the latest workspace is almost always already
// known from that continuous stream; the `workspaceId` `popout-closed`
// itself carries is only a fallback for the (rare, and now much smaller)
// window where no continuous update was ever received.
import { useEffect, useRef } from 'react'
import { useUiStore } from '@/store/ui'
import { onLibraryPopoutClosed, onLibraryWorkspaceChanged } from '@/lib/libraryHandoff'
import { LibraryExplorer } from './LibraryExplorer'

export function LibraryPanel() {
  const libraryPanel = useUiStore((s) => s.libraryPanel)
  const closeLibraryPanel = useUiStore((s) => s.closeLibraryPanel)
  // `set: false` until the FIRST continuous broadcast arrives, so a
  // `popout-closed` that beats every `workspace-changed` message (e.g. the
  // pop-out closed before this listener ever mounted) correctly falls back
  // to the `popout-closed` payload instead of an undefined "known" value
  // that would look identical to "the pop-out is at the virtual root".
  const lastKnownPopoutWorkspaceRef = useRef<{ set: boolean; workspaceId?: string }>({ set: false })

  useEffect(() => {
    return onLibraryWorkspaceChanged((workspaceId) => {
      lastKnownPopoutWorkspaceRef.current = { set: true, workspaceId }
    })
  }, [])

  // Re-target (or re-open) the docked panel to wherever the pop-out was last
  // viewing, once it closes — see the module doc above for why this is now
  // unconditional rather than gated on "nothing currently docked".
  useEffect(() => {
    return onLibraryPopoutClosed((workspaceId) => {
      const known = lastKnownPopoutWorkspaceRef.current
      useUiStore.getState().openLibraryPanel(known.set ? known.workspaceId : workspaceId)
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
