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
// Pop-out ownership and workspace tracking live in panelPopoutLifecycle.ts,
// whose app-level owner remains mounted after this content unmounts. That
// stable owner keeps the opener-only handle, consumes the Library's
// continuous workspace broadcasts, and re-docks at the last known workspace
// through the leave guard. A different panel in the slot still wins.
import { useCallback, useEffect, useRef } from 'react'
import { useUiStore } from '@/store/ui'
import { generateId } from '@/lib/constants'
import {
  armPanelFocusFallback,
  focusPanelTab,
  resolveRegisteredPanelOpen,
  type PanelIdentity,
} from '@/lib/panelTabPresence'
import {
  registerPanelPopout,
  releasePanelPopoutWithoutAppOwner,
} from '@/lib/panelPopoutLifecycle'
import { leaveGateThen } from '@/components/panel-shell/leaveGate'
import type { PanelContentProps } from '@/components/panel-shell/types'
import { LibraryExplorer } from './LibraryExplorer'

export interface LibraryPanelProps {
  shellProps?: PanelContentProps
}

export function LibraryPanel({ shellProps }: LibraryPanelProps = {}) {
  const activePanel = useUiStore((s) => s.activePanel)
  const isolatedCleanupRef = useRef<{
    identity: PanelIdentity
    handle: Window
  } | null>(null)

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

  useEffect(() => () => {
    const owned = isolatedCleanupRef.current
    if (owned) releasePanelPopoutWithoutAppOwner(owned.identity, owned.handle)
    isolatedCleanupRef.current = null
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
    const popoutId = generateId()
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
    params.set('popout', popoutId)
    const qs = params.toString()
    // Hash routing: the route + search MUST live in the `#/` fragment or the
    // router falls back to the default route (same caveat as browser-live).
    const identity: PanelIdentity = { panelId: 'library', workspaceId }
    let openedPopup: Window | null = null
    const outcome = resolveRegisteredPanelOpen({
      identity,
      open: () => {
        openedPopup = window.open(`/#/library${qs ? `?${qs}` : ''}`, '_blank')
        return openedPopup
      },
    })

    if (outcome.kind === 'blocked') {
      useUiStore.getState().addToast({
        message: 'The Library tab was blocked. Allow popups and try again.',
        variant: 'error',
      })
      return false
    }
    if (outcome.kind === 'affordance') {
      const openHere = () => {
        armPanelFocusFallback(identity)
        useUiStore.getState().openPanel('library', { workspaceId })
      }
      useUiStore.getState().addToast({
        message: 'The Library is already open in another tab — switch.',
        variant: 'default',
        duration: 10_000,
        action: {
          label: 'Switch',
          onClick: () => {
            if (!focusPanelTab(identity)) openHere()
          },
        },
        secondaryAction: { label: 'Open here', onClick: openHere },
      })
      return true
    }
    if (outcome.kind === 'focused') {
      return true
    }

    const popup = openedPopup as Window | null
    if (!popup) return false
    try {
      popup.opener = null
    } catch {
      popup.close()
      useUiStore.getState().addToast({
        message: 'The Library tab could not open. The panel remains here.',
        variant: 'error',
      })
      return false
    }
    isolatedCleanupRef.current = { identity, handle: popup }
    registerPanelPopout({
      popoutId,
      identity,
      handle: popup,
      onClosed: (finalIdentity) => {
        isolatedCleanupRef.current = null
        const current = useUiStore.getState().activePanel
        if (current !== null && current.id !== 'library') return
        leaveGateThen(current?.id ?? null, () => {
          const latest = useUiStore.getState().activePanel
          if (latest !== null && latest.id !== 'library') return
          useUiStore.getState().openPanel('library', {
            workspaceId: finalIdentity.workspaceId,
          })
        })
      },
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
