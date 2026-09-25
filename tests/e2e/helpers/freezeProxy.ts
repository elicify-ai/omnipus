/**
 * freezeProxy.ts — BE-DESIGN.md §8.3 scenario b ("Dead connection about 60s,
 * server still thinks attached"): routes the page's WebSocket through
 * Playwright's `page.routeWebSocket` so traffic can be FROZEN (silently
 * stop forwarding both directions) without ever closing the socket — the
 * browser's WebSocket object stays `OPEN`, `navigator.onLine` stays true,
 * and the server never sees a close frame either. This is different from
 * every other outage helper in this repo (`context.setOffline`,
 * `browser-input-timing.ts`'s throttling, or a real network drop): those
 * all eventually surface as a close/error event on one side or the other.
 * A frozen proxy surfaces as NOTHING — the exact "gateway still thinks this
 * tab is attached, but nothing arrives" case BE-DESIGN.md's per-connection
 * queue overflow / close-4008 path (§2.1/§6.7) exists to recover from, and
 * the case scenario (b) needs to prove: a NEW connection (opened by
 * reloading, or by the browser itself eventually deciding the old one is
 * dead) can catch up while the frozen one is still nominally bound.
 *
 * Usage:
 *
 *   const freeze = await installFreezeProxy(page)
 *   // ... drive the app normally; the real WS connects through this proxy ...
 *   await freeze.freeze()      // traffic silently stops both ways
 *   // ... wait, assert the UI shows nothing (or the phase-1 quiet states) ...
 *   await freeze.unfreeze()    // traffic resumes (rarely needed — most
 *                              // scenarios instead open a SECOND connection
 *                              // while the frozen one is still "bound")
 *
 * Implementation note: `page.routeWebSocket` intercepts the WebSocket at
 * the Playwright/CDP layer, on the browser side, not a real network proxy —
 * it is a same-process interception, not a separate proxy server. That is
 * sufficient for this design's purposes: the gateway (the real server side)
 * still has a live TCP connection with data simply not arriving in either
 * direction while frozen, which is exactly what "server still thinks
 * attached" requires.
 */

import type { Page, WebSocketRoute } from '@playwright/test'

export interface FreezeProxyHandle {
  /** Stop forwarding frames in BOTH directions. The socket stays open. */
  freeze(): Promise<void>
  /** Resume forwarding frames in both directions. */
  unfreeze(): Promise<void>
  /** True while frozen. */
  isFrozen(): boolean
}

/**
 * Installs a WebSocket route that transparently forwards to the real
 * gateway connection until `freeze()` is called, matching any WS URL path
 * (the app connects to a single `/ws` endpoint — see `src/lib/ws.ts`'s
 * `getWsUrl()`). Must be called BEFORE the page navigates/connects, since
 * `routeWebSocket` only affects WebSockets created after it is registered.
 */
export async function installFreezeProxy(page: Page): Promise<FreezeProxyHandle> {
  // Real-browser follow-up (orchestrator, scenario b): `page.routeWebSocket`
  // registers ONE handler that fires again for every NEW WebSocket the page
  // creates — including the one `page.reload()` opens. A single, closure-
  // shared `frozen` boolean therefore froze every future connection too,
  // not just the one that was live when `freeze()` was called. Scenario b's
  // whole point is a NEW connection (opened by a reload) catching up while
  // the OLD one stays frozen — but since `unfreeze()` is never called
  // before that reload, the new connection silently inherited the freeze
  // and never received a single frame, which is exactly why the rebuilt
  // page showed zero messages forever (CI: `toHaveCount(1)` received `0`
  // for the full 60s timeout, every retry).
  //
  // Fixed by giving each connection ITS OWN `frozen` flag, private to that
  // invocation of the routeWebSocket callback. `freeze()`/`unfreeze()`
  // operate on whichever connection is CURRENTLY live (via
  // `currentFrozenSetter`, reassigned every time a new WebSocket is
  // established) — a reload's brand-new connection gets a fresh `frozen =
  // false` that nothing has touched, exactly matching "the new connection
  // ... while the frozen one is still nominally bound".
  let currentFrozenSetter: ((v: boolean) => void) | null = null
  let currentlyFrozen = false
  let activeRoute: WebSocketRoute | null = null

  await page.routeWebSocket(/.*/, (ws) => {
    const server = ws.connectToServer()
    activeRoute = ws
    let frozen = false
    currentFrozenSetter = (v: boolean) => {
      frozen = v
    }

    ws.onMessage((message) => {
      if (frozen) return // drop silently — the whole point of a freeze
      server.send(message)
    })
    server.onMessage((message) => {
      if (frozen) return
      ws.send(message)
    })
    // A real close on EITHER side must still propagate when not frozen —
    // freezing only suppresses DATA frames, never the close handshake
    // itself, so tests can still cleanly tear down.
    ws.onClose((code, reason) => server.close({ code, reason }))
    server.onClose((code, reason) => ws.close({ code, reason }))
  })

  return {
    async freeze() {
      currentlyFrozen = true
      currentFrozenSetter?.(true)
    },
    async unfreeze() {
      currentlyFrozen = false
      currentFrozenSetter?.(false)
      void activeRoute // kept for future direct-route inspection needs
    },
    isFrozen() {
      return currentlyFrozen
    },
  }
}
