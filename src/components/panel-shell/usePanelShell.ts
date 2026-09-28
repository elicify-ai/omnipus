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
import type { OpenPanel, PanelDefinition, PanelContext, PanelId } from './types'
import { usePanelShellStore, PANEL_WIDTH_UNSET } from './panelShellStore'
import { readPanelWidth, writePanelWidth, deletePanelWidth, panelWidthScope } from './panelWidthMemory'
import { getDiscardConfirmDialogOpen } from '@/components/library/preview/unsavedGuard'
import { focusPanelTriggerOrigin } from './panelFocus'

export { PANEL_TRIGGER_ATTR } from './panelFocus'

function usePanelWidthHydration(
  username: string,
  activePanel: ReturnType<typeof usePanelShellStore.getState>['activePanel'],
): void {
  const widthScope = activePanel === null
    ? null
    : `${activePanel.id}:${panelWidthScope(activePanel.id, activePanel.context)}`

  useEffect(() => {
    const current = usePanelShellStore.getState().activePanel
    if (current === null) return
    usePanelShellStore.getState().setPanelWidth(
      readPanelWidth(username, current.id, current.context) ?? PANEL_WIDTH_UNSET,
    )
  }, [username, widthScope])
}

export function usePanelShell(panels: readonly PanelDefinition[], username: string) {
  const activePanel = usePanelShellStore((s) => s.activePanel)
  const guardPending = usePanelShellStore((s) => s.guardPending)

  const panelsRef = useRef(panels)
  panelsRef.current = panels

  // Production entry points open through the global store, not requestOpen.
  // Hydrate on every user/panel/scope transition so those paths restore the
  // same remembered width as shell-owned opens.
  usePanelWidthHydration(username, activePanel)

  /** Return focus to the control that opened the panel (MIN-002). The
   *  trigger can be display:none AT the close moment — a container-query
   *  strip flip (compact dropdown ↔ full strip) or the takeover's chat
   *  un-hide resolves only after the close re-render + style recalc, and
   *  focus() into display:none is a silent no-op — so the focus lands via a
   *  short frame-bounded retry. Shell-level correctness: wave 1's real tab
   *  strip collapses the same way. */
  const restoreFocusToTrigger = useCallback((id: PanelId) => {
    const tryFocus = (): boolean => {
      return focusPanelTriggerOrigin(id)
    }
    const focusChatInput = (): void => {
      document.querySelector<HTMLElement>('[data-testid="chat-input"]')?.focus()
    }
    if (tryFocus()) return
    let frames = 0
    const tick = (): void => {
      if (tryFocus()) return
      // Bounded: if the trigger never becomes focusable (e.g. the chat
      // column is hidden in the takeover), stop after 10 frames and let
      // focus stay where the browser put it.
      if (++frames < 10) {
        requestAnimationFrame(tick)
      } else {
        focusChatInput()
      }
    }
    requestAnimationFrame(tick)
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
  const finishClose = useCallback(
    (id: PanelId) => {
      usePanelShellStore.getState().closePanel() // also clears historyPushed
      restoreFocusToTrigger(id)
    },
    [restoreFocusToTrigger],
  )

  /**
   * Guard, then close. `repushOnCancel` is true when a history entry for
   * the open panel was ALREADY popped (the Back-button path) and a guard
   * cancel must re-push it so the history stack never disagrees with the
   * shell state.
   */
  const guardThenClose = useCallback(
    async (repushOnCancel: boolean): Promise<void> => {
      const { activePanel } = usePanelShellStore.getState()
      if (activePanel === null) return
      const def = panelsRef.current.find((p) => p.id === activePanel.id)
      if (await runGuard(def)) {
        finishClose(activePanel.id)
      } else if (repushOnCancel) {
        usePanelShellStore.getState().setHistoryPushed(true)
        window.history.pushState({ sidePanel: activePanel.id }, '')
      }
    },
    [runGuard, finishClose],
  )

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
  const requestOpen = useCallback(
    (id: PanelId, context: PanelContext = {}): void => {
      const store = usePanelShellStore.getState()
      if (store.guardPending) return
      const outgoing = store.activePanel
      const go = (): void => {
        const s = usePanelShellStore.getState()
        ;(s.openPanel as (panelId: PanelId, panelContext?: PanelContext) => void)(id, context)
        // The hydration effect handles all entry points, including this one.
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
    },
    [runGuard],
  ) as OpenPanel

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

  /**
   * SP-12 expand: guard first, then run the panel-specific pop-out action (or
   * the registry target fallback), and close the source only after a popup
   * successfully opens. A declined guard is not a popup failure.
   */
  const requestExpand = useCallback(
    async (expandAction?: () => boolean): Promise<'opened' | 'cancelled' | 'blocked' | 'error'> => {
      const { activePanel, guardPending } = usePanelShellStore.getState()
      if (activePanel === null || guardPending) return 'cancelled'
      const def = panelsRef.current.find((p) => p.id === activePanel.id)
      if (def === undefined) return 'cancelled'
      // Panels without a leave guard (Browser in wave 1) reach window.open
      // in the original click stack. Guarded panels await their decision.
      if (def.beforeLeave !== undefined && !(await runGuard(def))) return 'cancelled'

      try {
        if (expandAction !== undefined) {
          if (!expandAction()) return 'blocked'
        } else {
          const url = def.expandTarget(activePanel.context)
          const win = window.open(url, '_blank')
          if (win === null) return 'blocked'
          if (win.closed) {
            console.error('[side-panel] Expand failed', new Error('window.open returned a closed window'))
            return 'error'
          }
          win.opener = null
        }
      } catch (error) {
        console.error('[side-panel] Expand failed', error)
        return 'error'
      }
      const current = usePanelShellStore.getState().activePanel
      if (current?.id === activePanel.id && current.context === activePanel.context) {
        finishClose(activePanel.id)
      }
      return 'opened'
    },
    [finishClose, runGuard],
  )

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
      ;(requestOpen as (panelId: PanelId, panelContext?: PanelContext) => void)(id, context)
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
    usePanelShellStore.getState().resetPanelWidth()
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
  const historyPushed = usePanelShellStore((st) => st.historyPushed)
  const guardPending = usePanelShellStore((st) => st.guardPending)
  const collapsingRef = useRef(false)
  const ownsEntryRef = useRef(false)
  const backGuardRef = useRef(false)

  // One push per (takeover visible, panel open). The store flag keeps the
  // push/pop bookkeeping in one place shared with requestClose.
  useEffect(() => {
    const store = usePanelShellStore.getState()
    if (enabled && activePanel !== null && !historyPushed && !guardPending && !backGuardRef.current) {
      store.setHistoryPushed(true)
      ownsEntryRef.current = true
      window.history.pushState({ sidePanel: activePanel.id }, '')
      return
    }
    if (enabled && activePanel !== null && historyPushed) {
      // Includes a guard-cancel re-push performed by guardThenClose.
      ownsEntryRef.current = true
      return
    }
    if ((!enabled || activePanel === null) && ownsEntryRef.current) {
      collapsingRef.current = true
      ownsEntryRef.current = false
      store.setHistoryPushed(false)
      window.history.back()
    }
  }, [enabled, activePanel, historyPushed, guardPending])

  useEffect(() => {
    const onPopState = (): void => {
      if (collapsingRef.current) {
        collapsingRef.current = false
        return
      }
      const store = usePanelShellStore.getState()
      if (!store.historyPushed || store.activePanel === null) return
      ownsEntryRef.current = false
      backGuardRef.current = true
      store.setHistoryPushed(false)
      void guardThenClose(true).finally(() => {
        backGuardRef.current = false
      })
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [guardThenClose])
}
