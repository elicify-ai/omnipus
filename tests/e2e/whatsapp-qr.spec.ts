/**
 * whatsapp-qr.spec.ts — E2E UI regression for the WhatsApp QR pairing feature.
 *
 * Covers:
 *   Case A — QR renders end-to-end (positive path, #283/#298):
 *     A `whatsapp_pairing` frame with status:'code' arriving over the WS causes
 *     the Configure WhatsApp panel to render the QR code in the DOM
 *     ([data-testid="whatsapp-qr"] containing an <svg> and "Link a Device" text).
 *
 *   Case B — Capability gating (negative path, #299 regression):
 *     When the channels API reports native_available:false for WhatsApp, the
 *     unavailable hint renders ([data-testid="native-unavailable-hint"]) and NO
 *     QR is rendered. There is no `use_native` toggle anymore (WhatsApp is always
 *     native); the gating is purely on the native_available capability flag.
 *
 * Approach: fully deterministic, no real WhatsApp connection.
 *   - page.route() stubs all REST calls the panel makes (channels list, config,
 *     routing, agents) so no gateway state is required.
 *   - page.routeWebSocket() (Playwright 1.49+) fully mocks the WS transport.
 *     On the client→server subscribe frame the mock immediately injects the
 *     pairing QR frame back to the page. The QR payload is a test sentinel that
 *     must NOT be scanned ("E2E_TEST_QR_PAYLOAD_DO_NOT_SCAN").
 *
 * Why we use the base @playwright/test `test` rather than the console-errors
 * fixture wrapper:
 *   The console-errors fixture's cancelOnTeardown auto-fixture is harmless here
 *   but the consoleErrors assertion fires after each test. A fully-mocked WS
 *   does not receive an auth-ack from a real server; depending on SPA internals
 *   this may log a WS/validation warning that the allowlist already covers (see
 *   CONSOLE_ERROR_ALLOWLIST in fixtures/console-errors.ts which allows
 *   /WebSocket.*reconnect/i). To avoid false-positive failures on benign WS
 *   messages from a test-only mocked transport, we use the base test import and
 *   let individual assertions be the source of truth. If the console-errors
 *   fixture is later extended to allowlist WS-mock warnings, this spec can be
 *   migrated to use it.
 *
 * Traces to:
 *   - GitHub issue #283 — WhatsApp QR pairing SPA rendering
 *   - GitHub issue #298 — WhatsApp QR pairing feature PR
 *   - GitHub issue #299 — capability gating regression (native_available:false)
 *   - CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { Route, WebSocketRoute } from '@playwright/test'
import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Shared stub data ──────────────────────────────────────────────────────────

/** Minimal WhatsApp channel entry, native_available=true, enabled=true (case A). */
const WHATSAPP_CHANNEL_NATIVE_AVAILABLE = {
  id: 'whatsapp',
  name: 'WhatsApp',
  transport: 'bridge',
  enabled: true,
  description: 'WhatsApp channel',
  native_available: true,
}

/** WhatsApp channel entry with native_available=false (case B). */
const WHATSAPP_CHANNEL_NATIVE_UNAVAILABLE = {
  ...WHATSAPP_CHANNEL_NATIVE_AVAILABLE,
  native_available: false,
}

/** WhatsApp channel entry, native_available=true but enabled=false (case C). */
const WHATSAPP_CHANNEL_DISABLED = {
  ...WHATSAPP_CHANNEL_NATIVE_AVAILABLE,
  enabled: false,
}

/** A second channel so the list is never empty. */
const WEBCHAT_CHANNEL = {
  id: 'webchat',
  name: 'Web Chat',
  transport: 'websocket',
  enabled: true,
  description: 'Built-in web chat',
}

/**
 * Minimal channel config. WhatsApp is always native now (no `use_native` field);
 * the QR notice mounts based purely on the channelId + native_available, not on
 * any config value, so an empty object is sufficient.
 */
const WHATSAPP_CONFIG_NATIVE = {}

/** Minimal routing response (no default agent configured). */
const EMPTY_ROUTING = {}

/** Minimal agents list (non-empty so routing section renders). */
const AGENTS_LIST = [{ id: 'mia', name: 'Mia' }]

/** The injected QR payload — a static sentinel, never valid for scanning. */
const TEST_QR_PAYLOAD = '2@E2E_TEST_QR_PAYLOAD_DO_NOT_SCAN'

// ── REST stub helper ──────────────────────────────────────────────────────────

/**
 * Register page.route() stubs for all REST calls the ChannelConfigPanel makes
 * when opened for WhatsApp.
 *
 * @param nativeAvailable Controls the native_available field on the WhatsApp
 *   channel entry. Pass true for case A/C, false for case B.
 * @param channelEnabled Controls the enabled field on the WhatsApp channel entry.
 *   Defaults to true (cases A/B). Pass false for case C.
 */
async function stubChannelsRest(
  page: import('@playwright/test').Page,
  nativeAvailable: boolean,
  channelEnabled = true,
): Promise<void> {
  const base = nativeAvailable
    ? WHATSAPP_CHANNEL_NATIVE_AVAILABLE
    : WHATSAPP_CHANNEL_NATIVE_UNAVAILABLE
  const whatsappEntry = channelEnabled ? base : { ...base, enabled: false }

  // GET /api/v1/channels — channel list (feeds the Channels screen cards)
  await page.route('**/api/v1/channels', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([whatsappEntry, WEBCHAT_CHANNEL]),
      })
    } else {
      await route.continue()
    }
  })

  // GET /api/v1/channels/whatsapp — channel config. The QR block no longer keys
  // off any config field (use_native was removed); it mounts purely from the
  // whatsapp channelId + the native_available capability flag. In case B the
  // gating respects native_available:false regardless of config contents.
  await page.route('**/api/v1/channels/whatsapp', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(WHATSAPP_CONFIG_NATIVE),
      })
    } else {
      await route.continue()
    }
  })

  // GET /api/v1/channels/whatsapp/routing — default agent routing (Routing section)
  await page.route('**/api/v1/channels/whatsapp/routing', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(EMPTY_ROUTING),
      })
    } else {
      await route.continue()
    }
  })

  // GET /api/v1/agents — agent list for the Routing SmartSelect
  await page.route('**/api/v1/agents', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(AGENTS_LIST),
      })
    } else {
      await route.continue()
    }
  })
}

// ── WS mock helper ────────────────────────────────────────────────────────────

/**
 * Register page.routeWebSocket() to fully mock the SPA's WS connection.
 *
 * The mock:
 * 1. Accepts the connection (the SPA's `isConnected` is set on ws.onopen).
 * 2. Listens for client→server messages.
 * 3. When it sees a `whatsapp_pairing_subscribe` with active:true, it
 *    immediately injects a `whatsapp_pairing` frame with status:'code' and
 *    the test sentinel QR payload.
 * 4. All other client frames (auth, ping, …) are silently ignored — the SPA
 *    does not require acks for these to function on the Channels screen.
 *
 * The WS URL pattern matches the SPA's /api/v1/chat/ws regardless of host
 * or port so it works with any OMNIPUS_URL value.
 */
async function mockWebSocket(page: import('@playwright/test').Page): Promise<void> {
  await page.routeWebSocket(/api\/v1\/chat\/ws/, (ws: WebSocketRoute) => {
    ws.onMessage((raw: string | Buffer) => {
      let parsed: { type?: string; active?: boolean } = {}
      try {
        parsed = JSON.parse(typeof raw === 'string' ? raw : raw.toString()) as {
          type?: string
          active?: boolean
        }
      } catch {
        return // silently ignore non-JSON frames
      }

      // Respond to the pairing subscribe with the QR frame.
      if (parsed.type === 'whatsapp_pairing_subscribe' && parsed.active === true) {
        ws.send(
          JSON.stringify({
            type: 'whatsapp_pairing',
            channel_id: 'whatsapp_native',
            status: 'code',
            qr: TEST_QR_PAYLOAD,
            message: '',
          }),
        )
      }
      // auth, ping, and other client→server frames require no response.
    })
  })
}

// ── Case A: QR renders end-to-end ────────────────────────────────────────────
// BDD: Given the SPA is on the Channels screen
//      And the channels API reports native_available:true for WhatsApp
//      And the channel config has use_native:true
//      When the user opens the Configure WhatsApp panel
//      And the WS delivers a whatsapp_pairing frame with status:'code'
//      Then [data-testid="whatsapp-qr"] is visible in the DOM
//      And the QR container contains an <svg> element
//      And the text "Link a Device" is visible
//
// Traces to: whatsapp-qr.spec.ts (this file) — case A; GitHub #283/#298

test(
  '(A) whatsapp_pairing QR frame renders the QR in the Configure panel (native_available:true)',
  async ({ page }) => {
    // Register the WS mock BEFORE navigation so it is in place when the SPA
    // first opens the socket. REST stubs must also be in place before goto().
    await mockWebSocket(page)
    await stubChannelsRest(page, /* nativeAvailable */ true)

    // Navigate to the Channels screen. A single goto() after routes are set is
    // deterministic and keeps the storageState auth token applied (same pattern
    // as channels-routing.spec.ts).
    await page.goto(`${BASE_URL}/#/channels`)
    await expect(page).toHaveURL(/channels/, { timeout: 10_000 })

    // Wait for the WhatsApp card to appear.
    const configureBtn = page.getByRole('button', { name: /configure whatsapp/i })
    await expect(configureBtn).toBeVisible({ timeout: 15_000 })
    await configureBtn.click()

    // ChannelConfigPanel opens in a Radix Sheet ([role="dialog"]).
    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 10_000 })
    await expect(sheet).toContainText(/configure whatsapp/i)

    // ── Core assertion: the QR container must be visible.
    // WhatsAppNativeNotice sends the subscribe frame when isConnected goes true
    // (on WS open). The mock responds immediately. React re-renders on the next
    // store update. Allow up to 10 s for the full round-trip to land in the DOM.
    const qrContainer = page.getByTestId('whatsapp-qr')
    await expect(qrContainer).toBeVisible({ timeout: 10_000 })

    // ── Differentiation assertions: must render real QR content, not a
    //    placeholder. A stub that returns an empty div would pass a bare
    //    toBeVisible() — these assertions catch that.

    // The QR container must contain an <svg> produced by qrcode.react.
    const svg = qrContainer.locator('svg')
    await expect(svg).toHaveCount(1)

    // The "Link a Device" instructions must be visible — proves the 'code'
    // status branch rendered, not the 'waiting' branch. (Copy updated in #325:
    // the QR step text is now "Settings → Linked Devices → Link a Device".)
    await expect(page.getByText('Link a Device', { exact: false })).toBeVisible({ timeout: 5_000 })

    // ── Negative assertion: the unavailable hint must NOT appear in this case.
    await expect(page.getByTestId('native-unavailable-hint')).toHaveCount(0)
  },
)

// ── Case B: capability gating (native_available:false) ───────────────────────
// BDD: Given the SPA is on the Channels screen
//      And the channels API reports native_available:false for WhatsApp
//      When the user opens the Configure WhatsApp panel
//      Then [data-testid="native-unavailable-hint"] is visible
//      And [data-testid="whatsapp-qr"] does NOT exist in the DOM
//
// Note: there is no `use_native` toggle anymore — WhatsApp is always native and
// the field was removed. The gating is purely capability-driven: native_available
// :false → render the hint, suppress the QR.
//
// Regression guard for #299: if the gating check is removed, the QR would
// render even on builds that cannot support native WhatsApp, confusing users.
//
// Traces to: whatsapp-qr.spec.ts (this file) — case B; GitHub #299

test(
  '(B) native_available:false shows the capability hint and does NOT render the QR (no toggle)',
  async ({ page }) => {
    // We still mock the WS so that the SPA's isConnected path works normally
    // (avoids spurious reconnect warnings) — but no QR frame should be
    // requested because the gating prevents WhatsAppNativeNotice from mounting.
    await mockWebSocket(page)
    await stubChannelsRest(page, /* nativeAvailable */ false)

    await page.goto(`${BASE_URL}/#/channels`)
    await expect(page).toHaveURL(/channels/, { timeout: 10_000 })

    const configureBtn = page.getByRole('button', { name: /configure whatsapp/i })
    await expect(configureBtn).toBeVisible({ timeout: 15_000 })
    await configureBtn.click()

    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 10_000 })
    await expect(sheet).toContainText(/configure whatsapp/i)

    // ── Core assertion A: the capability hint is visible.
    // The hint is shown (in place of the QR notice) only after the config loads
    // and the panel renders; waiting on it also covers the skeleton resolving.
    const unavailableHint = page.getByTestId('native-unavailable-hint')
    await expect(unavailableHint).toBeVisible({ timeout: 10_000 })

    // ── Core assertion B: no QR container in the DOM.
    // A hardcoded or ungated implementation would render the QR even when
    // native_available:false — this assertion catches that regression.
    await expect(page.getByTestId('whatsapp-qr')).toHaveCount(0)
  },
)

// ── Case C: channel not yet enabled (enable-prompt UX fix) ───────────────────
// BDD: Given the SPA is on the Channels screen
//      And the channels API reports native_available:true for WhatsApp
//      But the channel entry has enabled:false
//      When the user opens the Configure WhatsApp panel
//      Then [data-testid="whatsapp-enable-prompt"] is visible in the DOM
//      And [data-testid="whatsapp-qr"] does NOT exist in the DOM
//
// Rationale: whatsmeow only generates a QR code after the channel is enabled.
// Opening the panel on a disabled channel previously started a 15-second countdown
// that always ended in "QR code timed out — click Retry" with no guidance.
// This case validates the UX fix: show an informational prompt instead.
//
// Traces to: whatsapp-qr.spec.ts (this file) — case C

test(
  '(C) enabled:false shows the enable-prompt and does NOT render the QR',
  async ({ page }) => {
    // Mock the WS so isConnected works normally; no QR frame should be requested
    // because WhatsAppNativeNotice is not mounted when the channel is disabled.
    await mockWebSocket(page)
    await stubChannelsRest(page, /* nativeAvailable */ true, /* channelEnabled */ false)

    await page.goto(`${BASE_URL}/#/channels`)
    await expect(page).toHaveURL(/channels/, { timeout: 10_000 })

    const configureBtn = page.getByRole('button', { name: /configure whatsapp/i })
    await expect(configureBtn).toBeVisible({ timeout: 15_000 })
    await configureBtn.click()

    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 10_000 })
    await expect(sheet).toContainText(/configure whatsapp/i)

    // ── Core assertion A: the enable-prompt is visible.
    // Waits for the config skeleton to resolve and the panel to render its
    // WhatsApp section — which must show the prompt, not the QR notice.
    const enablePrompt = page.getByTestId('whatsapp-enable-prompt')
    await expect(enablePrompt).toBeVisible({ timeout: 10_000 })
    await expect(enablePrompt).toContainText(/save.*enable whatsapp/i)

    // ── Core assertion B: no QR container in the DOM.
    // If the gating is removed, WhatsAppNativeNotice mounts and the QR timeout
    // fires. This assertion ensures the notice is suppressed for disabled channels.
    await expect(page.getByTestId('whatsapp-qr')).toHaveCount(0)

    // ── Negative assertion: the capability-unavailable hint must NOT appear.
    // Case C is about enabled:false, not native_available:false — both hints
    // must not coexist.
    await expect(page.getByTestId('native-unavailable-hint')).toHaveCount(0)
  },
)
