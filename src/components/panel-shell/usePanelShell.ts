// usePanelShell.ts — the orchestration seam between the shell, the panel
// registry and the shell store (side-panel-shell-spec.md §8): CRIT-001's
// beforeLeave gate (awaited BEFORE the store, the URL or the content move —
// the outgoing panel stays mounted the whole time), SP-13 width memory
// (read on open, written on settle, deleted on reset), US-6/US-7 Escape
// handling with SP-19's Browser exception, SP-26's Back affordance (one
// pushed history step per open panel; browser/✕/swipe all route through
// it), and MIN-002's focus return to the opening trigger.
//
// Wave 0 wires this through the demo's registry; wave 1 swaps the demo
// registry for the real one and hands real ids (username, workspace) in —
// none of the call sites change shape.

import { useCallback, useEffect, useRef } from 'react'
import type { PanelDefinition, PanelContext, PanelId } from './types'
import {
  usePanelShellStore,
  PANEL_WIDTH_UNSET,
} from './panelShellStore'
import {
  readPanelWidth,
  writePanelWidth,
  deletePanelWidth,
  panelWidthScope,
} from './panelWidthMemory'
import { getDiscardConfirmDialogOpen } from '@/components/library/preview/unsavedGuard'
/** DOM hook the shell looks up to return focus on close (MIN-002). */
export const PANEL_TRIGGER_ATTR = 'data-panel-trigger'

export function usePanelShell(panels: PanelDefinition[], username: string) {
  const activePanel = usePanelShellStore((s) => s.activePanel)
  const guardPending = usePanelShellStore((s) => s.guardPending)

  const panelsRef = useRef(panels)
  panelsRef.current = panels

  /** Return focus to the control that opened the panel (MIN-002). */
  const restoreFocusToTrigger = useCallback((id: PanelId) => {
    const root = document.querySelector(`[${PANEL_TRIGGER_ATTR}="${id}"]`)
    if (root instanceof HTMLElement && root.isConnected) root.focus()
  }, [])

  /** CRIT-001: run the outgoing panel's guard; true when the move is allowed. */
  const runGuard = useCallback(async (def: PanelDefinition | undefined): Promise<boolean> => {
    if (def?.beforeLeave === undefined) return true
    usePanelShellStore.getState().setGuardPending(true)
    try {
      return await def.beforeLeave()
    } finally {
      usePanelShellStore.getState().setGuardPending(false)
    }
  }, [])

  /** Store-level close + focus return. Guard already passed. */
  const finishClose = useCallback((id: PanelId) => {
    usePanelShellStore.getState().closePanel() // also clears historyPushed
    restoreFocusToTrigger(id)
  }, [restoreFocusToTrigger])

  /**
   * Guard, then close. `repushOnCancel` is true when a history entry for
   * the open panel was ALREADY popped (the Back-button path) and a guard
   * cancel must re-push it so the history stack never disagrees with the
   * shell state.
   */
  const guardThenClose = useCallback(async (repushOnCancel: boolean): Promise<void> => {
    const { activePanel } = usePanelShellStore.getState()
    if (activePanel === null) return
    const def = panelsRef.current.find((p) => p.id === activePanel.id)
    if (await runGuard(def)) {
      finishClose(activePanel.id)
    } else if (repushOnCancel) {
      usePanelShellStore.getState().setHistoryPushed(true)
      window.history.pushState({ sidePanel: activePanel.id }, '')
    }
  }, [runGuard, finishClose])

  /**
   * The one close door — Escape, the header ✕, and SP-26's swipe all come
   * through here. A pushed history entry routes the close through
   * history.back() so URL and shell state never disagree; popstate runs
   * guardThenClose on the way back.
   */
  const requestClose = useCallback((): void => {
    const store = usePanelShellStore.getState()
    if (store.guardPending || store.activePanel === null) return
    if (getDiscardConfirmDialogOpen()) return
    if (store.historyPushed) {
      window.history.back() // popstate runs guardThenClose(false)
      return
    }
    void guardThenClose(false)
  }, [guardThenClose])

  /**
   * Open (or switch to) a panel. Switching panels runs the OUTGOING
   * panel's guard first — CRIT-001: the outgoing panel stays mounted until
   * the guard resolves, so its own dialog can host the confirm.
   */
  const requestOpen = useCallback((id: PanelId, context: PanelContext = {}): void => {
    const store = usePanelShellStore.getState()
    if (store.guardPending) return
    const outgoing = store.activePanel
    const go = (): void => {
      const s = usePanelShellStore.getState()
      const prev = s.activePanel
      const switched = prev?.id !== id
      // Same panel, new scope (e.g. the demo's workspace switcher): the
      // width bucket changed, so the width is re-read from memory (SP-13).
      const scopeChanged =
        prev !== null && prev.id === id &&
        panelWidthScope(id, prev.context) !== panelWidthScope(id, context)
      s.openPanel(id, context)
      if (switched || scopeChanged) {
        s.setPanelWidth(readPanelWidth(username, id, context) ?? PANEL_WIDTH_UNSET)
      }
      // Takeover push happens in the takeover effect in the shell hook-up.
    }
    if (outgoing !== null && outgoing.id !== id) {
      const outDef = panelsRef.current.find((p) => p.id === outgoing.id)
      void (async () => {
        if (await runGuard(outDef)) go()
      })()
      return
    }
    go()
  }, [runGuard, username])

  /**
   * SP-26's header Back: when the takeover pushed a history entry, Back is
   * literally history.back() (popstate runs the guard); without an entry
   * it degenerates to the normal guarded close.
   */
  const requestBack = useCallback((): void => {
    if (usePanelShellStore.getState().historyPushed) {
      window.history.back() // popstate runs guardThenClose(false)
      return
    }
    requestClose()
  }, [requestClose])

  /** SP-12 expand: open the panel's full-page target in a new tab.
   *  Returns false when the popup was blocked so the shell can fail visibly. */
  const requestExpand = useCallback((): boolean => {
    const { activePanel } = usePanelShellStore.getState()
    if (activePanel === null) return false
    const def = panelsRef.current.find((p) => p.id === activePanel.id)
    if (def === undefined) return false
    const url = def.expandTarget(activePanel.context)
    const win = window.open(url, '_blank')
    return win !== null && !win.closed
  }, [])

  /**
   * US-5's tab-strip toggle: a second click on the ACTIVE panel's trigger
   * closes it; anything else opens (or guards-then-switches). Wave 1's real
   * tab strip uses the same seam.
   */
  const requestToggle = useCallback(
    (id: PanelId, context: PanelContext = {}): void => {
      const { activePanel, guardPending } = usePanelShellStore.getState()
      if (guardPending) return
      if (activePanel?.id === id) {
        requestClose()
        return
      }
      requestOpen(id, context)
    },
    [requestClose, requestOpen],
  )

  /** SP-13 width settle: write the stored width for this panel's scope. */
  const settleWidth = useCallback(
    (px: number): void => {
      const { activePanel } = usePanelShellStore.getState()
      if (activePanel === null) return
      setPanelWidthInStore(px)
      writePanelWidth(username, activePanel.id, activePanel.context, px)
    },
    [username],
  )

  /** US-2 AS-3 double-click reset: DELETE the stored width (US-3 AS-4 — the
   *  default is re-derived at read time, never stored). */
  const resetWidth = useCallback((): void => {
    const { activePanel } = usePanelShellStore.getState()
    if (activePanel === null) return
    setPanelWidthInStore(PANEL_WIDTH_UNSET)
    deletePanelWidth(username, activePanel.id, activePanel.context)
  }, [username])

  return {
    activePanel,
    guardPending,
    requestClose,
    requestBack,
    guardThenClose,
    requestOpen,
    requestToggle,
    requestExpand,
    settleWidth,
    resetWidth,
  }
}

/** Local alias so the module reads without re-importing the store twice. */
function setPanelWidthInStore(px: number): void {
  usePanelShellStore.getState().setPanelWidth(px)
}

/**
 * SP-26's Back affordance plumbing: while the takeover is showing (or, in
 * wave 1, the shell decides a Back step belongs to this open), exactly one
 * history entry is pushed per open panel, and popstate (the browser's own
 * Back, or requestClose routing through history.back()) closes the panel
 * through the guard. A guard cancel re-pushes so history never disagrees
 * with the shell state.
 */
export function usePanelShellHistory(options: {
  /** true while the takeover layout is showing the open panel. */
  enabled: boolean
  guardThenClose: (repushOnCancel: boolean) => Promise<void>
}): void {
  const { enabled, guardThenClose } = options
  const activePanel = usePanelShellStore((st) => st.activePanel)

  // One push per (takeover visible, panel open). The store flag keeps the
  // push/pop bookkeeping in one place shared with requestClose.
  useEffect(() => {
    const store = usePanelShellStore.getState()
    if (enabled && activePanel !== null && !store.historyPushed) {
      store.setHistoryPushed(true)
      window.history.pushState({ sidePanel: activePanel.id }, '')
    }
  }, [enabled, activePanel])

  useEffect(() => {
    const onPopState = (): void => {
      const store = usePanelShellStore.getState()
      if (!store.historyPushed || store.activePanel === null) return
      store.setHistoryPushed(false)
      void guardThenClose(false)
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [guardThenClose])
}
