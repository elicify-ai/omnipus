// LibraryPanel — Library content hosted by SidePanelShell (library-spec.md
// D-4). The global ui store still owns the active-panel context; the shell
// now owns the shared landmark, width, title, Expand and Close controls.
//
// Open = always docked inside the shared shell — never a Sheet/modal. The
// only other layout is the fullscreen `/#/library` tab (handlePopOut).
//
// C4 UPDATE (library-b-c-design-2026-09-07.md "fullscreen carries the
// selection"): Expand registers `handlePopOut` with SidePanelShell; after
// it opens successfully, the shell closes the docked panel. This REVERSES
// the note that used to stand
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
// control lock. Re-invoking the same identity focuses the existing full-page
// tab instead of opening a duplicate.
//
// UAT fix (Dana, re-verified v8 — "pop-out re-dock STILL does not restore
// the workspace"): the ORIGINAL version of this re-dock reaction only ever
// fired when NOTHING was currently docked (a pure safety net). Dana's exact
// repro — pop out from "My Workspace" (docked panel stays OPEN the whole
// time), navigate the pop-out to "Dana Workspace B", close it — never hit
// that branch at all: the docked panel was never null, so the broadcast was
// treated as a no-op by design, regardless of whether the message plumbing
// itself worked. That guard, not the BroadcastChannel wiring, was the actual
// bug. Wave 1 preserves that follow-to-last-workspace behavior only in the
// opener tab: the held Window handle proves ownership, the Library leave
// guard protects a same-panel re-target, and a different open panel wins.
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
import { useCallback, useEffect, useRef } from 'react'
import { useUiStore } from '@/store/ui'
import { onLibraryPopoutClosed, onLibraryWorkspaceChanged } from '@/lib/libraryHandoff'
import { panelIdentityKey, resolvePanelOpen } from '@/lib/panelTabPresence'
import { watchPopoutClosed } from '@/lib/browserLiveHandoff'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import type { PanelContentProps } from '@/components/panel-shell/types'
import { LibraryExplorer } from './LibraryExplorer'

type OwnedLibraryPopout = {
  window: Window
  identityKey: string
  stop: () => void
}

const libraryPopoutHandles = new Map<string, Window>()

export interface LibraryPanelProps {
  shellProps?: PanelContentProps
}

export function LibraryPanel({ shellProps }: LibraryPanelProps = {}) {
  const activePanel = useUiStore((s) => s.activePanel)
  const ownedPopoutRef = useRef<OwnedLibraryPopout | null>(null)
  const presenceRef = useRef<Array<{ panelId: string; workspaceId?: string }>>([])
  // `set: false` until the FIRST continuous broadcast arrives, so a
  // `popout-closed` that beats every `workspace-changed` message (e.g. the
  // pop-out closed before this listener ever mounted) correctly falls back
  // to the `popout-closed` payload instead of an undefined "known" value
  // that would look identical to "the pop-out is at the virtual root".
  const lastKnownPopoutWorkspaceRef = useRef<{
    set: boolean
    workspaceId?: string
  }>({ set: false })

  // C4: the docked LibraryExplorer's CURRENT location — not the
  // `libraryPanel.workspaceId` the store recorded at open time, which goes
  // stale the moment the operator navigates to a different workspace inside
  // the docked panel without closing it (there was no live workspace signal
  // wired here before C4 — `onWorkspaceChange` is new to this file). Kept in
  // refs, not state: this is read exactly once, at pop-out click time, and
  // does not need to trigger a re-render on every keystroke of navigation.
  const currentWorkspaceRef = useRef<string | undefined>(undefined)
  const currentSelectionRef = useRef<{ path: string | null; folder: string }>({
    path: null,
    folder: '',
  })

  useEffect(() => {
    return onLibraryWorkspaceChanged((workspaceId) => {
      lastKnownPopoutWorkspaceRef.current = { set: true, workspaceId }
      presenceRef.current = [{ panelId: 'library', workspaceId }]

      const owned = ownedPopoutRef.current
      if (!owned) return
      const nextKey = panelIdentityKey({ panelId: 'library', workspaceId })
      if (nextKey === owned.identityKey) return
      libraryPopoutHandles.delete(owned.identityKey)
      libraryPopoutHandles.set(nextKey, owned.window)
      owned.identityKey = nextKey
    })
  }, [])

  // A pop-out close only re-docks in its opener. The held handle is the
  // ownership proof; a manual/third tab's broadcast therefore cannot move
  // this tab's panel state (MAJ-208).
  useEffect(() => {
    const reDockOwned = (workspaceId?: string): void => {
      const owned = ownedPopoutRef.current
      presenceRef.current = []
      if (!owned) return
      ownedPopoutRef.current = null
      owned.stop()
      libraryPopoutHandles.delete(owned.identityKey)
      const active = useUiStore.getState().activePanel
      if (active !== null && active.id !== 'library') return
      const known = lastKnownPopoutWorkspaceRef.current
      leaveGateThen(() => {
        const current = useUiStore.getState().activePanel
        if (current !== null && current.id !== 'library') return
        useUiStore.getState().openPanel('library', {
          workspaceId: known.set ? known.workspaceId : workspaceId,
        })
      })
    }

    const stopBroadcast = onLibraryPopoutClosed(reDockOwned)
    return () => {
      stopBroadcast()
      const current = ownedPopoutRef.current
      current?.stop()
      if (current) libraryPopoutHandles.delete(current.identityKey)
      ownedPopoutRef.current = null
    }
  }, [])

  useEffect(() => {
    return useUiStore.subscribe((state) => {
      if (state.activePanel?.id !== 'library') return
      const key = panelIdentityKey({
        panelId: 'library',
        workspaceId: state.activePanel.context.workspaceId,
      })
      const handle = libraryPopoutHandles.get(key)
      if (handle?.closed) {
        libraryPopoutHandles.delete(key)
        return
      }
      if (handle) {
        state.closePanel()
        try {
          handle.focus()
        } catch {
          // Focus is best-effort; keeping one tab is more important than
          // opening a duplicate when the browser declines the request.
        }
        return
      }
      if (presenceRef.current.some((identity) => panelIdentityKey(identity) === key)) {
        state.closePanel()
        state.addToast({
          message: 'The Library is already open in another tab.',
          variant: 'default',
        })
      }
    })
  }, [])

  const libraryPanel = shellProps?.context ?? (activePanel?.id === 'library' ? activePanel.context : null)

  // Arrow function expression (not a `function` declaration) so TypeScript's
  // control-flow narrowing of `libraryPanel` from the early-return above
  // actually carries into this closure — a hoisted function declaration
  // does NOT inherit that narrowing, since TS must assume it could be
  // invoked independent of the narrowing check's control flow.
  const handlePopOut = useCallback((): boolean => {
    if (!libraryPanel) return false
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
    const identity = { panelId: 'library', workspaceId }
    const identityKey = panelIdentityKey(identity)
    const outcome = resolvePanelOpen({
      identity,
      handles: libraryPopoutHandles,
      presence: presenceRef.current,
      open: () => window.open(`/#/library${qs ? `?${qs}` : ''}`, '_blank'),
    })

    if (outcome.kind === 'blocked') {
      useUiStore.getState().addToast({
        message: 'The Library tab was blocked. Allow popups and try again.',
        variant: 'error',
      })
      return false
    }
    if (outcome.kind === 'affordance') {
      useUiStore.getState().addToast({
        message: 'The Library is already open in another tab.',
        variant: 'default',
      })
      return true
    }
    if (outcome.kind === 'focused') {
      return true
    }

    const popup = libraryPopoutHandles.get(identityKey)
    if (!popup) return false
    try {
      popup.opener = null
    } catch {
      libraryPopoutHandles.delete(identityKey)
      popup.close()
      useUiStore.getState().addToast({
        message: 'The Library tab could not open. The panel remains here.',
        variant: 'error',
      })
      return false
    }
    const owned: OwnedLibraryPopout = {
      window: popup,
      identityKey,
      stop: () => {},
    }
    ownedPopoutRef.current = owned
    owned.stop = watchPopoutClosed(popup, () => {
      if (ownedPopoutRef.current !== owned) return
      ownedPopoutRef.current = null
      libraryPopoutHandles.delete(owned.identityKey)
      presenceRef.current = []
      const known = lastKnownPopoutWorkspaceRef.current
      const current = useUiStore.getState().activePanel
      if (current !== null && current.id !== 'library') return
      leaveGateThen(() => {
        const latest = useUiStore.getState().activePanel
        if (latest !== null && latest.id !== 'library') return
        useUiStore.getState().openPanel('library', {
          workspaceId: known.set ? known.workspaceId : workspaceId,
        })
      })
    })
    return true
  }, [libraryPanel])

  useEffect(() => {
    if (!shellProps) return
    shellProps.registerExpand(handlePopOut)
    return () => shellProps.registerExpand(null)
  }, [handlePopOut, shellProps])

  if (!libraryPanel || (shellProps && activePanel?.id !== 'library')) return null

  const Root = shellProps ? 'div' : 'aside'

  return (
    <Root
      data-testid="library-panel-docked"
      aria-label="Library panel"
      className="flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-[var(--color-surface-0)]"
    >
      <LibraryExplorer
        // Keys the mount to the initial target so a second "open Library"
        // click with a DIFFERENT initial workspace (e.g. sidebar → virtual
        // root after the panel was already scoped to one workspace) starts
        // fresh navigation state instead of leaving stale path/selection from
        // the previous target.
        key={libraryPanel.workspaceId ?? 'root'}
        initialWorkspaceId={libraryPanel.workspaceId}
        onWorkspaceChange={(id) => {
          currentWorkspaceRef.current = id ?? undefined
        }}
        onSelectionChange={(selection) => {
          currentSelectionRef.current = selection
        }}
      />
    </Root>
  )
}
