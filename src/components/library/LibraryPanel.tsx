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
// C4 UPDATE (library-b-c-design-2026-09-07.md "fullscreen carries the
// selection"): popping out NOW closes this docked panel — `handlePopOut`
// calls `closeLibraryPanel()`. This REVERSES the note that used to stand
// here ("popping out does NOT close the docked panel... keeping both open is
// strictly more useful than forcing a hand-over"). That reasoning is still
// correct as far as it goes — the Library has no BrowserLivePanel-style
// exclusive control lock, and two tabs browsing the same files is still
// harmless — but it answered a different question than the one C4 asks. The
// founder-locked spec's own words: "the new tab starts with the same
// folder/item selected... and the slide-out then closes." With the pop-out
// now carrying the EXACT same selection (see `currentSelectionRef` below),
// leaving the slide-out open beside an identical fullscreen view is
// redundant clutter, not a second useful vantage point — closing it is a
// declutter decision, not a concurrency one, and it does not reintroduce a
// control lock: nothing stops the operator re-opening the docked panel
// (sidebar, or the existing pop-out-closed handoff below) while the
// fullscreen tab stays open.
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

  // C4: the docked LibraryExplorer's CURRENT location — not the
  // `libraryPanel.workspaceId` the store recorded at open time, which goes
  // stale the moment the operator navigates to a different workspace inside
  // the docked panel without closing it (there was no live workspace signal
  // wired here before C4 — `onWorkspaceChange` is new to this file). Kept in
  // refs, not state: this is read exactly once, at pop-out click time, and
  // does not need to trigger a re-render on every keystroke of navigation.
  const currentWorkspaceRef = useRef<string | undefined>(undefined)
  const currentSelectionRef = useRef<{ path: string | null; folder: string }>({ path: null, folder: '' })

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
    // `currentWorkspaceRef`, not `libraryPanel.workspaceId` — see this ref's
    // own doc comment above.
    const workspaceId = currentWorkspaceRef.current
    if (workspaceId) params.set('workspace', workspaceId)
    // C4: carry the CURRENT selection into the new tab. A selected file
    // (`path`) takes priority — it already fully determines its own folder
    // (LibraryAddress/`selectedDir`) — and only when nothing is selected does
    // the browsed folder itself (`folder`) go along, so a plain "I was
    // looking at this folder, nothing open" state still lands in the right
    // place rather than the workspace root.
    const { path, folder } = currentSelectionRef.current
    if (path) {
      params.set('path', path)
    } else if (folder) {
      params.set('folder', folder)
    }
    const qs = params.toString()
    // Hash routing: the route + search MUST live in the `#/` fragment or the
    // router falls back to the default route (same caveat as browser-live).
    window.open(`/#/library${qs ? `?${qs}` : ''}`, '_blank', 'noopener,noreferrer')
    // C4: close the slide-out now that the fullscreen tab shows the same
    // place — see the module doc's "C4 UPDATE" note for why this reverses
    // the prior "never close on pop-out" decision without reintroducing a
    // control lock.
    closeLibraryPanel()
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
        onWorkspaceChange={(id) => {
          currentWorkspaceRef.current = id ?? undefined
        }}
        onSelectionChange={(selection) => {
          currentSelectionRef.current = selection
        }}
      />
    </aside>
  )
}
