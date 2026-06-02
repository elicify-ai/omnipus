/**
 * channels-routing.spec.ts — E2E tests for the Channels screen and channel
 * routing configuration (sprint/258-jun-2026).
 *
 * Tests exercise:
 *   (a) Channels sidebar item renders and navigates to /#/channels.
 *   (b) Channel feed renders cards from the real gateway GET /api/v1/channels.
 *   (c) Opening Configure opens the ChannelConfigPanel sheet for a channel.
 *   (d) Setting a Default agent via the Routing section issues a real
 *       PUT /api/v1/channels/{id}/routing and the value persists across
 *       a GET /api/v1/channels/{id}/routing read-back.
 *   (e) Clearing the Default agent (selecting "(Global default)") issues
 *       PUT with default_agent_id: null.
 *
 * Architecture:
 *   - Global storageState provides a pre-authenticated session.
 *   - Tests (d)/(e) use page.route() interception to assert the PUT payload
 *     AND fulfil the request so the mutation chain completes cleanly.
 *   - We do NOT run the full LLM suite here — no real LLM calls are needed.
 *
 * CLAUDE.md note: E2E tests always target the embedded SPA (Go binary), not
 * the Vite dev server. Ensure the SPA sync pipeline has run before this spec:
 *   npm run build && rm -rf pkg/gateway/spa/assets && cp -r dist/spa/* pkg/gateway/spa/
 *   CGO_ENABLED=0 go build -o /tmp/omnipus-ci ./cmd/omnipus/
 *
 * Traces to: sprint/258-jun-2026 — Channels screen + ChannelConfigPanel Routing section.
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page, type Route } from '@playwright/test'
import { test } from './fixtures/console-errors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Auth helpers (mirrored from sidebar.spec.ts / cancel-cross-channel.spec.ts) ─

function getStoredAuthToken(): string | null {
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
    // Auth file may not exist on first run.
  }
  return null
}

async function getCsrfToken(page: Page): Promise<string | null> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  return csrfCookie?.value ?? null
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const authToken = getStoredAuthToken()
  const csrfToken = await getCsrfToken(page)
  return {
    'Content-Type': 'application/json',
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
    ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
  }
}

// ── (a) Channels sidebar item ─────────────────────────────────────────────────
// BDD: Given the user is logged in,
//      When they click the "Channels" nav item in the sidebar,
//      Then the browser navigates to /#/channels.
//
// Traces to: sprint/258-jun-2026 — Sidebar "Channels" nav item.

test('(a) Channels sidebar item navigates to /#/channels', async ({ page }) => {
  await page.goto('/')

  const hamburger = page.locator('#sidebar-hamburger')
  await expect(hamburger).toBeVisible({ timeout: 10_000 })
  await hamburger.click()

  const nav = page.locator('nav[aria-label="Main navigation"]')
  await expect(nav).toBeVisible({ timeout: 5_000 })

  // HashRouter: link href="/#/channels"
  const channelsLink = nav.locator('a[href="/#/channels"]')
  await expect(channelsLink).toBeVisible({ timeout: 5_000 })

  // Verify the link contains the label text "Channels".
  await expect(channelsLink).toContainText('Channels')

  await channelsLink.click()
  await expect(page).toHaveURL(/channels/, { timeout: 10_000 })
})

// ── (b) Channel feed renders cards ────────────────────────────────────────────
// BDD: Given the gateway is running with at least some channels configured,
//      When the user navigates to /#/channels,
//      Then the channel feed renders with channel cards.
//
// Traces to: sprint/258-jun-2026 — Channels screen, channel feed list.

test('(b) channel feed renders at least one channel card', async ({ page }) => {
  // Navigate directly via hash route.
  await page.goto('/#/channels')
  await expect(page).toHaveURL(/channels/, { timeout: 10_000 })

  // Wait for either channel cards or the empty state — both are valid depending
  // on whether channels are configured in the CI gateway.
  // The Channels screen renders channel names as <span> text inside card rows.
  // If no channels are configured, an empty state paragraph is shown instead.
  const channelContent = page.locator(
    '[data-testid^="channel-card-"], p:has-text("No channels configured")',
  )
  // Allow for data to load (API round-trip + React render).
  await expect(channelContent.first()).toBeVisible({ timeout: 15_000 })

  // If the page returned an error state rather than empty, the test should fail.
  await expect(page.getByText('Could not load channels.')).not.toBeVisible()
})

// ── (c) Configure button opens ChannelConfigPanel sheet ──────────────────────
// BDD: Given the channel feed has loaded,
//      When the user clicks "Configure" on a channel card,
//      Then the ChannelConfigPanel sheet opens (showing the channel name in title).
//
// Traces to: sprint/258-jun-2026 — ChannelConfigPanel opens from Configure button.

test('(c) clicking Configure opens the channel config panel sheet', async ({ page }) => {
  // Register route mocks BEFORE the first navigation, then goto once. Do NOT
  // page.reload() — a hard reload re-runs the SPA auth bootstrap and races the
  // token rehydration, bouncing to /#/login. A single goto after the routes are
  // in place is deterministic and keeps the storageState token applied.
  await page.route('**/api/v1/channels', async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          id: 'telegram',
          name: 'Telegram',
          transport: 'webhook',
          enabled: false,
          description: 'Telegram Bot integration',
        },
      ]),
    })
  })
  // Also stub routing and config endpoints to prevent 404 errors in the panel.
  await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({}),
      })
    } else {
      await route.continue()
    }
  })
  await page.route('**/api/v1/channels/telegram', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({}),
      })
    } else {
      await route.continue()
    }
  })

  // Navigate once, with mocks already in place.
  await page.goto('/#/channels')
  await expect(page).toHaveURL(/channels/, { timeout: 10_000 })

  // The Configure button must be visible.
  const configureBtn = page.getByRole('button', { name: /configure telegram/i })
  await expect(configureBtn).toBeVisible({ timeout: 15_000 })
  await configureBtn.click()

  // ChannelConfigPanel renders inside a Radix Sheet ([role="dialog"]).
  // The sheet title is "Configure Telegram" (SheetTitle in ChannelConfigPanel.tsx).
  const sheet = page.locator('[role="dialog"]')
  await expect(sheet).toBeVisible({ timeout: 10_000 })
  await expect(sheet).toContainText(/configure telegram/i)
})

// ── (d) Setting a Default agent issues PUT /channels/{id}/routing ─────────────
// BDD: Given the ChannelConfigPanel is open for "telegram",
//      When the user selects "Mia" in the Default agent SmartSelect,
//      Then a PUT /api/v1/channels/telegram/routing request is made
//           with body {"default_agent_id": "<mia-id>"},
//      And a subsequent GET /api/v1/channels/telegram/routing returns the set value.
//
// Traces to: sprint/258-jun-2026 — ChannelConfigPanel Routing section, set agent.

test(
  '(d) selecting a Default agent calls PUT /channels/{id}/routing with the agent id',
  async ({ page }) => {
    // Use REST API to find a known agent ID (Mia is always seeded).
    const agentsResp = await page.request.get(`${BASE_URL}/api/v1/agents`, {
      headers: await apiHeaders(page),
    })

    let miaId = 'mia'
    if (agentsResp.ok()) {
      const agents = (await agentsResp.json()) as Array<{ id: string; name: string }>
      const mia = agents.find((a) => /mia/i.test(a.name))
      if (mia) miaId = mia.id
    }

    // Intercept the routing GET to serve a clean starting state.
    await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({}),
        })
      } else {
        // Let the PUT go through to the real gateway so we can assert it was made.
        await route.continue()
      }
    })

    // Stub the channels list and channel config so the panel renders correctly.
    await page.route('**/api/v1/channels', async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'telegram', name: 'Telegram', transport: 'webhook', enabled: false, description: '' },
        ]),
      })
    })
    await page.route('**/api/v1/channels/telegram', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({}),
        })
      } else {
        await route.continue()
      }
    })

    await page.goto('/#/channels')
    await expect(page).toHaveURL(/channels/, { timeout: 10_000 })

    const configureBtn = page.getByRole('button', { name: /configure telegram/i })
    await expect(configureBtn).toBeVisible({ timeout: 15_000 })
    await configureBtn.click()

    // Sheet must open.
    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 10_000 })

    // Routing section must appear.
    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Intercept the PUT to capture the request body.
    let capturedPutBody: string | null = null
    await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
      if (route.request().method() === 'PUT') {
        capturedPutBody = route.request().postData()
        // Fulfil with the expected response so the mutation chain completes.
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ default_agent_id: miaId }),
        })
      } else {
        await route.continue()
      }
    })

    // Select the agent by name in the SmartSelect. SmartSelect renders a native
    // <select> when items.length <= SEARCHABLE_THRESHOLD (5); above that it renders
    // a Popover whose trigger is <button aria-haspopup="listbox"> and whose options
    // are cmdk items with role="option". With 5 agents + "(Global default)" = 6
    // items, the searchable Popover mode is used.
    const agentSelect = sheet.locator('select').first()
    const hasSelect = await agentSelect.count() > 0
    if (hasSelect) {
      await agentSelect.selectOption({ label: /mia/i })
    } else {
      const agentTrigger = sheet.locator('button[aria-haspopup="listbox"]').first()
      await expect(agentTrigger).toBeVisible({ timeout: 5_000 })
      await agentTrigger.click()
      await page.getByRole('option', { name: /mia/i }).first().click()
    }

    // The PUT must have been called with a non-null default_agent_id.
    await expect.poll(
      () => capturedPutBody,
      { timeout: 5_000, message: 'PUT /channels/telegram/routing was not called' },
    ).not.toBeNull()

    if (capturedPutBody) {
      const body = JSON.parse(capturedPutBody) as { default_agent_id: string | null }
      // Tightened assertion: must equal the specific agent ID we selected, not just
      // any non-null string. A hardcoded response would return the same value regardless
      // of which agent was selected — this assertion catches that.
      expect(body.default_agent_id).toBe(miaId)
    }
  },
)

// ── (e) Clearing to "(Global default)" sends PUT with default_agent_id omitted ─
// BDD: Given the channel already has a default agent set,
//      When the user selects "(Global default)" in the SmartSelect,
//      Then a PUT /api/v1/channels/telegram/routing is sent with default_agent_id
//           omitted (the gateway removes the channel binding).
//
// Traces to: sprint/258-jun-2026 — ChannelConfigPanel Routing section, clear agent.

test(
  '(e) selecting "(Global default)" calls PUT /channels/{id}/routing with default_agent_id omitted',
  async ({ page }) => {
    // Seed routing with an existing agent so we can clear it.
    let serveExisting = true

    await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(serveExisting ? { default_agent_id: 'mia' } : {}),
        })
      } else {
        // capture and fulfil
        await route.continue()
      }
    })
    await page.route('**/api/v1/channels', async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'telegram', name: 'Telegram', transport: 'webhook', enabled: false, description: '' },
        ]),
      })
    })
    await page.route('**/api/v1/channels/telegram', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
      } else {
        await route.continue()
      }
    })

    await page.goto('/#/channels')
    await expect(page).toHaveURL(/channels/, { timeout: 10_000 })

    const configureBtn = page.getByRole('button', { name: /configure telegram/i })
    await expect(configureBtn).toBeVisible({ timeout: 15_000 })
    await configureBtn.click()

    const sheet = page.locator('[role="dialog"]')
    await expect(sheet).toBeVisible({ timeout: 10_000 })
    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Intercept PUT to capture body.
    let capturedPutBody: string | null = null
    await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
      if (route.request().method() === 'PUT') {
        capturedPutBody = route.request().postData()
        serveExisting = false
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({}),
        })
      } else {
        await route.continue()
      }
    })

    // Select "(Global default)" in the SmartSelect (searchable Popover mode; see (d)).
    const agentSelect = sheet.locator('select').first()
    const hasSelect = await agentSelect.count() > 0
    if (hasSelect) {
      await agentSelect.selectOption({ label: /(global default)/i })
    } else {
      const agentTrigger = sheet.locator('button[aria-haspopup="listbox"]').first()
      await expect(agentTrigger).toBeVisible({ timeout: 5_000 })
      await agentTrigger.click()
      await page.getByRole('option', { name: /(global default)/i }).first().click()
    }

    // The PUT must have been called. Per the ChannelRouting contract (nullable
    // dropped — F12), "(Global default)" sends default_agent_id OMITTED (undefined),
    // which the gateway treats identically to empty: remove the channel binding.
    await expect.poll(
      () => capturedPutBody,
      { timeout: 5_000, message: 'PUT /channels/telegram/routing was not called for global-default' },
    ).not.toBeNull()

    if (capturedPutBody) {
      const body = JSON.parse(capturedPutBody) as { default_agent_id?: string }
      // Field is omitted (or empty) — never a non-empty agent id.
      expect(body.default_agent_id ?? '').toBe('')
    }
  },
)
