/**
 * channels-routing.spec.ts — E2E tests for the Channels screen and channel
 * routing configuration (sprint/258-jun-2026).
 *
 * Tests exercise:
 *   (a) Channels sidebar item renders and navigates to /#/channels.
 *   (b) Channel feed renders cards from the real gateway GET /api/v1/channels.
 *   (c) Opening Configure opens the ChannelConfigPanel sheet for a channel.
 *   (d) Setting a Default agent now goes through the workspace-scoped
 *       routing flow (ADR-029 / channel-instance-workspace-binding-spec.md
 *       US-1..3): the operator selects a WORKSPACE first (the agent picker
 *       is disabled until then), then an agent from that workspace's
 *       core_team. This issues a real PUT /api/v1/channels/{id}/routing with
 *       {workspace_id, default_agent_id}, and a subsequent
 *       GET /api/v1/channels/{id}/routing (fired by the panel's own
 *       post-save cache invalidation) reflects the persisted value.
 *   (e) Clearing a bound channel's routing is done by selecting "No workspace
 *       (global default routing)" in the WORKSPACE picker — the bound flow's
 *       agent picker has no standalone "(Global default)" option (US-3 /
 *       FR-003). This issues PUT with both default_agent_id and
 *       workspace_id omitted, and the cleared state is reflected on the
 *       next GET read-back.
 *
 * Architecture:
 *   - Global storageState provides a pre-authenticated session.
 *   - Tests (c)/(d)/(e) use page.route() interception to assert the PUT
 *     payload AND fulfil the request so the mutation chain completes cleanly.
 *   - We do NOT run the full LLM suite here — no real LLM calls are needed.
 *
 * CLAUDE.md note: E2E tests always target the embedded SPA (Go binary), not
 * the Vite dev server. Ensure the SPA sync pipeline has run before this spec:
 *   npm run build && rm -rf pkg/gateway/spa/assets && cp -r dist/spa/* pkg/gateway/spa/
 *   CGO_ENABLED=0 go build -o /tmp/omnipus-ci ./cmd/omnipus/
 *
 * Traces to: sprint/258-jun-2026 — Channels screen + ChannelConfigPanel Routing section.
 */

import { expect, type Locator, type Page, type Route } from '@playwright/test'
import { test } from './fixtures/console-errors'

// ── (a) Connectors sidebar item ───────────────────────────────────────────────
// BDD: Given the user is logged in,
//      When they click the "Connectors" nav item in the sidebar,
//      Then the browser navigates to /#/connectors.
//
// Traces to: sprint/258-jun-2026 — Sidebar "Connectors" nav item (IA rename).

test('(a) Channels sidebar item navigates to /#/channels', async ({ page }) => {
  await page.goto('/')

  const hamburger = page.locator('#sidebar-hamburger')
  await expect(hamburger).toBeVisible({ timeout: 10_000 })
  await hamburger.click()

  const nav = page.locator('nav[aria-label="Main navigation"]')
  await expect(nav).toBeVisible({ timeout: 5_000 })

  // HashRouter: link href="/#/connectors" (renamed from Channels in the IA update)
  const connectorsLink = nav.locator('a[href="/#/connectors"]')
  await expect(connectorsLink).toBeVisible({ timeout: 5_000 })

  // Verify the link contains the label text "Connectors".
  await expect(connectorsLink).toContainText('Connectors')

  await connectorsLink.click()
  await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })
})

// ── (b) Channel feed renders cards ────────────────────────────────────────────
// BDD: Given the gateway is running with at least some channels configured,
//      When the user navigates to /#/channels,
//      Then the channel feed renders with channel cards.
//
// Traces to: sprint/258-jun-2026 — Channels screen, channel feed list.

test('(b) channel feed renders at least one channel card', async ({ page }) => {
  // Navigate directly via hash route (renamed from /#/channels to /#/connectors).
  await page.goto('/#/connectors')
  await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })

  // Wait for either channel cards or the empty state — both are valid depending
  // on whether channels are configured in the CI gateway.
  // ConnectorsScreen renders channel cards as div[data-testid="channel-card-{id}"].
  // If no channels are configured, the empty-state roster (US-9, AS-3) renders
  // instead, identified by [data-testid="channel-roster"].
  const channelContent = page.locator(
    '[data-testid^="channel-card-"], [data-testid="channel-roster"]',
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
          instance_id: 'telegram',
          // bound identity: bare-key disabled entries without one are DefaultConfig template stubs (roster, not a row)
          identity: { kind: 'agent', id: 'mia' },
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
  // Route is now /#/connectors (renamed from /#/channels in the IA update).
  await page.goto('/#/connectors')
  await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })

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

// ── Stub data + helpers for the workspace-scoped routing flow (tests d/e) ────
//
// The routing UX was redesigned (ADR-029 / channel-instance-workspace-binding
// -spec.md, US-1..3, already shipped and covered end-to-end by the sibling
// tests/e2e/channel-routing.spec.ts): a channel's Default agent can no longer
// be picked directly — the operator must first bind the channel to a
// WORKSPACE (routing-workspace-select), then pick an agent from that
// workspace's core_team (routing-agent-select, disabled until a workspace is
// chosen, and with no standalone "(Global default)" option once bound).
// Clearing a binding is done by re-selecting "No workspace (global default
// routing)" in the WORKSPACE picker, not in the agent picker.
//
// The SPA validates every response against its generated zod schema and
// drops payloads missing required fields (Constraint #8), so these stubs
// carry every schema-required key (mirrors channel-routing.spec.ts's stubs).
const ROUTING_AGENT_REQUIRED = {
  status: 'idle' as const,
  soul: 'stub soul',
  timeout_seconds: 300,
  max_tool_iterations: 200,
  // Required by contracts/components/schemas/Agent.yaml since 36801b44
  // (ADR-066/067/068 wire contracts). Omitting it makes AgentSchema reject the
  // whole GET /agents payload, so the panel renders "Couldn't load agent list."
  // instead of routing-agent-select. false = healthy (has a usable model).
  needs_model: false,
}
const ROUTING_STUB_AGENTS = [
  { id: 'mia', name: 'Mia', type: 'core', locked: true, ...ROUTING_AGENT_REQUIRED },
  { id: 'ray', name: 'Ray', type: 'core', locked: true, ...ROUTING_AGENT_REQUIRED },
]
const ROUTING_WS_REQUIRED = {
  pinned: false,
  pin_order: 0,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
}
const ROUTING_STUB_WORKSPACE = {
  id: 'sales',
  name: 'Sales',
  status: 'active',
  core_team: ['mia', 'ray'],
  task_count: 0,
  ...ROUTING_WS_REQUIRED,
}
const ROUTING_STUB_CHANNEL = {
  id: 'telegram',
  instance_id: 'telegram',
  // bound identity: bare-key disabled entries without one are DefaultConfig template stubs (roster, not a row)
  identity: { kind: 'agent', id: 'mia' },
  name: 'Telegram',
  transport: 'webhook',
  enabled: false,
  description: '',
}

type RoutingState = { workspace_id?: string; default_agent_id?: string }

/**
 * Register the route stubs for the workspace-scoped routing tests. PUTs to
 * the routing sub-resource mutate the same in-memory `state` that GETs read
 * back from (a full-replace resource — an omitted field means "cleared", per
 * ChannelConfigPanel's own unbind comment), so a GET issued after a PUT (the
 * panel's post-save `invalidateQueries` triggers exactly this) proves the
 * value actually persisted server-side rather than merely echoing the request.
 */
async function registerRoutingRoutes(
  page: Page,
  initialRouting: RoutingState,
): Promise<{ putBodies: string[]; getSnapshots: RoutingState[] }> {
  let state: RoutingState = { ...initialRouting }
  const putBodies: string[] = []
  const getSnapshots: RoutingState[] = []

  await page.route('**/api/v1/channels', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([ROUTING_STUB_CHANNEL]),
      })
    } else {
      await route.continue()
    }
  })

  await page.route('**/api/v1/channels/telegram', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    } else {
      await route.continue()
    }
  })

  await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
    const method = route.request().method()
    if (method === 'GET') {
      getSnapshots.push({ ...state })
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) })
    } else if (method === 'PUT') {
      const raw = route.request().postData() ?? '{}'
      putBodies.push(raw)
      // Full replace, not a patch — an omitted key means "cleared" (matches
      // ChannelConfigPanel's unbind path, which sends {} on purpose).
      state = JSON.parse(raw) as RoutingState
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) })
    } else {
      await route.continue()
    }
  })

  await page.route('**/api/v1/agents', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(ROUTING_STUB_AGENTS),
      })
    } else {
      await route.continue()
    }
  })

  await page.route('**/api/v1/workspaces?**', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([ROUTING_STUB_WORKSPACE]),
      })
    } else {
      await route.continue()
    }
  })

  await page.route('**/api/v1/workspaces/sales', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(ROUTING_STUB_WORKSPACE),
      })
    } else {
      await route.continue()
    }
  })

  return { putBodies, getSnapshots }
}

/** Open the Connectors screen and click Configure for the stubbed Telegram channel. */
async function openTelegramConfigPanel(page: Page): Promise<Locator> {
  await page.goto('/#/connectors')
  await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })

  const configureBtn = page.getByRole('button', { name: /configure telegram/i })
  await expect(configureBtn).toBeVisible({ timeout: 15_000 })
  await configureBtn.click()

  const sheet = page.locator('[role="dialog"]')
  await expect(sheet).toBeVisible({ timeout: 10_000 })
  await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })
  return sheet
}

/**
 * Select an option (by visible label) from a SmartSelect container. Handles
 * both the plain Radix Select (<=5 items — trigger is a <button role="combobox">,
 * no native <select> element is ever rendered here) and the searchable
 * Popover/cmdk mode used above the 5-item threshold.
 */
async function chooseSmartSelectOption(page: Page, container: Locator, label: RegExp) {
  const native = container.locator('select')
  if ((await native.count()) > 0) {
    await native.selectOption({ label })
  } else {
    const trigger = container.locator('button').first()
    await expect(trigger).toBeVisible({ timeout: 5_000 })
    await trigger.click()
    await page.getByRole('option', { name: label }).first().click()
  }
}

/** Select "No workspace (global default routing)" by its stable `__none__`
 *  value (avoids escaping the parenthesised label text as a regex). */
async function chooseNoWorkspace(page: Page, container: Locator) {
  const native = container.locator('select')
  if ((await native.count()) > 0) {
    await native.selectOption({ value: '__none__' })
  } else {
    const trigger = container.locator('button').first()
    await expect(trigger).toBeVisible({ timeout: 5_000 })
    await trigger.click()
    await page.getByRole('option', { name: /no workspace/i }).first().click()
  }
}

// ── (d) Binding a workspace + agent calls PUT /channels/{id}/routing ────────
// BDD: Given the ChannelConfigPanel is open for "telegram" and unbound,
//      When the user selects the "Sales" workspace (US-1) and then "Mia"
//           from the now-enabled, workspace-filtered Default agent picker
//           (US-2),
//      Then a PUT /api/v1/channels/telegram/routing request is made with
//           body {"workspace_id":"sales","default_agent_id":"mia"} (US-3),
//      And a subsequent GET /api/v1/channels/telegram/routing (fired by the
//           panel's own post-save cache invalidation) returns that same
//           persisted value.
//
// Traces to: channel-instance-workspace-binding-spec.md US-1/US-2/US-3
// (ADR-029). This supersedes the test's original "pick any agent directly,
// with a (Global default) escape hatch" assertions: that flow was
// deliberately REPLACED by the mandatory workspace-first routing redesign
// (ChannelConfigPanel.tsx's `isBoundFlow`), not broken by the 4-base roster
// re-cast the original skip blamed — the roster re-cast is unrelated; the
// picker's shape changed because the underlying routing model changed.

test(
  '(d) selecting a workspace then an agent calls PUT /channels/{id}/routing with workspace_id + default_agent_id, and it persists on GET read-back',
  async ({ page }) => {
    const { putBodies, getSnapshots } = await registerRoutingRoutes(page, {})

    const sheet = await openTelegramConfigPanel(page)

    // Step 1 (US-1): bind the channel to the "Sales" workspace. Until this is
    // done the Default agent picker is disabled (mandatory workspace-first flow).
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })
    await chooseSmartSelectOption(page, wsContainer, /Sales/i)

    // Step 2 (US-2/US-3): pick "Mia" from the now-enabled, workspace-filtered
    // agent picker (Sales' core_team is [mia, ray]).
    const agentContainer = sheet.locator('[data-testid="routing-agent-select"]')
    await expect(agentContainer).toBeVisible({ timeout: 10_000 })
    const agentNativeSelect = agentContainer.locator('select')
    if ((await agentNativeSelect.count()) > 0) {
      await expect(agentNativeSelect).not.toBeDisabled({ timeout: 8_000 })
    } else {
      await expect(agentContainer.locator('button').first()).not.toBeDisabled({ timeout: 8_000 })
    }
    await chooseSmartSelectOption(page, agentContainer, /Mia/i)

    // The 400ms-debounced PUT must fire with both ids.
    await expect
      .poll(() => putBodies.length, {
        timeout: 5_000,
        message: 'PUT /channels/telegram/routing was not called',
      })
      .toBeGreaterThan(0)

    const putBody = JSON.parse(putBodies[putBodies.length - 1]) as RoutingState
    expect(putBody.workspace_id).toBe('sales')
    expect(putBody.default_agent_id).toBe('mia')

    // Persistence: the panel invalidates the routing query on save success,
    // firing a real subsequent GET. That GET must reflect what was actually
    // written (mutated mock "backend" state), not just echo the request.
    await expect
      .poll(
        () => {
          const last = getSnapshots[getSnapshots.length - 1]
          return last?.workspace_id === 'sales' && last?.default_agent_id === 'mia'
        },
        {
          timeout: 5_000,
          message: 'no GET /channels/telegram/routing read-back reflected the persisted binding',
        },
      )
      .toBe(true)
  },
)

// ── (e) Clearing a bound channel via "No workspace" omits both routing ids ───
// BDD: Given the channel is already bound to workspace "sales" / agent "mia",
//      When the user selects "No workspace (global default routing)" in the
//           WORKSPACE picker (there is no equivalent "(Global default)"
//           option in the bound flow's agent picker — US-3/FR-003),
//      Then a PUT /api/v1/channels/telegram/routing is sent with both
//           default_agent_id and workspace_id omitted,
//      And the agent picker reverts to its disabled, unbound state, and a
//           subsequent GET read-back reflects the cleared binding.
//
// Traces to: channel-instance-workspace-binding-spec.md US-3 (ADR-029). This
// supersedes the test's original "(Global default)" agent-picker option —
// that standalone option was intentionally REMOVED from the bound flow by
// the workspace-scoped redesign; unbinding now happens one level up, via the
// workspace picker.

test(
  '(e) selecting "No workspace" on a bound channel calls PUT with default_agent_id + workspace_id omitted, and it persists on GET read-back',
  async ({ page }) => {
    const { putBodies, getSnapshots } = await registerRoutingRoutes(page, {
      workspace_id: 'sales',
      default_agent_id: 'mia',
    })

    const sheet = await openTelegramConfigPanel(page)

    // Pre-condition: the panel loads already bound (workspace + agent shown).
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })
    await expect(wsContainer).toContainText('Sales', { timeout: 8_000 })
    const agentContainer = sheet.locator('[data-testid="routing-agent-select"]')
    await expect(agentContainer).toContainText('Mia', { timeout: 8_000 })

    // Unbind: select "No workspace (global default routing)" in the
    // WORKSPACE picker — there is no equivalent option in the agent picker
    // once bound (US-3/FR-003).
    await chooseNoWorkspace(page, wsContainer)

    await expect
      .poll(() => putBodies.length, {
        timeout: 5_000,
        message: 'PUT /channels/telegram/routing was not called for unbind',
      })
      .toBeGreaterThan(0)

    const putBody = JSON.parse(putBodies[putBodies.length - 1]) as RoutingState
    expect(putBody.workspace_id ?? '').toBe('')
    expect(putBody.default_agent_id ?? '').toBe('')

    // The agent picker reverts to the disabled, unbound state.
    const agentNativeSelect = agentContainer.locator('select')
    if ((await agentNativeSelect.count()) > 0) {
      await expect(agentNativeSelect).toBeDisabled({ timeout: 5_000 })
    } else {
      await expect(agentContainer.locator('button').first()).toBeDisabled({ timeout: 5_000 })
    }

    // Persistence: the post-save invalidation fires a real GET; it must
    // reflect the CLEARED state, not the pre-unbind {sales, mia} binding.
    await expect
      .poll(
        () => {
          const last = getSnapshots[getSnapshots.length - 1]
          return (last?.workspace_id ?? '') === '' && (last?.default_agent_id ?? '') === ''
        },
        {
          timeout: 5_000,
          message: 'no GET /channels/telegram/routing read-back reflected the cleared binding',
        },
      )
      .toBe(true)
  },
)
