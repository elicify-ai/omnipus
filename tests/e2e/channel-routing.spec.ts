/**
 * channel-routing.spec.ts — E2E tests for workspace-scoped channel routing UX.
 *
 * Tests exercise US-1 / US-2 / US-3 from the channel-instance-workspace-binding
 * spec:
 *
 *   (a) US-1 / FR-001: Workspace selector renders above the agent picker in the
 *       ChannelConfigPanel Routing section.
 *   (b) US-2 / FR-002: Selecting a workspace filters the agent picker to that
 *       workspace's core_team members only.
 *   (c) US-2 / FR-009: When the selected workspace has an empty core_team the
 *       empty-core-team hint renders (data-testid="routing-empty-core-team-hint")
 *       and the agent picker is disabled.
 *   (d) US-3 / FR-004: Selecting a workspace without then choosing an agent does
 *       NOT fire PUT /channels/{id}/routing; the required-agent hint is visible
 *       (data-testid="routing-agent-required-hint").
 *   (e) US-3 / FR-003: In bound flow (workspace selected), "(Global default)"
 *       option is absent from the agent picker.
 *   (f) US-1+3: Selecting workspace + agent fires PUT with both workspace_id and
 *       default_agent_id in the body.
 *
 * Architecture:
 *   - All external API calls are intercepted with page.route() stubs so the
 *     spec runs without a real gateway, real agents, or real workspaces.
 *   - The interceptors are registered before page.goto() to avoid race
 *     conditions on initial load.
 *   - Tests do NOT use test.skip() for soft-skips unless explicitly noted.
 *
 * CLAUDE.md note: E2E tests always target the embedded SPA (Go binary), not
 * the Vite dev server.  Ensure the SPA sync pipeline has run before this spec.
 *
 * Traces to: channel-instance-workspace-binding-spec.md US-1/US-2/US-3.
 */

import { expect, type Page, type Route } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { selectNativeOptionByLabel } from './fixtures/selectors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
void BASE_URL // used only for reference; actual calls are via page.goto

// ── Stub data ────────────────────────────────────────────────────────────────

const STUB_CHANNELS = [
  {
    id: 'telegram',
    // instance_id marks this as a CONFIGURED instance — the redesigned
    // ConnectorsScreen only renders channel-card rows (with Configure) for
    // configured instances; rows without instance_id go to the roster.
    instance_id: 'telegram',
    // bound identity: bare-key disabled entries without one are DefaultConfig template stubs (roster, not a row)
    identity: { kind: 'agent', id: 'mia' },
    name: 'Telegram',
    transport: 'webhook',
    enabled: false,
    description: 'Telegram Bot integration',
  },
]

// The SPA's request() validates every response against the generated zod
// schema and REJECTS the whole payload on any mismatch (ApiSchemaError) —
// stubs must carry every schema-required field or the panel renders its
// "Couldn't load agent list." error instead of routing-agent-select.
const AGENT_REQUIRED = {
  status: 'idle',
  soul: 'stub soul',
  timeout_seconds: 300,
  max_tool_iterations: 200,
}
const STUB_AGENTS = [
  { id: 'mia', name: 'Mia', type: 'core', locked: true, ...AGENT_REQUIRED },
  { id: 'ray', name: 'Ray', type: 'core', locked: true, ...AGENT_REQUIRED },
  { id: 'jim', name: 'Jim', type: 'core', locked: true, ...AGENT_REQUIRED },
  { id: 'ava', name: 'Ava', type: 'core', locked: true, ...AGENT_REQUIRED },
  { id: 'worker-1', name: 'Worker One', type: 'Subagent', locked: false, ...AGENT_REQUIRED },
]

// Workspace schema requires pinned/pin_order/created_at/updated_at too.
const WS_REQUIRED = {
  pinned: false,
  pin_order: 0,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
}

const STUB_WORKSPACES = [
  {
    id: 'sales',
    name: 'Sales',
    status: 'active',
    core_team: ['mia', 'ray'],
    task_count: 0,
    ...WS_REQUIRED,
  },
  {
    id: 'engineering',
    name: 'Engineering',
    status: 'active',
    core_team: ['jim', 'ava'],
    task_count: 0,
    ...WS_REQUIRED,
  },
]

const STUB_WORKSPACE_SALES = {
  id: 'sales',
  name: 'Sales',
  status: 'active',
  core_team: ['mia', 'ray'],
  task_count: 0,
  ...WS_REQUIRED,
}

const STUB_WORKSPACE_EMPTY = {
  id: 'empty-ws',
  name: 'Empty Team',
  status: 'active',
  core_team: [],
  task_count: 0,
  ...WS_REQUIRED,
}

// ── Route registration helper ────────────────────────────────────────────────

/**
 * Register all the standard route stubs needed to open the ChannelConfigPanel
 * for "telegram".  workspacesOverride and workspaceDetailOverride let individual
 * tests customise the workspace data without re-registering everything.
 */
async function registerBaseRoutes(
  page: Page,
  opts: {
    routingResponse?: object
    workspacesOverride?: object[]
    workspaceDetailOverride?: Record<string, object>
  } = {},
) {
  const {
    routingResponse = {},
    workspacesOverride = STUB_WORKSPACES,
    workspaceDetailOverride = {
      sales: STUB_WORKSPACE_SALES,
      engineering: { id: 'engineering', name: 'Engineering', status: 'active', core_team: ['jim', 'ava'], task_count: 0 },
      'empty-ws': STUB_WORKSPACE_EMPTY,
    },
  } = opts

  await page.route('**/api/v1/channels', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(STUB_CHANNELS),
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
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(routingResponse),
      })
    } else {
      await route.continue()
    }
  })

  await page.route('**/api/v1/agents', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(STUB_AGENTS),
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
        body: JSON.stringify(workspacesOverride),
      })
    } else {
      await route.continue()
    }
  })

  // Per-workspace detail: match /api/v1/workspaces/<id> (no query string)
  for (const [wsId, wsDetail] of Object.entries(workspaceDetailOverride)) {
    await page.route(`**/api/v1/workspaces/${wsId}`, async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(wsDetail),
        })
      } else {
        await route.continue()
      }
    })
  }
}

/** Open the connectors screen and click Configure for telegram. */
async function openTelegramPanel(page: Page) {
  await page.goto('/#/connectors')
  await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })

  const configureBtn = page.getByRole('button', { name: /configure telegram/i })
  await expect(configureBtn).toBeVisible({ timeout: 15_000 })
  await configureBtn.click()

  const sheet = page.locator('[role="dialog"]')
  await expect(sheet).toBeVisible({ timeout: 10_000 })
  await expect(sheet).toContainText(/configure telegram/i)
  return sheet
}

// ── (a) US-1 / FR-001: Workspace selector renders ──────────────────────────

test(
  '(a) workspace selector renders above agent picker in Routing section',
  async ({ page }) => {
    await registerBaseRoutes(page)
    const sheet = await openTelegramPanel(page)

    // Routing section heading must be present
    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Workspace selector must be present (wrapped in data-testid div)
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })

    // Agent selector must also be present
    const agentContainer = sheet.locator('[data-testid="routing-agent-select"]')
    await expect(agentContainer).toBeVisible({ timeout: 10_000 })

    // Workspace container must appear ABOVE agent container in the DOM
    const wsBox = await wsContainer.boundingBox()
    const agentBox = await agentContainer.boundingBox()
    expect(wsBox).not.toBeNull()
    expect(agentBox).not.toBeNull()
    expect(wsBox!.y).toBeLessThan(agentBox!.y)
  },
)

// ── (b) US-2 / FR-002: Workspace selection filters agents ─────────────────

test(
  '(b) selecting workspace filters agent picker to core_team members only',
  async ({ page }) => {
    await registerBaseRoutes(page)
    const sheet = await openTelegramPanel(page)

    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Select the "Sales" workspace from the workspace picker.
    // SmartSelect renders either a native <select> (≤5 items) or a Radix combobox.
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })

    const wsNativeSelect = wsContainer.locator('select')
    const wsHasNative = (await wsNativeSelect.count()) > 0

    if (wsHasNative) {
      await selectNativeOptionByLabel(wsNativeSelect, /Sales/i)
    } else {
      // SmartSelect renders a plain Radix SelectTrigger (role=combobox,
      // no aria-haspopup) for <=5 items and the searchable popover button
      // above that threshold — the container's single button covers both.
      const wsTrigger = wsContainer.locator('button').first()
      await expect(wsTrigger).toBeVisible({ timeout: 5_000 })
      await wsTrigger.click()
      await page.getByRole('option', { name: /Sales/i }).first().click()
    }

    // After workspace selection the agent picker should become enabled
    // (core_team = ['mia', 'ray'] → both non-workers → 2 items)
    const agentContainer = sheet.locator('[data-testid="routing-agent-select"]')
    await expect(agentContainer).toBeVisible({ timeout: 10_000 })

    // The empty-core-team hint must NOT be shown
    await expect(
      sheet.locator('[data-testid="routing-empty-core-team-hint"]'),
    ).not.toBeVisible()

    // The required-agent hint is shown when no agent is selected yet (bound flow)
    await expect(
      sheet.locator('[data-testid="routing-agent-required-hint"]'),
    ).toBeVisible({ timeout: 5_000 })

    // Worker agent must not appear in agent options.  Open the agent select.
    const agentNativeSelect = agentContainer.locator('select')
    const agentHasNative = (await agentNativeSelect.count()) > 0

    if (agentHasNative) {
      // Native select — inspect option text
      const optionValues = await agentNativeSelect.evaluate((sel: HTMLSelectElement) =>
        Array.from(sel.options).map((o) => o.text),
      )
      // core_team=['mia','ray'] → Mia, Ray should appear; Jim/Ava/Worker should not
      expect(optionValues.some((t) => /Mia/i.test(t))).toBe(true)
      expect(optionValues.some((t) => /Ray/i.test(t))).toBe(true)
      expect(optionValues.some((t) => /Worker/i.test(t))).toBe(false)
      expect(optionValues.some((t) => /Jim/i.test(t))).toBe(false)
    } else {
      // Radix combobox — open it to expose options
      const agentTrigger = agentContainer.locator('button').first()
      const isDisabled = await agentTrigger.isDisabled()
      if (!isDisabled) {
        await agentTrigger.click()
        // Options appear in a popover
        const options = page.locator('[role="option"]')
        await expect(options.first()).toBeVisible({ timeout: 5_000 })
        const texts = await options.allTextContents()
        expect(texts.some((t) => /Mia/i.test(t))).toBe(true)
        expect(texts.some((t) => /Ray/i.test(t))).toBe(true)
        expect(texts.some((t) => /Worker/i.test(t))).toBe(false)
        // Close the popover
        await page.keyboard.press('Escape')
      }
    }
  },
)

// ── (c) US-2 / FR-009: Empty core_team shows hint; agent picker hidden ──

test(
  '(c) workspace with empty core_team shows empty-team hint and hides the agent picker',
  async ({ page }) => {
    await registerBaseRoutes(page, {
      workspacesOverride: [
        ...STUB_WORKSPACES,
        { id: 'empty-ws', name: 'Empty Team', status: 'active', core_team: [], task_count: 0, ...WS_REQUIRED },
      ],
    })
    const sheet = await openTelegramPanel(page)

    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Select the "Empty Team" workspace
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })

    const wsNativeSelect = wsContainer.locator('select')
    const wsHasNative = (await wsNativeSelect.count()) > 0

    if (wsHasNative) {
      await selectNativeOptionByLabel(wsNativeSelect, /Empty Team/i)
    } else {
      const wsTrigger = wsContainer.locator('button').first()
      await expect(wsTrigger).toBeVisible({ timeout: 5_000 })
      await wsTrigger.click()
      await page.getByRole('option', { name: /Empty Team/i }).first().click()
    }

    // Empty-core-team hint must appear
    await expect(
      sheet.locator('[data-testid="routing-empty-core-team-hint"]'),
    ).toBeVisible({ timeout: 8_000 })

    // FR-009 (spec §583) requires the "add a member first" state — nothing is
    // selectable when core_team is empty. ChannelConfigPanel renders that hint
    // INSTEAD of the agent picker (mutually-exclusive ternary branch, see
    // ChannelConfigPanel.tsx:874 vs :883), so the picker control is ABSENT, not
    // merely disabled. Assert its absence (the earlier over-assertion of a
    // disabled routing-agent-select never matched FR-009 or the component).
    await expect(
      sheet.locator('[data-testid="routing-agent-select"]'),
    ).toHaveCount(0)
  },
)

// ── (d) US-3 / FR-004: No PUT when workspace chosen but agent not yet chosen ─

test(
  '(d) workspace selected but no agent chosen: no PUT fires and required-hint visible',
  async ({ page }) => {
    let putFired = false

    await registerBaseRoutes(page)

    // Override the routing PUT interceptor to detect any spurious calls
    await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
      if (route.request().method() === 'PUT') {
        putFired = true
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({}),
        })
      } else {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({}),
        })
      }
    })

    const sheet = await openTelegramPanel(page)
    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Select a workspace (bound flow starts, agent is still empty)
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })

    const wsNativeSelect = wsContainer.locator('select')
    const wsHasNative = (await wsNativeSelect.count()) > 0

    if (wsHasNative) {
      await selectNativeOptionByLabel(wsNativeSelect, /Sales/i)
    } else {
      const wsTrigger = wsContainer.locator('button').first()
      await expect(wsTrigger).toBeVisible({ timeout: 5_000 })
      await wsTrigger.click()
      await page.getByRole('option', { name: /Sales/i }).first().click()
    }

    // Required-agent hint must appear immediately
    await expect(
      sheet.locator('[data-testid="routing-agent-required-hint"]'),
    ).toBeVisible({ timeout: 8_000 })

    // Wait 600ms (beyond the 400ms debounce) to confirm no PUT was fired
    await page.waitForTimeout(600)
    expect(putFired).toBe(false)
  },
)

// ── (e) US-3 / FR-003: No "(Global default)" in bound flow ─────────────────

test(
  '(e) bound flow hides "(Global default)" option in agent picker',
  async ({ page }) => {
    await registerBaseRoutes(page)
    const sheet = await openTelegramPanel(page)

    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // First confirm the workspace selector is present (unbound flow initially)
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })

    // Select a workspace to enter bound flow
    const wsNativeSelect = wsContainer.locator('select')
    const wsHasNative = (await wsNativeSelect.count()) > 0

    if (wsHasNative) {
      await selectNativeOptionByLabel(wsNativeSelect, /Sales/i)
    } else {
      const wsTrigger = wsContainer.locator('button').first()
      await expect(wsTrigger).toBeVisible({ timeout: 5_000 })
      await wsTrigger.click()
      await page.getByRole('option', { name: /Sales/i }).first().click()
    }

    // Agent picker must now be enabled (Sales has core_team = ['mia','ray'])
    const agentContainer = sheet.locator('[data-testid="routing-agent-select"]')
    const agentNativeSelect = agentContainer.locator('select')
    const agentHasNative = (await agentNativeSelect.count()) > 0

    if (agentHasNative) {
      const optionLabels = await agentNativeSelect.evaluate((sel: HTMLSelectElement) =>
        Array.from(sel.options).map((o) => o.text),
      )
      expect(optionLabels.some((t) => /global default/i.test(t))).toBe(false)
    } else {
      const agentTrigger = agentContainer.locator('button').first()
      const isDisabled = await agentTrigger.isDisabled()
      if (!isDisabled) {
        await agentTrigger.click()
        const options = page.locator('[role="option"]')
        await expect(options.first()).toBeVisible({ timeout: 5_000 })
        const texts = await options.allTextContents()
        expect(texts.some((t) => /global default/i.test(t))).toBe(false)
        await page.keyboard.press('Escape')
      }
    }
  },
)

// ── (f) US-1+3: Workspace + agent → PUT fires with both IDs ─────────────────

test(
  '(f) selecting workspace then agent fires PUT with workspace_id and default_agent_id',
  async ({ page }) => {
    let capturedPutBody: string | null = null

    await registerBaseRoutes(page)

    // Override routing interceptor to capture the PUT body
    await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
      if (route.request().method() === 'PUT') {
        capturedPutBody = route.request().postData()
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: capturedPutBody ?? '{}',
        })
      } else {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({}),
        })
      }
    })

    const sheet = await openTelegramPanel(page)
    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Step 1: Select workspace "Sales"
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })

    const wsNativeSelect = wsContainer.locator('select')
    const wsHasNative = (await wsNativeSelect.count()) > 0

    if (wsHasNative) {
      await selectNativeOptionByLabel(wsNativeSelect, /Sales/i)
    } else {
      const wsTrigger = wsContainer.locator('button').first()
      await expect(wsTrigger).toBeVisible({ timeout: 5_000 })
      await wsTrigger.click()
      await page.getByRole('option', { name: /Sales/i }).first().click()
    }

    // Wait for agent picker to be enabled
    const agentContainer = sheet.locator('[data-testid="routing-agent-select"]')
    await expect(agentContainer).toBeVisible({ timeout: 8_000 })

    // Step 2: Select agent "Mia" from the filtered list
    const agentNativeSelect = agentContainer.locator('select')
    const agentHasNative = (await agentNativeSelect.count()) > 0

    if (agentHasNative) {
      // Wait for the select to be enabled before selecting
      await expect(agentNativeSelect).not.toBeDisabled({ timeout: 8_000 })
      await selectNativeOptionByLabel(agentNativeSelect, /Mia/i)
    } else {
      const agentTrigger = agentContainer.locator('button').first()
      await expect(agentTrigger).not.toBeDisabled({ timeout: 8_000 })
      await agentTrigger.click()
      await page.getByRole('option', { name: /Mia/i }).first().click()
    }

    // Wait for the debounced PUT to fire (400ms debounce + network round-trip)
    await expect
      .poll(() => capturedPutBody, {
        timeout: 5_000,
        message: 'PUT /channels/telegram/routing was not called after agent selection',
      })
      .not.toBeNull()

    // Verify the PUT body contains both workspace_id and default_agent_id
    expect(capturedPutBody).not.toBeNull()
    const body = JSON.parse(capturedPutBody!) as {
      workspace_id?: string
      default_agent_id?: string
    }
    expect(body.workspace_id).toBe('sales')
    expect(body.default_agent_id).toBe('mia')
  },
)

// ── (g) Finding #1: workspace load error → routing-workspace-load-error shown ──
//
// When the workspace detail fetch fails (500), the agent picker must NOT appear
// silently empty — the distinct routing-workspace-load-error element must render.

test(
  '(g) workspace fetch failure shows routing-workspace-load-error, not silent empty agent picker',
  async ({ page }) => {
    // Bind the channel to "sales" in the routing response so the workspace
    // detail fetch is triggered when the panel opens.
    await registerBaseRoutes(page, {
      routingResponse: { workspace_id: 'sales', default_agent_id: undefined },
    })

    // Override the workspace detail endpoint to return 500 for 'sales'
    await page.route('**/api/v1/workspaces/sales', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ status: 500, body: 'Internal Server Error' })
      } else {
        await route.continue()
      }
    })

    const sheet = await openTelegramPanel(page)
    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // The distinct error element must appear
    await expect(
      sheet.locator('[data-testid="routing-workspace-load-error"]'),
    ).toBeVisible({ timeout: 10_000 })

    // The agent picker must NOT render silently empty
    await expect(
      sheet.locator('[data-testid="routing-agent-select"]'),
    ).not.toBeVisible()

    // The required-agent hint must NOT coexist with the error (it would be misleading)
    await expect(
      sheet.locator('[data-testid="routing-agent-required-hint"]'),
    ).not.toBeVisible()
  },
)

// ── (h) Finding #2: unbind flow → PUT fires with no workspace_id ───────────────
//
// When the user selects "No workspace" on a currently-bound channel instance,
// a PUT must fire immediately with no workspace_id and no default_agent_id so
// the backend clears the binding.

test(
  '(h) selecting "No workspace" on a bound channel fires PUT with no workspace_id',
  async ({ page }) => {
    let capturedUnbindBody: string | null = null

    // Channel starts bound to 'sales'
    await registerBaseRoutes(page, {
      routingResponse: { workspace_id: 'sales', default_agent_id: 'mia' },
    })

    // Intercept the PUT to capture the unbind payload
    await page.route('**/api/v1/channels/telegram/routing', async (route: Route) => {
      if (route.request().method() === 'PUT') {
        capturedUnbindBody = route.request().postData()
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: capturedUnbindBody ?? '{}',
        })
      } else {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ workspace_id: 'sales', default_agent_id: 'mia' }),
        })
      }
    })

    const sheet = await openTelegramPanel(page)
    await expect(sheet.getByText(/^Routing$/i)).toBeVisible({ timeout: 10_000 })

    // Select "No workspace" to unbind
    const wsContainer = sheet.locator('[data-testid="routing-workspace-select"]')
    await expect(wsContainer).toBeVisible({ timeout: 10_000 })

    const wsNativeSelect = wsContainer.locator('select')
    const wsHasNative = (await wsNativeSelect.count()) > 0

    if (wsHasNative) {
      await wsNativeSelect.selectOption({ value: '__none__' })
    } else {
      const wsTrigger = wsContainer.locator('button').first()
      await expect(wsTrigger).toBeVisible({ timeout: 5_000 })
      await wsTrigger.click()
      await page.getByRole('option', { name: /No workspace/i }).first().click()
    }

    // Wait for the debounced PUT to fire (400ms debounce + some margin)
    await expect
      .poll(() => capturedUnbindBody, {
        timeout: 5_000,
        message: 'PUT /channels/telegram/routing was not called after selecting No workspace',
      })
      .not.toBeNull()

    // Verify the unbind PUT shape: no workspace_id, no default_agent_id
    const body = JSON.parse(capturedUnbindBody!) as {
      workspace_id?: string
      default_agent_id?: string
    }
    expect(body.workspace_id === undefined || body.workspace_id === '').toBe(true)
    expect(body.default_agent_id === undefined || body.default_agent_id === '').toBe(true)

    // After unbind, the agent selector must be disabled (unbound flow)
    const agentContainer = sheet.locator('[data-testid="routing-agent-select"]')
    await expect(agentContainer).toBeVisible({ timeout: 5_000 })
    const agentNativeSelect = agentContainer.locator('select')
    const agentHasNative = (await agentNativeSelect.count()) > 0
    if (agentHasNative) {
      await expect(agentNativeSelect).toBeDisabled()
    } else {
      const agentTrigger = agentContainer.locator('button').first()
      await expect(agentTrigger).toBeDisabled()
    }
  },
)
