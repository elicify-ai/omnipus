import type { PanelId } from './types'

export const PANEL_TRIGGER_ATTR = 'data-panel-trigger'

let origin: { panelId: PanelId; element: HTMLElement } | null = null

/** Capture only the real control that initiated this store open. */
export function capturePanelTriggerOrigin(panelId: PanelId): void {
  const active = document.activeElement
  const candidate = active instanceof HTMLElement
    ? active.closest<HTMLElement>(`[${PANEL_TRIGGER_ATTR}="${panelId}"]`)
    : null
  origin = candidate?.isConnected ? { panelId, element: candidate } : null
}

export function hasPanelTriggerOrigin(panelId: PanelId): boolean {
  return origin?.panelId === panelId && origin.element.isConnected
}

export function focusPanelTriggerOrigin(panelId: PanelId): boolean {
  if (!hasPanelTriggerOrigin(panelId) || origin === null) return false
  origin.element.focus()
  return document.activeElement === origin.element
}
