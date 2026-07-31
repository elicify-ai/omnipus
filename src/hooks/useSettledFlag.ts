import { useEffect, useRef, useState } from 'react'

/**
 * Returns `value` only once it has been continuously true for `delayMs`;
 * falls back to false the instant `value` goes false.
 *
 * Why this exists (operator report, 2026-07-31): a red "Disconnected —
 * reconnecting..." banner flashed in the top-left for a split second on a
 * connection that recovered immediately. The underlying WebSocket really had
 * dropped — the gateway logs 1006 abnormal closures on a long-haul path — but
 * a drop that self-heals in well under a second is not something a user can
 * act on, and rendering an ERROR-coloured banner for it reads as a fault when
 * the system is in fact doing exactly what it should.
 *
 * Debouncing the DISPLAY (never the reconnect logic itself, which must still
 * fire immediately) means a fast recovery is invisible, while a genuine
 * outage still surfaces after `delayMs`. Asymmetric on purpose: clearing is
 * immediate, so the banner vanishes the moment the connection is back rather
 * than lingering for another full delay.
 */
export function useSettledFlag(value: boolean, delayMs: number): boolean {
  const [settled, setSettled] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!value) {
      // Clear immediately — see the asymmetry note above.
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      setSettled(false)
      return undefined
    }
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      setSettled(true)
    }, delayMs)
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
  }, [value, delayMs])

  return settled
}
