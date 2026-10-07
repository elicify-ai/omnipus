import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { useUiStore } from '@/store/ui'
import { STATUS_ORDER } from '@/lib/statusColors'

// Preserve the board's existing readable floor. Six equal 162px columns need
// 972px of content (borders are included by border-box); never use viewport width.
export const TASK_BOARD_MIN_COLUMN_PX = 162
export const TASK_BOARD_MIN_WIDTH_PX = STATUS_ORDER.length * TASK_BOARD_MIN_COLUMN_PX

// A transient finite request, not a persisted width. SidePanelShell already
// clamps stored requests to its live SP-17 ceiling, including the chat floor.
const MAX_DOCKED_REQUEST = Number.MAX_SAFE_INTEGER

export function useTasksBoardLayout(
  surfaceRef: RefObject<HTMLDivElement | null>,
  frameRef: RefObject<HTMLDivElement | null>,
  boardSelected: boolean,
  workspaceId: string,
) {
  const [frameWidth, setFrameWidth] = useState<number | null>(null)
  const previousWidth = useRef<number | null | undefined>(undefined)
  const requestBoardWidth = useCallback(() => {
    const docked = surfaceRef.current?.closest('[data-testid="side-panel"]')
    // Full-screen/phone takeover already own their available width. Never
    // change an unrelated docked instance through the global store.
    if (!docked || docked.getAttribute('data-takeover') === 'true') return
    const store = useUiStore.getState()
    if (store.activePanel?.id !== 'tasks') return
    if (previousWidth.current === undefined) previousWidth.current = store.panelWidth
    store.setPanelWidth(MAX_DOCKED_REQUEST)
  }, [surfaceRef])

  useEffect(() => {
    if (!boardSelected) return
    requestBoardWidth()
    return () => {
      const previous = previousWidth.current
      previousWidth.current = undefined
      if (previous === undefined) return
      const store = useUiStore.getState()
      // A different panel/close has already reset the width slice; do not
      // leak this Tasks instance's width into its replacement.
      if (store.activePanel?.id !== 'tasks') return
      if (previous === null) store.resetPanelWidth()
      else store.setPanelWidth(previous)
    }
  }, [boardSelected, workspaceId, requestBoardWidth])

  useEffect(() => {
    const frame = frameRef.current
    if (!frame) return
    const measure = (width: number) => {
      // Hidden/unmeasured frames have no usable geometry yet. Preserve the
      // last real measurement rather than turning a hidden mount into List.
      if (width > 0) setFrameWidth(width)
    }
    measure(frame.getBoundingClientRect().width)
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver((entries) => measure(entries[0]?.contentRect.width ?? 0))
    observer.observe(frame)
    return () => observer.disconnect()
  }, [frameRef])

  return { boardFits: frameWidth === null || frameWidth >= TASK_BOARD_MIN_WIDTH_PX, requestBoardWidth }
}
