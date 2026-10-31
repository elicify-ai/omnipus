// useLibraryAutoRefresh — the visibility/focus gate behind the Library's
// self-refresh (founder decision 2026-10-30, "Q8b A": the open Library
// refreshes itself on a timer). Until now the panel only refreshed when the
// user returned to the tab (useLibraryCrossTabRefresh, D-107's pull half) or
// through the WS library_changed frame — which agent file tools never send —
// so agent writes, shell commands, knowledge-base edits and outside changes
// left the open Library stale.
//
// R1 of that change: re-check every 10 s ONLY while the Library panel is open
// AND the tab is visible AND the window is focused; no requests while the
// panel is closed (its queries unmount) or the tab hidden/unfocused. The
// visibility+hasFocus pair is the same "is this window actually attended"
// test BrowserLiveView uses for input recovery; TanStack's own
// refetchIntervalInBackground would only pause on document.hidden, so a
// visible but unfocused window would keep polling without this gate.
//
// Consumer:
//   - LibraryExplorer: a setInterval tick that re-fetches only the shown
//     folder listing (or the workspace list at the root) and writes it with
//     setQueryData. The open file's content is deliberately never re-fetched
//     by the timer (epic #1517: an open file is not live-updated).
// The interval constant lives here so every consumer ticks on one cadence.

import { useEffect, useState } from 'react'

/** How often the open Library re-checks itself (R1). */
export const LIBRARY_AUTO_REFRESH_INTERVAL_MS = 10_000

function libraryAutoRefreshActive(): boolean {
  if (typeof window === 'undefined' || typeof document === 'undefined') return false
  return document.visibilityState === 'visible' && document.hasFocus()
}

/**
 * True while the browser tab is visible AND the window is focused — the
 * condition under which the Library's background re-checks may run. Stops
 * (false) the moment the tab is hidden or the window loses focus, and
 * resumes on focus/visibilitychange.
 */
export function useLibraryAutoRefresh(): boolean {
  const [active, setActive] = useState(libraryAutoRefreshActive)

  useEffect(() => {
    if (typeof window === 'undefined') return
    const update = () => setActive(libraryAutoRefreshActive())
    window.addEventListener('focus', update)
    window.addEventListener('blur', update)
    document.addEventListener('visibilitychange', update)
    return () => {
      window.removeEventListener('focus', update)
      window.removeEventListener('blur', update)
      document.removeEventListener('visibilitychange', update)
    }
  }, [])

  return active
}
