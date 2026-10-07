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
import { flushSync } from 'react-dom'
import { useRouter } from '@tanstack/react-router'
import { isStandaloneWindow } from '@/lib/browserDisplayMode'
import type { OpenPanel, PanelDefinition, PanelContext, PanelId } from './types'
import { usePanelShellStore } from './panelShellStore'
import { readPanelWidth, writePanelWidth, deletePanelWidth, panelWidthScope } from './panelWidthMemory'
import { getDiscardConfirmDialogOpen } from '@/components/library/preview/unsavedGuard'
import { focusChatInput, focusPanelTriggerOrigin } from './panelFocus'
import { generateId } from '@/lib/constants'
import {
  panelIdentityFromContext,
  resolveRegisteredPanelOpen,
} from '@/lib/panelTabPresence'
import {
  discardPanelPopout,
  registerPanelPopout,
} from '@/lib/panelPopoutLifecycle'
import { leaveGateThen } from './leaveGate'

export { PANEL_TRIGGER_ATTR } from './panelFocus'

export type PanelFocusReturnReason = 'trigger' | 'chat'

let panelWidthPersistenceWarned = false

function restoreFocusToChat(isCurrent: () => boolean): void {
  const focus = () => isCurrent() && focusChatInput()
  if (focus()) return
  requestAnimationFrame(focus)
}

function restoreFocusToTrigger(id: PanelId, isCurrent: () => boolean): void {
  if (!isCurrent()) return
  if (focusPanelTriggerOrigin(id)) return
  let frames = 0
  const tick = (): void => {
    if (!isCurrent()) return
    if (focusPanelTriggerOrigin(id)) return
    if (++frames < 10) requestAnimationFrame(tick)
    else restoreFocusToChat(isCurrent)
  }
  requestAnimationFrame(tick)
}

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
    const width = readPanelWidth(username, current.id, current.context)
    if (width === null) usePanelShellStore.getState().resetPanelWidth()
    else usePanelShellStore.getState().setPanelWidth(width)
  }, [username, widthScope])
}

type PanelExpandResult = 'opened' | 'cancelled' | 'blocked' | 'error'

async function expandActivePanel(options: {
  panels: readonly PanelDefinition[]
  getCurrentContext?: () => PanelContext
  runGuard: (definition: PanelDefinition | undefined) => Promise<boolean>
  finishClose: (id: PanelId, focusReturn: PanelFocusReturnReason) => void
  navigateFullScreen: (id: PanelId, search: Record<string, string>) => Promise<void>
}): Promise<PanelExpandResult> {
  const { panels, getCurrentContext, runGuard, finishClose, navigateFullScreen } = options
  const { activePanel, guardPending } = usePanelShellStore.getState()
  if (activePanel === null || guardPending) return 'cancelled'
  const definition = panels.find((panel) => panel.id === activePanel.id)
  if (definition === undefined) return 'cancelled'
  if (definition.beforeLeave !== undefined) {
    try {
      if (!(await runGuard(definition))) return 'cancelled'
    } catch (error) {
      console.error('[side-panel] Expand leave guard failed', { panelId: definition.id, error })
      usePanelShellStore.getState().addToast({
        message: `${definition.title} could not expand because its unsaved-change check failed. Try again.`,
        variant: 'error',
      })
      return 'error'
    }
  }

  const context = getCurrentContext?.() ?? activePanel.context
  const identity = panelIdentityFromContext(definition.id, context)
  if (!identity) {
    usePanelShellStore.getState().addToast({
      message: `${definition.title} could not open full screen. Try again.`,
      variant: 'error',
    })
    return 'error'
  }

  if (isStandaloneWindow()) {
    try {
      // App windows have no tabs. Use the existing full-screen route without
      // a popout owner; its Back control re-docks in this same window. Await
      // navigation before closing so takeover history cannot back out of it.
      await navigateFullScreen(definition.id, definition.fullScreen.toSearch(context))
      finishClose(activePanel.id, 'chat')
      return 'opened'
    } catch (error) {
      console.error('[side-panel] Same-window Expand navigation failed', error)
      usePanelShellStore.getState().addToast({
        message: `${definition.title} could not open full screen. The panel remains here.`,
        variant: 'error',
      })
      return 'error'
    }
  }

  const popoutId = generateId()
  let popupCreationThrew = false
  let popupCreationError: unknown
  let popup: Window | null = null
  const outcome = resolveRegisteredPanelOpen({
    identity,
    open: () => {
      try {
        popup = window.open('about:blank', '_blank')
        return popup
      } catch (error) {
        popupCreationThrew = true
        popupCreationError = error
        throw error
      }
    },
  })

  if (outcome.kind === 'blocked') {
    if (popupCreationThrew) {
      console.error('[side-panel] Expand failed while opening a tab', popupCreationError)
    }
    usePanelShellStore.getState().addToast({
      message: popupCreationThrew
        ? `${definition.title} could not open full screen. The panel remains here.`
        : `${definition.title} was blocked. Allow pop-ups and try again.`,
      variant: 'error',
    })
    return popupCreationThrew ? 'error' : 'blocked'
  }

  const reopen = () => {
    const store = usePanelShellStore.getState()
    if (store.activePanel !== null) return
    ;(store.openPanel as (id: PanelId, context?: PanelContext) => void)(definition.id, context)
  }
  if (outcome.kind === 'affordance') {
    const { showPanelTabSwitch } = await import('./panelTabSwitch')
    showPanelTabSwitch(identity, `The ${definition.title}`, reopen)
    finishClose(activePanel.id, 'chat')
    return 'opened'
  }
  if (outcome.kind === 'focus-failed') {
    const { showPanelTabFocusDegraded } = await import('./panelTabSwitch')
    showPanelTabFocusDegraded(`The ${definition.title}`)
    finishClose(activePanel.id, 'chat')
    return 'opened'
  }
  if (outcome.kind === 'focused') {
    const { showPanelTabFocused } = await import('./panelTabSwitch')
    showPanelTabFocused(`The ${definition.title}`)
    finishClose(activePanel.id, 'chat')
    return 'opened'
  }

  const openedPopup = popup as Window | null
  if (!openedPopup) return 'blocked'
  try {
    openedPopup.opener = null
    registerPanelPopout({
      popoutId,
      identity,
      context,
      handle: openedPopup,
      onClosed: (_finalIdentity, finalContext) => {
        const store = usePanelShellStore.getState()
        const outgoingPanelId = store.activePanel?.id ?? null
        if (outgoingPanelId !== null && outgoingPanelId !== definition.id) return
        leaveGateThen(outgoingPanelId, () => {
          const latest = usePanelShellStore.getState()
          if (latest.activePanel !== null && latest.activePanel.id !== definition.id) return
          ;(latest.openPanel as (id: PanelId, context?: PanelContext) => void)(definition.id, finalContext)
          restoreFocusToChat(() => {
            const current = usePanelShellStore.getState().activePanel
            return current?.id === definition.id && current.context === finalContext
          })
        })
      },
    })
    flushSync(() => finishClose(activePanel.id, 'chat'))
    const search = new URLSearchParams(definition.fullScreen.toSearch(context))
    search.set('popout', popoutId)
    const fullScreenUrl = `/#/panel/${definition.id}?${search.toString()}`
    const absoluteUrlBytes = new TextEncoder().encode(
      new URL(fullScreenUrl, window.location.href).href,
    ).byteLength
    if (absoluteUrlBytes > 8 * 1024) {
      console.warn(
        '[side-panel] Full-screen panel URL exceeds 8 KB; opening it without truncation.',
        { panelId: definition.id, bytes: absoluteUrlBytes },
      )
    }
    openedPopup.location.replace(fullScreenUrl)
    return 'opened'
  } catch (error) {
    console.error('[side-panel] Expand handoff failed', error)
    discardPanelPopout(identity, openedPopup)
    try {
      openedPopup.close()
    } catch {
      // The original error remains the actionable failure.
    }
    reopen()
    usePanelShellStore.getState().addToast({
      message: `${definition.title} could not open full screen. The panel remains here.`,
      variant: 'error',
    })
    return 'error'
  }
}

export function usePanelShell(panels: readonly PanelDefinition[], username: string) {
  // Normal-tab/demo shells do not need routing; installed-app expansion does.
  // A missing router there is reported through Expand's visible error path.
  const router = useRouter({ warn: false })
  const activePanel = usePanelShellStore((s) => s.activePanel)
  const guardPending = usePanelShellStore((s) => s.guardPending)

  const panelsRef = useRef(panels)
  const historyFocusReturnRef = useRef<PanelFocusReturnReason>('trigger')
  const focusReturnGenerationRef = useRef(0)
  panelsRef.current = panels

  // Production entry points open through the global store, not requestOpen.
  // Hydrate on every user/panel/scope transition so those paths restore the
  // same remembered width as shell-owned opens.
  usePanelWidthHydration(username, activePanel)

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
    (id: PanelId, focusReturn: PanelFocusReturnReason) => {
      usePanelShellStore.getState().closePanel() // also clears historyPushed
      const generation = ++focusReturnGenerationRef.current
      // A prior close may still be retrying on animation frames. It must not
      // steal focus after this close, or after another panel has opened.
      const isCurrent = () => focusReturnGenerationRef.current === generation
        && usePanelShellStore.getState().activePanel === null
      if (focusReturn === 'chat') restoreFocusToChat(isCurrent)
      else restoreFocusToTrigger(id, isCurrent)
    },
    [],
  )

  /**
   * Guard, then close. `repushOnCancel` is true when a history entry for
   * the open panel was ALREADY popped (the Back-button path) and a guard
   * cancel must re-push it so the history stack never disagrees with the
   * shell state.
   */
  const guardThenClose = useCallback(
    async (
      repushOnCancel: boolean,
      focusReturn: PanelFocusReturnReason = historyFocusReturnRef.current,
    ): Promise<void> => {
      const { activePanel } = usePanelShellStore.getState()
      if (activePanel === null) {
        historyFocusReturnRef.current = 'trigger'
        return
      }
      const def = panelsRef.current.find((p) => p.id === activePanel.id)
      try {
        if (await runGuard(def)) {
          finishClose(activePanel.id, focusReturn)
        } else if (repushOnCancel) {
          usePanelShellStore.getState().setHistoryPushed(true)
          window.history.pushState({ sidePanel: activePanel.id }, '')
        }
      } finally {
        historyFocusReturnRef.current = 'trigger'
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
  const requestClose = useCallback((focusReturn: PanelFocusReturnReason = 'trigger'): void => {
    const store = usePanelShellStore.getState()
    if (store.guardPending || store.activePanel === null) return
    if (getDiscardConfirmDialogOpen()) return
    if (store.historyPushed) {
      historyFocusReturnRef.current = focusReturn
      window.history.back() // popstate runs guardThenClose(false)
      return
    }
    void guardThenClose(false, focusReturn)
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
   * SP-38 expand: the shell owns the popup, presence, handoff and re-dock
   * lifecycle. A panel only reports its current addressable context.
   */
  const requestExpand = useCallback(
    (getCurrentContext?: () => PanelContext) => expandActivePanel({
      panels: panelsRef.current,
      getCurrentContext,
      runGuard,
      finishClose,
      navigateFullScreen: async (id, search) => {
        if (!router) throw new Error('Full-screen navigation requires the app router.')
        await router.navigate({ to: '/panel/$panelId', params: { panelId: id }, search })
      },
    }),
    [finishClose, runGuard, router],
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
      if (!writePanelWidth(username, activePanel.id, activePanel.context, px) && !panelWidthPersistenceWarned) {
        panelWidthPersistenceWarned = true
        console.warn('[side-panel] Panel width could not be persisted; using session memory only.')
      }
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
