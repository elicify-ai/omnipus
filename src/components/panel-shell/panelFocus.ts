import type { PanelId } from './types'

export const PANEL_TRIGGER_ATTR = 'data-panel-trigger'

/** Shared US-9 focus target for docked close, re-dock and same-tab return. */
export function focusChatInput(): boolean {
  const input = document.querySelector<HTMLElement>('[data-testid="chat-input"]')
  input?.focus()
  return input !== null && document.activeElement === input
}

/** The chat composer can mount after async session restoration completes. */
export function focusChatInputWhenReady(isCurrentChat: () => boolean): void {
  if (!isCurrentChat() || focusChatInput()) return
  const observer = new MutationObserver(() => {
    if (!isCurrentChat() || focusChatInput()) observer.disconnect()
  })
  observer.observe(document.body, { childList: true, subtree: true })
}

let origin: { panelId: PanelId; element: HTMLElement } | null = null
let pendingClickOrigin: { panelId: string; element: HTMLElement } | null = null
let focusPanelOnOpen: PanelId | null = null

/** Stage a real click before its handler opens the panel through the store. */
export function recordPanelTriggerClick(event: MouseEvent): void {
  const target = event.target
  const candidate = target instanceof Element
    ? target.closest<HTMLElement>(`[${PANEL_TRIGGER_ATTR}]`)
    : null
  const panelId = candidate?.getAttribute(PANEL_TRIGGER_ATTR)
  pendingClickOrigin = candidate?.isConnected && panelId
    ? { panelId, element: candidate }
    : null
}

/** Capture the return target, distinguishing a real click from a restore. */
export function capturePanelTriggerOrigin(panelId: PanelId): void {
  const active = document.activeElement
  const focusedCandidate = active instanceof HTMLElement
    ? active.closest<HTMLElement>(`[${PANEL_TRIGGER_ATTR}="${panelId}"]`)
    : null
  const clickedCandidate = pendingClickOrigin?.panelId === panelId
    ? pendingClickOrigin.element
    : null

  origin = clickedCandidate?.isConnected
    ? { panelId, element: clickedCandidate }
    : focusedCandidate?.isConnected
      ? { panelId, element: focusedCandidate }
      : null
  focusPanelOnOpen = clickedCandidate?.isConnected ? panelId : null
  pendingClickOrigin = null
}

export function hasPanelTriggerOrigin(panelId: PanelId): boolean {
  return origin?.panelId === panelId && origin.element.isConnected
}

/** Consume the one-shot signal that this open came from a real trigger click. */
export function consumePanelOpenFocus(panelId: PanelId): boolean {
  if (focusPanelOnOpen !== panelId) return false
  focusPanelOnOpen = null
  return true
}

export function focusPanelTriggerOrigin(panelId: PanelId): boolean {
  if (!hasPanelTriggerOrigin(panelId) || origin === null) return false
  origin.element.focus()
  return document.activeElement === origin.element
}
