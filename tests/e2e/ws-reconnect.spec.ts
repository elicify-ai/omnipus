/**
 * ws-reconnect.spec.ts — T1.12 + T1.13
 *
 * T1.12: visibilitychange_triggers_reconnect_without_short-drop_noise
 *   Kill the WS by evaluating WebSocket close in the browser.
 *   Dispatch document visibilitychange (hidden → visible).
 *   Assert: a short drop stays visually quiet while reconnect continues.
 *   Restore the connection (re-enable the route).
 *   Assert: banner clears once connected.
 *
 * T1.13: online_event_triggers_reconnect
 *   page.context().setOffline(true) then setOffline(false).
 *   Assert: reconnect happened on the `online` event (chat input re-enables).
 *
 * Both tests are honest-red: they verify behavior that depends on the WS
 * reconnect + persistent-banner implementation in ws.ts / the UI layer.
 * If the persistent banner doesn't exist yet, T1.12 will fail with a
 * descriptive message identifying exactly what's missing.
 *
 * Tests drive against the real embedded SPA (Go binary + Playwright).
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'

// ── T1.12: visibilitychange triggers reconnect with persistent banner ─────────

test(
  'visibilitychange_triggers_reconnect_without_short_drop_noise',
  async ({ page }) => {
    // Navigate to the chat screen and wait for initial connection.
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Wait for the chat input to be enabled (WS connected).
    const chatInput = page.locator('textarea').first()
    await expect(chatInput).toBeEnabled({ timeout: 15_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix):
    // the composer is also enabled while reconnectPhase is 'reconnecting' or
    // 'slow', so it can look ready immediately after page load even during a
    // transient first-connect blip. Confirm genuine connectivity — via the
    // absence of a connection-status line — so
    // the WebSocket-stubbing steps that follow start from a real, stable
    // connection rather than a mid-reconnect one.
    await expect(page.getByTestId('connection-status-line')).toBeHidden({ timeout: 15_000 })

    // Step 1a: Stub the WebSocket constructor so any reconnect attempt
    // produces a never-opens socket. page.route() does NOT intercept
    // WebSocket connections, so we patch the constructor in-page. The
    // override is reverted in step 5 by restoring the real constructor.
    await page.evaluate(() => {
      const w = window as unknown as {
        __real_WebSocket?: typeof WebSocket
        WebSocket: typeof WebSocket
      }
      w.__real_WebSocket = w.WebSocket
      class StubWebSocket {
        readyState = 0
        url: string
        onopen: ((ev: Event) => void) | null = null
        onclose: ((ev: CloseEvent) => void) | null = null
        onerror: ((ev: Event) => void) | null = null
        onmessage: ((ev: MessageEvent) => void) | null = null
        constructor(url: string) {
          this.url = url
        }
        send() {}
        close() {
          this.readyState = 3
          if (this.onclose) {
            const ev = new CloseEvent('close', { code: 1006, reason: 'stubbed' })
            this.onclose(ev)
          }
        }
        addEventListener() {}
        removeEventListener() {}
        dispatchEvent() { return true }
      }
      ;(StubWebSocket as unknown as typeof WebSocket & { CONNECTING: number; OPEN: number; CLOSING: number; CLOSED: number }).CONNECTING = 0
      ;(StubWebSocket as unknown as typeof WebSocket & { CONNECTING: number; OPEN: number; CLOSING: number; CLOSED: number }).OPEN = 1
      ;(StubWebSocket as unknown as typeof WebSocket & { CONNECTING: number; OPEN: number; CLOSING: number; CLOSED: number }).CLOSING = 2
      ;(StubWebSocket as unknown as typeof WebSocket & { CONNECTING: number; OPEN: number; CLOSING: number; CLOSED: number }).CLOSED = 3
      w.WebSocket = StubWebSocket as unknown as typeof WebSocket
    })

    // Step 1b: Kill the WebSocket connection from within the browser via the
    // ws.ts-exposed __ws_instances registry. This simulates a network drop
    // without actually disabling the network so the visibilitychange-driven
    // reconnect logic still fires (and is blocked by the WebSocket stub).
    await page.evaluate(() => {
      const wsList = (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances
      if (wsList) {
        for (const ws of wsList) {
          try { ws.close(1000, 'test-kill') } catch { /* ignore */ }
        }
      }
    })

    // Step 2: Simulate visibilitychange: hidden → visible.
    // This triggers the ws.ts reconnect logic (the SPA re-attaches on becoming visible).
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', {
        configurable: true,
        get() { return 'hidden' },
      })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', {
        configurable: true,
        get() { return 'visible' },
      })
      document.dispatchEvent(new Event('visibilitychange'))
    })

    // Step 3: #823 deliberately keeps drops shorter than 15 seconds quiet.
    const connectionStatus = page.getByTestId('connection-status-line')
    await expect(connectionStatus).toBeHidden()
    await page.waitForTimeout(5_500)
    await expect(connectionStatus).toBeHidden()

    // Step 5: Restore the real WebSocket constructor and force the in-flight
    // stub socket to close so the visibilitychange handler creates a fresh
    // (real) one.
    await page.evaluate(() => {
      const w = window as unknown as {
        __real_WebSocket?: typeof WebSocket
        WebSocket: typeof WebSocket
        __ws_instances?: WebSocket[]
      }
      if (w.__real_WebSocket) w.WebSocket = w.__real_WebSocket
      for (const ws of w.__ws_instances ?? []) {
        try { ws.close(1000, 'restore') } catch { /* ignore */ }
      }
      Object.defineProperty(document, 'visibilityState', { configurable: true, get() { return 'hidden' } })
      document.dispatchEvent(new Event('visibilitychange'))
      Object.defineProperty(document, 'visibilityState', { configurable: true, get() { return 'visible' } })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await expect(chatInput).toBeEnabled({ timeout: 20_000 })
    await expect.poll(async () => page.evaluate(() =>
      ((window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [])
        .some((ws) => ws.readyState === WebSocket.OPEN),
    ), { timeout: 20_000 }).toBe(true)

    // Step 6: Assert the banner clears once the connection is restored.
    await expect(
      connectionStatus,
    ).not.toBeVisible({ timeout: 10_000 })
  },
)

// ── T1.13: online event triggers reconnect ────────────────────────────────────

test(
  'online_event_triggers_reconnect',
  async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    const chatInput = page.locator('textarea').first()
    await expect(chatInput).toBeEnabled({ timeout: 15_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix)
    // — confirm the socket is genuinely open before this test deliberately
    // drops it below, so the subsequent "banner appears / composer stays
    // enabled" assertions measure a real transition rather than racing an
    // already-in-progress reconnect from page load.
    await expect(page.getByTestId('connection-status-line')).toBeHidden({ timeout: 15_000 })

    // Step 1: Set the browser context offline (network unavailable).
    // This will cause the existing WS to disconnect (TCP reset).
    await page.context().setOffline(true)

    // Wait briefly for the WS to detect the disconnect and the UI to update.
    // Detect the socket close directly. #823 intentionally draws no status
    // line for a drop shorter than 15 seconds.
    //
    // NOTE (offline send queue, #105): the chat input intentionally does NOT
    // become disabled here (a previous version of this test asserted
    // `toBeDisabled`). ChatScreen.tsx's `inputEnabled` allows typing/sending
    // while reconnectPhase is 'reconnecting' or 'slow' so a message composed
    // during a transient outage is buffered (useChatStore's outboundQueue)
    // and sent automatically once the connection recovers, instead of being
    // silently blocked — see tests/e2e/chat.spec.ts "(f) queue-on-disconnect"
    // for that behavior itself. This test only needs to verify the
    // disconnect is detected and that the `online` event drives recovery, so
    // it asserts on the banner instead, and additionally pins that the
    // composer stays enabled throughout (a regression that re-disabled it
    // during reconnect would break the queue feature silently).
    await expect.poll(async () => page.evaluate(() =>
      ((window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [])
        .every((ws) => ws.readyState !== WebSocket.OPEN),
    ), { timeout: 10_000 }).toBe(true)
    await expect(page.getByTestId('connection-status-line')).toBeHidden()
    await expect(chatInput, 'composer must stay usable during the reconnect-retry window (#105 offline queue)').toBeEnabled()

    // Step 2: Restore network (setOffline=false) to simulate the device coming
    // back online. This fires the browser's 'online' event, which ws.ts should
    // listen to and use as a reconnect trigger.
    await page.context().setOffline(false)

    // Step 3: Assert the connection was restored.
    // When the 'online' event fires the reconnect, ws.ts re-establishes the WS,
    // isConnected flips to true, and the reconnect banner clears.
    await expect.poll(async () => page.evaluate(() =>
      ((window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? [])
        .some((ws) => ws.readyState === WebSocket.OPEN),
    ), { timeout: 20_000 }).toBe(true)
    await expect(page.getByTestId('connection-status-line')).toBeHidden()
    await expect(chatInput).toBeEnabled()
  },
)
