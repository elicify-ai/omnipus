// useSwipeToClose.ts — SP-26's swipe-to-close recognizer for the phone
// takeover. From a touch that STARTS inside the 24px left edge zone, a
// rightward drag closes the panel on release when it travels >= 96px, or
// when it is flicked (velocity >= 0.4px/ms) after >= 48px of travel. It
// must NEVER fire when the drag starts on horizontally-scrollable content
// (SP-26's own exclusion — a swipe over a scrollable row is a scroll).
// During the drag the panel tracks the finger 1:1 (translateX), released
// cleanly on settle or cancel.

import { useEffect, useRef } from 'react'
import type { RefObject } from 'react'

export const SWIPE_EDGE_ZONE_PX = 24
export const SWIPE_CLOSE_DISTANCE_PX = 96
export const SWIPE_VELOCITY_MIN_TRAVEL_PX = 48
export const SWIPE_CLOSE_VELOCITY_PX_PER_MS = 0.4

interface SwipeTrack {
  identifier: number
  startX: number
  startY: number
  startTime: number
  lastX: number
  lastTime: number
  active: boolean
}

/** True when `target` (or any ancestor up to `root`) scrolls horizontally. */
export function startsInHorizontallyScrollable(
  target: Element | null,
  root: Element,
): boolean {
  let node: Element | null = target
  while (node !== null && node !== root) {
    if (node.scrollWidth > node.clientWidth + 1) {
      const ox = window.getComputedStyle(node).overflowX
      if (ox === 'auto' || ox === 'scroll') return true
    }
    node = node.parentElement
  }
  return false
}

/**
 * Attaches SP-26's touch recognizer to the takeover panel root. Returns the
 * ref to spread on the panel column element. `onClose` fires at most once
 * per completed gesture.
 */
export function useSwipeToClose(options: {
  enabled: boolean
  onClose: () => void
}): RefObject<HTMLDivElement | null> {
  const { enabled, onClose } = options
  const panelRef = useRef<HTMLDivElement | null>(null)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose

  useEffect(() => {
    const panel = panelRef.current
    if (!enabled || panel === null) return

    let track: SwipeTrack | null = null

    const onStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) return
      const t = e.touches[0]
      if (t === null) return
      const rect = panel.getBoundingClientRect()
      // The gesture only exists inside the 24px edge zone of the panel.
      if (t.clientX - rect.left > SWIPE_EDGE_ZONE_PX) return
      if (startsInHorizontallyScrollable(t.target as Element | null, panel)) return
      track = {
        identifier: t.identifier,
        startX: t.clientX,
        startY: t.clientY,
        startTime: performance.now(),
        lastX: t.clientX,
        lastTime: performance.now(),
        active: false,
      }
    }

    const onMove = (e: TouchEvent) => {
      if (track === null) return
      const t = Array.from(e.touches).find((c) => c.identifier === track?.identifier)
      if (t === undefined) return
      const dx = t.clientX - track.startX
      const dy = t.clientY - track.startY
      if (!track.active) {
        // Direction lock: rightward intent must dominate before the panel
        // starts tracking — a vertical or leftward touch is not a swipe.
        if (dx <= 0 || Math.abs(dy) > Math.abs(dx)) return
        track.active = true
      }
      if (e.cancelable) e.preventDefault()
      const travel = Math.max(dx, 0)
      track.lastX = t.clientX
      track.lastTime = performance.now()
      panel.style.transform = `translateX(${travel}px)`
    }

    const finish = (e: TouchEvent) => {
      if (track === null) return
      // The lifted finger lives in changedTouches, not touches.
      const last = Array.from(e.changedTouches).find((c) => c.identifier === track?.identifier)
      if (last === undefined) return
      const dx = last.clientX - track.startX
      const travel = Math.max(dx, 0)
      const elapsed = Math.max(performance.now() - track.startTime, 1)
      const velocity = travel / elapsed
      const wasActive = track.active
      panel.style.transform = ''
      track = null
      if (!wasActive) return
      const shouldClose =
        travel >= SWIPE_CLOSE_DISTANCE_PX ||
        (travel >= SWIPE_VELOCITY_MIN_TRAVEL_PX && velocity >= SWIPE_CLOSE_VELOCITY_PX_PER_MS)
      if (shouldClose) onCloseRef.current()
    }

    const onCancel = () => {
      if (track === null) return
      panel.style.transform = ''
      track = null
    }

    panel.addEventListener('touchstart', onStart, { passive: true })
    panel.addEventListener('touchmove', onMove, { passive: false })
    panel.addEventListener('touchend', finish)
    panel.addEventListener('touchcancel', onCancel)
    return () => {
      panel.removeEventListener('touchstart', onStart)
      panel.removeEventListener('touchmove', onMove)
      panel.removeEventListener('touchend', finish)
      panel.removeEventListener('touchcancel', onCancel)
    }
  }, [enabled])

  return panelRef
}
