/**
 * ws-reconnect.spec.ts — T1.12 + T1.13
 *
 * T1.12: visibilitychange_triggers_reconnect_with_persistent_banner
 *   Kill the WS by evaluating WebSocket close in the browser.
 *   Dispatch document visibilitychange (hidden → visible).
 *   Assert: reconnect banner is visible and persistent (not a transient toast
 *   that auto-dismisses within 5 s).
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

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── T1.12: visibilitychange triggers reconnect with persistent banner ─────────

test(
  'visibilitychange_triggers_reconnect_with_persistent_banner',
  async ({ page }) => {
    // Navigate to the chat screen and wait for initial connection.
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Wait for the chat input to be enabled (WS connected).
    const chatInput = page.locator('textarea').first()
    await expect(chatInput).toBeEnabled({ timeout: 15_000 })

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

    // Step 3: Assert the reconnect banner is visible.
    // The banner must be a persistent UI element (not a transient toast) so the
    // user can see the disconnected state without waiting for a toast to appear.
    //
    // This test is intentionally honest-red if the persistent banner doesn't exist.
    // The expected banner text is one of:
    //   - "Reconnecting..." / "Connecting..." / "Connection lost" / "Disconnected"
    // We use a broad locator to cover whatever text the implementation uses.
    //
    // If the banner doesn't exist at all, this assertion fails with a clear message.
    const reconnectBanner = page
      .locator('[data-testid="reconnect-banner"]')
      .or(page.locator('text=Reconnecting'))
      .or(page.locator('text=Connecting to gateway'))
      .or(page.locator('text=Connection lost'))
      .or(page.locator('text=Disconnected'))
      .first()

    // Wait up to 8 s for the banner to appear
    await expect(
      reconnectBanner,
      [
        'A persistent reconnect banner must be visible after the WS was killed.',
        'This is not a transient toast — it must persist until the connection is restored.',
        'If this fails, the persistent banner feature has not been implemented.',
      ].join(' '),
    ).toBeVisible({ timeout: 8_000 })

    // Step 4: Wait 5 s (the typical auto-dismiss window for toasts) and assert
    // the banner is STILL visible — confirming it is persistent, not transient.
    await page.waitForTimeout(5_500)

    await expect(
      reconnectBanner,
      [
        'The reconnect banner must still be visible after 5 s — it must not auto-dismiss like a toast.',
        'If this fails, the UI is using a toast instead of a persistent banner.',
      ].join(' '),
    ).toBeVisible()

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

    // Step 6: Assert the banner clears once the connection is restored.
    await expect(
      reconnectBanner,
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

    // Step 1: Set the browser context offline (network unavailable).
    // This will cause the existing WS to disconnect (TCP reset).
    await page.context().setOffline(true)

    // Wait briefly for the WS to detect the disconnect and the UI to update.
    // The chat input becomes disabled when isConnected=false.
    await expect(chatInput).toBeDisabled({ timeout: 10_000 })

    // Step 2: Restore network (setOffline=false) to simulate the device coming
    // back online. This fires the browser's 'online' event, which ws.ts should
    // listen to and use as a reconnect trigger.
    await page.context().setOffline(false)

    // Step 3: Assert the connection was restored.
    // When the 'online' event fires the reconnect, ws.ts re-establishes the WS
    // and isConnected flips to true, which re-enables the chat input.
    await expect(
      chatInput,
      [
        'Chat input must re-enable after network is restored via the `online` event.',
        'If this fails, ws.ts does not listen to `window.addEventListener("online", ...)`,',
        'or the reconnect triggered by the online event is not completing.',
      ].join(' '),
    ).toBeEnabled({ timeout: 20_000 })
  },
)
