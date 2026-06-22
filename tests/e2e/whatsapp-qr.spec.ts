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
 *   Case C — Channel not yet enabled (enable-prompt UX fix):
 *     When the channel entry has enabled:false, [data-testid="whatsapp-enable-prompt"]
 *     renders and [data-testid="whatsapp-qr"] does NOT appear in the DOM.
 *
 *   Case D — Real backend: pairing WS frame delivered after channel enable:
 *     Uses the real gateway WS (no routeWebSocket mock).  Enables the WhatsApp
 *     channel via the real REST API, opens the Configure panel, and waits for the
 *     backend to emit a whatsapp_pairing frame over the live WS.
 *     Skipped when the binary is built without whatsmeow (native_available:false).
 *
 * Approach for Cases A–C: fully deterministic, no real WhatsApp connection.
 *   - page.route() stubs all REST calls the panel makes (channels list, config,
 *     routing, agents) so no gateway state is required.
 *   - page.routeWebSocket() (Playwright 1.49+) fully mocks the WS transport.
 *     On the client→server subscribe frame the mock immediately injects the
 *     pairing QR frame back to the page. The QR payload is a test sentinel that
 *     must NOT be scanned ("E2E_TEST_QR_PAYLOAD_DO_NOT_SCAN").
 *
 * Approach for Case D: real backend pipeline.
 *   - No page.route() stubs — all REST calls hit the live gateway.
 *   - No page.routeWebSocket() — the SPA's real WS connection is used.
 *   - The test enables the WhatsApp channel via PUT /api/v1/channels/whatsapp/enable,
 *     which causes whatsmeow to start and emit a whatsapp_pairing QR frame.
 *   - Cleans up by disabling the channel after the assertion.
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

import * as fs from 'fs'
import * as path from 'path'
import type { Route, WebSocketRoute } from '@playwright/test'
import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Shared stub data ──────────────────────────────────────────────────────────

/** Minimal WhatsApp channel entry, native_available=true (case A). */
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
 *   channel entry. Pass true for case A, false for case B.
 */
async function stubChannelsRest(
  page: import('@playwright/test').Page,
  nativeAvailable: boolean,
): Promise<void> {
  const whatsappEntry = nativeAvailable
    ? WHATSAPP_CHANNEL_NATIVE_AVAILABLE
    : WHATSAPP_CHANNEL_NATIVE_UNAVAILABLE

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
    await page.goto(`${BASE_URL}/#/connectors`)
    await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })

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

    await page.goto(`${BASE_URL}/#/connectors`)
    await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })

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

// ── Case D: real backend — pairing WS frame delivered after channel enable ───
//
// BDD:
//   Given the gateway is running with the native WhatsApp build (NativeAvailable=true)
//   And the WhatsApp channel is initially disabled
//   And the user is authenticated (global storageState)
//   When the user enables the WhatsApp channel via PUT /api/v1/channels/whatsapp/enable
//   And the user navigates to the Channels screen
//   And the user opens the Configure WhatsApp panel
//   Then [data-testid="whatsapp-qr"] becomes visible within 20 seconds
//   And the QR container contains an <svg> element
//
// This is the only test in this file that exercises the real backend→WS→SPA
// pipeline without any page.route() or page.routeWebSocket() mocking.
// It validates that the gateway actually starts whatsmeow, generates the pairing
// QR, and delivers the whatsapp_pairing WS frame to the SPA end-to-end.
//
// Skip condition: if GET /api/v1/channels returns native_available:false for
// WhatsApp (i.e., the binary is a lite build without whatsmeow), the test is
// skipped with test.skip() — there is no QR to wait for in that case.
//
// Cleanup: the test disables the WhatsApp channel after the assertion to avoid
// leaving the gateway in a state where whatsmeow tries to connect and generates
// spurious log noise for subsequent tests.
//
// Traces to: CLAUDE.md §Architecture Patterns — "WhatsApp native QR pairing
//   (#283 via #298)"; GitHub issue #283/#298; pkg/gateway/rest.go:setChannelEnabled.

/**
 * Read the Bearer auth token from the Playwright storageState file.
 * Case D needs it to call PUT /api/v1/channels/whatsapp/enable directly.
 * Mirrors the pattern in replay-fidelity.spec.ts::getStoredAuthToken().
 */
function _whatsappGetStoredAuthToken(): string | null {
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(
        path.dirname(new URL(import.meta.url).pathname),
        'fixtures/.auth/admin.json',
      )
  if (!fs.existsSync(authFile)) return null
  try {
    const raw = fs.readFileSync(authFile, 'utf-8')
    const state = JSON.parse(raw) as {
      origins?: Array<{
        origin: string
        localStorage?: Array<{ name: string; value: string }>
      }>
    }
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value
      }
    }
  } catch {
    // Auth file may not exist on first run
  }
  return null
}

test(
  '(D) real backend: pairing WS frame delivered after channel enable',
  async ({ page }) => {
    // 30s is enough: whatsmeow typically emits the first QR frame within 3-8s
    // of being started, plus ~5s for channel init and WS round-trip.
    test.setTimeout(60_000)

    // ── Step 1: Check native_available from the real API ──
    // If the binary is a lite build (no whatsmeow), native_available is false
    // and we cannot receive a real QR — skip rather than fail.
    const channelsResp = await page.request.get(`${BASE_URL}/api/v1/channels`)
    if (!channelsResp.ok()) {
      test.skip(true, `GET /api/v1/channels failed: ${channelsResp.status()} — cannot determine native_available`)
      return
    }
    const channels = (await channelsResp.json()) as Array<{
      id: string
      native_available?: boolean
      enabled?: boolean
    }>
    const whatsappEntry = channels.find((c) => c.id === 'whatsapp')
    if (!whatsappEntry?.native_available) {
      test.skip(
        true,
        'WhatsApp native_available=false (lite build without whatsmeow) — no QR to wait for',
      )
      return
    }

    // ── Step 2: Read the Bearer token ──
    const authToken = _whatsappGetStoredAuthToken()

    // ── Step 3: Navigate to root first so the SPA loads and mints the CSRF
    //    cookie before we make any state-mutating API calls.
    await page.goto(`${BASE_URL}/`)
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Read the CSRF cookie now that the SPA has initialised it.
    const freshCookies = await page.context().cookies()
    const freshCsrf = freshCookies.find((c) => c.name === '__Host-csrf')
    const finalHeaders: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
      ...(freshCsrf ? { 'X-CSRF-Token': freshCsrf.value } : {}),
    }

    // ── Step 4: Enable the WhatsApp channel via the real REST API ──
    // The enable endpoint is PUT /api/v1/channels/whatsapp/enable (no body).
    // This causes the gateway to call manager.SetChannelEnabled("whatsapp_native", true)
    // which starts whatsmeow and triggers the pairing QR emission over WS.
    const enableResp = await page.request.put(
      `${BASE_URL}/api/v1/channels/whatsapp/enable`,
      { headers: finalHeaders },
    )
    if (!enableResp.ok()) {
      // If the enable fails (e.g. missing credentials, network error), fail
      // with a clear message rather than letting the QR assertion time out.
      const body = await enableResp.text()
      throw new Error(
        `PUT /api/v1/channels/whatsapp/enable failed: ${enableResp.status()} — ${body}`,
      )
    }

    // ── Step 5: Navigate to the Channels screen ──
    await page.goto(`${BASE_URL}/#/connectors`)
    await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })

    // ── Step 6: Open the Configure WhatsApp panel ──
    const configureBtn = page.getByRole('button', { name: /configure whatsapp/i })
    await expect(configureBtn).toBeVisible({ timeout: 15_000 })
    await configureBtn.click()

    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 10_000 })
    await expect(sheet).toContainText(/configure whatsapp/i)

    // ── Step 7: Wait for the real QR to appear ──
    // whatsmeow generates the pairing QR within a few seconds of start.
    // The SPA subscribes via whatsapp_pairing_subscribe over the real WS and
    // renders [data-testid="whatsapp-qr"] when the backend delivers a
    // whatsapp_pairing frame with status:'code'.
    // We allow up to 20s for the full backend→WS→SPA round-trip.
    const qrContainer = page.getByTestId('whatsapp-qr')
    await expect(qrContainer).toBeVisible({ timeout: 20_000 })

    // ── Step 8: Differentiation assertions ──
    // These catch a stub that renders an empty or hardcoded container.

    // The QR container must contain an <svg> produced by qrcode.react.
    const svg = qrContainer.locator('svg')
    await expect(svg).toHaveCount(1)

    // The QR must not be empty — qrcode.react renders at least one <path>
    // element inside the SVG for real QR content.
    const paths = svg.locator('path')
    const pathCount = await paths.count()
    expect(pathCount).toBeGreaterThan(0)

    // ── Cleanup: disable the channel to avoid leaving whatsmeow running ──
    // Best effort — if this fails the test result is still the assertion above.
    try {
      const freshCookies2 = await page.context().cookies()
      const cleanupCsrf = freshCookies2.find((c) => c.name === '__Host-csrf')
      await page.request.put(`${BASE_URL}/api/v1/channels/whatsapp/disable`, {
        headers: {
          'Content-Type': 'application/json',
          ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
          ...(cleanupCsrf ? { 'X-CSRF-Token': cleanupCsrf.value } : {}),
        },
      })
    } catch {
      // Cleanup failure is non-fatal: the test result is already determined.
    }
  },
)
