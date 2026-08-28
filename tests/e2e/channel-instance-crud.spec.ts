/**
 * channel-instance-crud.spec.ts — E2E tests for the channel add-instance / delete flow.
 *
 * The Add-channel flow is slug-free ("mom-friendly Add channel", hotfix/v0.1.1
 * commit c8a4cf0b): CreateChannelSheet (ConnectorsScreen.tsx) collects three
 * Radix Selects — Channel, Workspace, Agent — and has NO slug input. The
 * instance key (<type>.<slug>) is auto-derived from the chosen workspace's
 * name via `autoInstanceSlug` and de-duplicated against existing instances
 * with a -2/-3 suffix; the operator never types or sees it. On submit the SPA:
 *   1. POST /api/v1/channels {type, slug} (slug auto-derived) — 201 creates
 *      the instance.
 *   2. PUT /api/v1/channels/<newid>/routing {workspace_id, default_agent_id}
 *      persists the ADR-029 binding chosen in the same Sheet.
 *   3. Closes the Sheet and immediately opens ChannelConfigPanel for the new
 *      instance (two-chained-Sheets handoff — credentials are step two).
 *
 * Tests exercise the channel create/delete + grouped-IA flows from the
 * connectors-providers-redesign spec (US-9 / US-10) against this slug-free
 * flow:
 *
 *   (a) FR-009 / US-10: "Add channel" button renders on the Connectors screen
 *       and opens a Sheet with Channel / Workspace / Agent selectors.
 *   (b) US-6 / FR-017 superseded by the slug-free redesign (see NOTE below):
 *       no slug input exists; submit stays disabled until Channel, Workspace
 *       AND Agent are all chosen.
 *   (c+d) US-10, AS-2: Valid Channel+Workspace+Agent fires POST with
 *       {type, slug} (slug auto-derived from the workspace name) and PUT
 *       .../routing with the chosen binding, and the new instance appears —
 *       grouped alongside the pre-existing instance of the same type — in
 *       the channel list (create→appears flow).
 *   (e) US-10 / AC-2: Clicking the delete affordance opens a confirmation
 *       dialog; confirming fires DELETE and the instance disappears from the
 *       list (delete→gone flow).
 *   (f) US-10: Cancelling the delete dialog fires no DELETE.
 *   (g) US-9, AS-1/AS-2: Instances group under their channel type, and a
 *       configured row shows its workspace→agent binding title.
 *
 * NOTE — connectors-providers-redesign-spec.md line 249 still describes the
 * create Sheet as "type to slug"; the shipped flow (ConnectorsScreen.tsx
 * CreateChannelSheet, commit c8a4cf0b) replaced the slug input with the
 * Workspace + Agent pickers instead (the slug is auto-derived, never typed).
 * This file follows the shipped code (source of truth per CLAUDE.md) — the
 * spec-doc update is tracked separately and is not this file's concern.
 *
 * Architecture:
 *   - All external API calls are intercepted with page.route() stubs so the
 *     spec runs without a real gateway, real agents, or real workspaces.
 *   - Interceptors are registered before page.goto() to avoid race conditions
 *     on initial load.
 *   - The two /api/v1/workspaces endpoints (list, `?status=...`, and detail,
 *     `/<id>`) are stubbed via a URL-predicate matcher (pathname equality /
 *     regex), NOT a glob string. Playwright's glob '?' is a single-character
 *     wildcard that also matches the '/' separating "workspaces" from an id
 *     segment, so a glob route registered for the list would silently also
 *     intercept detail requests (or vice versa, depending on registration
 *     order — Playwright gives the most-recently-registered matching route
 *     priority). A URL predicate testing `url.pathname` sidesteps the
 *     ambiguity entirely instead of relying on registration-order semantics.
 *   - data-testid selectors are used throughout for stable targeting.
 *
 * Traces to: connectors-providers-redesign-spec.md US-9/US-10;
 * ConnectorsScreen.tsx CreateChannelSheet / autoInstanceSlug (shipped flow,
 * commit c8a4cf0b).
 */

import { expect, type Page, type Route } from '@playwright/test'
import { test } from './fixtures/console-errors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
void BASE_URL // used only for reference; actual calls are via page.goto

// ── Stub data ────────────────────────────────────────────────────────────────
//
// The fixed backend emits ONE entry per cfg.Channels instance, keyed by
// instance_id (e.g. "telegram.us-sales" and "telegram.eu-sales" are TWO
// distinct rows even though they share type "telegram"). Fixture realism
// rule (ConnectorsScreen.tsx isTemplateStub): a mocked telegram row meant to
// be a CONFIGURED instance must carry an instance_id WITH A DOT (namespaced,
// e.g. "telegram.us-sales") or an `identity` — a bare-key, disabled, unbound
// row is a DefaultConfig template stub and renders in the "Available"
// roster, not as a configured channel-card.

// Pre-existing instance — "telegram.us-sales" already exists before the user
// adds a second telegram instance via the create flow. Lets test (c+d) assert
// two-per-type grouping.
const STUB_CHANNELS_INITIAL = [
  {
    id: 'telegram',
    instance_id: 'telegram.us-sales',
    name: 'Telegram',
    transport: 'webhook',
    enabled: false,
    description: 'Telegram Bot API',
  },
]

// After POST /api/v1/channels (create telegram.eu-sales) the backend re-lists
// ALL instances. The new entry appears as a distinct per-instance row
// alongside the pre-existing telegram.us-sales — two rows for the same base
// type.
const STUB_CHANNELS_AFTER_CREATE = [
  ...STUB_CHANNELS_INITIAL,
  {
    id: 'telegram',
    instance_id: 'telegram.eu-sales',
    name: 'Telegram',
    transport: 'webhook',
    enabled: false,
    description: 'Telegram Bot API',
  },
]

// Two named, active workspaces — the create Sheet's Workspace picker
// (GET /api/v1/workspaces?status=active) and the auto-slug derivation both
// key off these names ("US Sales" -> "us-sales", "EU Sales" -> "eu-sales").
// Full required-field shapes per contracts/components/schemas/Workspace.yaml
// so the SPA's runtime zod validation doesn't drop the entries.
const WORKSPACE_US_SALES = {
  id: 'ws-us',
  name: 'US Sales',
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  core_team: ['mia'],
  created_at: '2026-06-08T14:22:00Z',
  updated_at: '2026-06-08T14:22:00Z',
}
const WORKSPACE_EU_SALES = {
  id: 'ws-eu',
  name: 'EU Sales',
  status: 'active',
  pinned: false,
  pin_order: 1,
  task_count: 0,
  core_team: ['mia'],
  created_at: '2026-06-08T14:22:00Z',
  updated_at: '2026-06-08T14:22:00Z',
}
const WORKSPACES_FIXTURE = [WORKSPACE_US_SALES, WORKSPACE_EU_SALES]

// The single non-worker core agent used across these tests — a member of
// both fixture workspaces' core_team, per contracts/components/schemas/Agent.yaml.
const AGENT_MIA = {
  id: 'mia',
  name: 'Mia',
  type: 'core',
  locked: true,
  status: 'active',
  soul: '',
  timeout_seconds: 300,
  max_tool_iterations: 25,
  // Required by Agent.yaml since 36801b44 (ADR-066/067/068); omitting it makes
  // AgentSchema reject GET /agents. false = healthy (has a usable model).
  needs_model: false,
}
const AGENTS_FIXTURE = [AGENT_MIA]

// ── Workspace route predicates (URL-based — see header docstring) ───────────

function isWorkspacesListUrl(url: URL): boolean {
  return url.pathname === '/api/v1/workspaces'
}

function isWorkspaceDetailUrl(url: URL): boolean {
  return /^\/api\/v1\/workspaces\/[^/]+$/.test(url.pathname)
}

/** Stub GET /api/v1/workspaces (list, any query string) with a fixed array. */
async function stubWorkspacesList(page: Page, workspaces: object[]) {
  await page.route(isWorkspacesListUrl, async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(workspaces) })
    } else {
      await route.continue()
    }
  })
}

/** Stub GET /api/v1/workspaces/<id> (detail) by id lookup; 404 if unknown. */
async function stubWorkspaceDetail(page: Page, workspaces: object[]) {
  const byId = new Map((workspaces as Array<{ id: string }>).map((w) => [w.id, w]))
  await page.route(isWorkspaceDetailUrl, async (route: Route) => {
    if (route.request().method() === 'GET') {
      const id = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop() ?? '')
      const ws = byId.get(id)
      if (ws) {
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(ws) })
      } else {
        await route.fulfill({
          status: 404,
          contentType: 'application/json',
          body: JSON.stringify({ error: `unknown workspace: ${id}` }),
        })
      }
    } else {
      await route.continue()
    }
  })
}

/** Stub GET /api/v1/agents with a fixed array. */
async function stubAgents(page: Page, agents: object[]) {
  await page.route('**/api/v1/agents', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(agents) })
    } else {
      await route.continue()
    }
  })
}

// ── Route registration helper ────────────────────────────────────────────────

/**
 * Register base route stubs for the Connectors screen: channels list/create,
 * per-instance config + routing, and the default (two-workspace, one-agent)
 * workspaces/agents fixtures. Individual tests may override specific routes
 * after calling this.
 */
async function registerBaseRoutes(
  page: Page,
  opts: {
    channels?: object[]
    postCreateResponse?: object
    postCreateStatus?: number
  } = {},
) {
  const {
    channels = STUB_CHANNELS_INITIAL,
    postCreateResponse = { id: 'telegram.eu-sales', type: 'telegram', enabled: false },
    postCreateStatus = 201,
  } = opts

  // GET /api/v1/channels — initial channel list
  // POST /api/v1/channels — create instance
  await page.route('**/api/v1/channels', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(channels),
      })
    } else if (route.request().method() === 'POST') {
      await route.fulfill({
        status: postCreateStatus,
        contentType: 'application/json',
        body: JSON.stringify(postCreateResponse),
      })
    } else {
      await route.continue()
    }
  })

  // Stub GET /api/v1/channels/<id> for any per-instance config fetch
  // (needed by ChannelConfigPanel when opened for telegram.us-sales or telegram.eu-sales)
  await page.route('**/api/v1/channels/telegram*', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    } else {
      await route.continue()
    }
  })

  // Stub GET/PUT /api/v1/channels/<id>/routing — the redesigned rows
  // (ChannelInstanceRow) fire fetchChannelRouting(instanceId) per rendered row
  // to resolve their workspace→agent binding title, and CreateChannelSheet
  // fires setChannelRouting(newId, ...) right after a successful create.
  // Defaults to an unbound instance on GET ({}) and a 200 echo of the body on
  // PUT; individual tests may override with a more specific page.route()
  // after calling this helper (e.g. to capture + assert the PUT body).
  await page.route('**/api/v1/channels/*/routing', async (route: Route) => {
    const req = route.request()
    if (req.method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    } else if (req.method() === 'PUT') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: req.postData() ?? '{}' })
    } else {
      await route.continue()
    }
  })

  await stubAgents(page, AGENTS_FIXTURE)
  await stubWorkspacesList(page, WORKSPACES_FIXTURE)
  await stubWorkspaceDetail(page, WORKSPACES_FIXTURE)
}

/** Navigate to the Connectors screen. */
async function gotoConnectors(page: Page) {
  await page.goto('/#/connectors')
  await expect(page).toHaveURL(/connectors/, { timeout: 10_000 })
}

// ── (a) Add-instance button renders ─────────────────────────────────────────

test(
  '(a) "Add channel" button renders on the Connectors screen',
  async ({ page }) => {
    await registerBaseRoutes(page)
    await gotoConnectors(page)

    const addBtn = page.getByTestId('add-channel-instance-btn')
    await expect(addBtn).toBeVisible({ timeout: 15_000 })
    await expect(addBtn).toContainText(/add channel/i)
  },
)

// ── (b) All three picks required → submit blocked; no slug input ───────────

test(
  '(b) submit stays disabled until Channel, Workspace AND Agent are all chosen — no slug input exists',
  async ({ page }) => {
    await registerBaseRoutes(page)
    await gotoConnectors(page)

    // Open the create-channel Sheet
    const addBtn = page.getByTestId('add-channel-instance-btn')
    await expect(addBtn).toBeVisible({ timeout: 15_000 })
    await addBtn.click()

    const dialog = page.getByTestId('create-channel-sheet')
    await expect(dialog).toBeVisible({ timeout: 8_000 })

    // The instance key is auto-generated from the workspace name — the
    // operator never types one. There is no slug input anywhere in the Sheet.
    await expect(dialog.getByTestId('create-channel-slug-input')).toHaveCount(0)

    const submitBtn = dialog.getByTestId('create-channel-submit-btn')
    await expect(submitBtn).toBeDisabled()

    // Pick 1 of 3 (Channel) — still disabled.
    const typeSelect = dialog.getByTestId('create-channel-type-select')
    await typeSelect.click()
    await page.getByRole('option', { name: /telegram/i }).click()
    await expect(submitBtn).toBeDisabled()

    // Pick 2 of 3 (Workspace) — still disabled (Agent not chosen yet).
    const workspaceSelect = dialog.getByTestId('create-channel-workspace-select')
    await workspaceSelect.click()
    await page.getByRole('option', { name: 'US Sales' }).click()
    await expect(submitBtn).toBeDisabled()

    // Pick 3 of 3 (Agent) — now enabled.
    const agentSelect = dialog.getByTestId('create-channel-agent-select')
    await agentSelect.click()
    await page.getByRole('option', { name: 'Mia' }).click()
    await expect(submitBtn).not.toBeDisabled()
  },
)

// ── (c+d) Valid Channel+Workspace+Agent → POST + PUT fire → instance appears ─
//
// The fixed backend emits one row per cfg.Channels instance. After creating
// telegram.eu-sales (Workspace "EU Sales" auto-derives the "eu-sales" slug)
// the GET /channels response includes BOTH telegram.us-sales (the
// pre-existing instance) and telegram.eu-sales (the newly created one) as
// distinct per-instance entries — two rows for the same base type. This test
// asserts that per-instance behaviour: both cards appear after the create,
// grouped under one "telegram" header.

test(
  '(c+d) valid Channel+Workspace+Agent fires POST (auto slug) + PUT routing, and the new instance appears in the channel list',
  async ({ page }) => {
    let capturedPostBody: string | null = null
    let capturedPutBody: string | null = null
    let capturedPutId: string | null = null

    // First GET returns the initial channel list (telegram.us-sales already
    // exists); after POST + second GET returns the updated list
    // (telegram.us-sales + telegram.eu-sales). Both responses carry
    // `instance_id` matching the per-instance backend shape.
    let getCallCount = 0
    await page.route('**/api/v1/channels', async (route: Route) => {
      if (route.request().method() === 'GET') {
        getCallCount++
        const channels = getCallCount <= 1 ? STUB_CHANNELS_INITIAL : STUB_CHANNELS_AFTER_CREATE
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(channels),
        })
      } else if (route.request().method() === 'POST') {
        capturedPostBody = route.request().postData()
        await route.fulfill({
          status: 201,
          contentType: 'application/json',
          body: JSON.stringify({ id: 'telegram.eu-sales', type: 'telegram', enabled: false }),
        })
      } else {
        await route.continue()
      }
    })

    // Stub per-instance config fetches (telegram.us-sales, telegram.eu-sales)
    await page.route('**/api/v1/channels/telegram*', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
      } else {
        await route.continue()
      }
    })

    // Capture the post-create PUT .../routing so we can assert the ADR-029
    // binding chosen in the Sheet was persisted with the right ids.
    await page.route('**/api/v1/channels/*/routing', async (route: Route) => {
      const req = route.request()
      if (req.method() === 'GET') {
        await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
      } else if (req.method() === 'PUT') {
        capturedPutId = req.url().split('/api/v1/channels/')[1]?.split('/routing')[0] ?? null
        capturedPutBody = req.postData()
        await route.fulfill({ status: 200, contentType: 'application/json', body: req.postData() ?? '{}' })
      } else {
        await route.continue()
      }
    })

    await stubAgents(page, AGENTS_FIXTURE)
    await stubWorkspacesList(page, WORKSPACES_FIXTURE)
    await stubWorkspaceDetail(page, WORKSPACES_FIXTURE)

    await gotoConnectors(page)

    // Pre-existing telegram.us-sales card must be visible before any action
    const existingCard = page.getByTestId('channel-card-telegram.us-sales')
    await expect(existingCard).toBeVisible({ timeout: 15_000 })

    // Open the dialog
    const addBtn = page.getByTestId('add-channel-instance-btn')
    await expect(addBtn).toBeVisible({ timeout: 5_000 })
    await addBtn.click()

    const dialog = page.getByTestId('create-channel-sheet')
    await expect(dialog).toBeVisible({ timeout: 8_000 })

    // Select Channel
    const typeSelect = dialog.getByTestId('create-channel-type-select')
    await typeSelect.click()
    await page.getByRole('option', { name: /telegram/i }).click()

    // Select Workspace — "EU Sales" auto-derives the "eu-sales" slug,
    // distinct from the pre-existing "us-sales" instance.
    const workspaceSelect = dialog.getByTestId('create-channel-workspace-select')
    await workspaceSelect.click()
    await page.getByRole('option', { name: 'EU Sales' }).click()

    // Select Agent
    const agentSelect = dialog.getByTestId('create-channel-agent-select')
    await agentSelect.click()
    await page.getByRole('option', { name: 'Mia' }).click()

    // Submit
    const submitBtn = dialog.getByTestId('create-channel-submit-btn')
    await expect(submitBtn).not.toBeDisabled()
    await submitBtn.click()

    // (c) Verify POST body — {type, slug}; slug is auto-derived (no slug
    // input existed anywhere in the form).
    await expect
      .poll(() => capturedPostBody, {
        timeout: 5_000,
        message: 'POST /api/v1/channels was not called after form submit',
      })
      .not.toBeNull()

    const postBody = JSON.parse(capturedPostBody!) as { type?: string; slug?: string }
    expect(postBody.type).toBe('telegram')
    expect(postBody.slug).toBe('eu-sales')

    // Verify the ADR-029 binding PUT fired for the new instance with the
    // chosen workspace + agent.
    await expect
      .poll(() => capturedPutBody, {
        timeout: 5_000,
        message: 'PUT /api/v1/channels/telegram.eu-sales/routing was not called after create',
      })
      .not.toBeNull()
    expect(capturedPutId).toBe('telegram.eu-sales')
    const putBody = JSON.parse(capturedPutBody!) as { workspace_id?: string; default_agent_id?: string }
    expect(putBody.workspace_id).toBe('ws-eu')
    expect(putBody.default_agent_id).toBe('mia')

    // Dialog should close after success
    await expect(dialog).not.toBeVisible({ timeout: 5_000 })

    // (d) Both per-instance rows appear in the channel list after the create.
    // The pre-existing telegram.us-sales row must still be present AND the
    // new telegram.eu-sales row must appear — proving the backend emits one
    // row per instance (not one row per type).
    const newCard = page.getByTestId('channel-card-telegram.eu-sales')
    await expect(newCard).toBeVisible({ timeout: 8_000 })
    await expect(newCard).toContainText('telegram.eu-sales')
    // Pre-existing instance must still be listed (two-per-type assertion)
    await expect(existingCard).toBeVisible()
    await expect(existingCard).toContainText('telegram.us-sales')

    // TDD #31 / US-9, AS-1: instances group under their channel type — one
    // "telegram" group header contains BOTH per-instance rows, not two
    // separate top-level cards.
    const telegramGroup = page.getByTestId('channel-type-group-telegram')
    await expect(telegramGroup).toBeVisible()
    await expect(telegramGroup.getByTestId('channel-card-telegram.us-sales')).toBeVisible()
    await expect(telegramGroup.getByTestId('channel-card-telegram.eu-sales')).toBeVisible()
  },
)

// ── (e) Delete → confirm → gone ──────────────────────────────────────────────

test(
  '(e) delete confirm fires DELETE and the instance disappears from the list',
  async ({ page }) => {
    let deleteCallId: string | null = null
    let getCallCount = 0

    // Start with a list that includes telegram.eu
    const channelsWithInstance = [
      {
        id: 'telegram',
        instance_id: 'telegram.eu',
        name: 'Telegram',
        transport: 'webhook',
        enabled: false,
        description: 'Telegram Bot API',
      },
    ]

    await page.route('**/api/v1/channels', async (route: Route) => {
      if (route.request().method() === 'GET') {
        getCallCount++
        // First fetch returns the instance; subsequent ones return empty (after delete)
        const channels = getCallCount <= 1 ? channelsWithInstance : []
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(channels),
        })
      } else {
        await route.continue()
      }
    })

    // Stub DELETE /api/v1/channels/telegram.eu
    await page.route('**/api/v1/channels/telegram.eu', async (route: Route) => {
      if (route.request().method() === 'DELETE') {
        deleteCallId = 'telegram.eu'
        await route.fulfill({ status: 204 })
      } else {
        await route.continue()
      }
    })

    await stubAgents(page, AGENTS_FIXTURE)
    await stubWorkspacesList(page, WORKSPACES_FIXTURE)

    await gotoConnectors(page)

    // The instance card must be visible
    const card = page.getByTestId('channel-card-telegram.eu')
    await expect(card).toBeVisible({ timeout: 15_000 })

    // Click the delete button
    const deleteBtn = page.getByTestId('channel-delete-btn-telegram.eu')
    await expect(deleteBtn).toBeVisible({ timeout: 5_000 })
    await deleteBtn.click()

    // Confirmation dialog must appear
    const dialog = page.getByTestId('delete-instance-dialog')
    await expect(dialog).toBeVisible({ timeout: 5_000 })
    await expect(dialog).toContainText(/telegram\.eu/i)

    // Confirm the delete
    const confirmBtn = page.getByTestId('delete-instance-confirm-btn')
    await confirmBtn.click()

    // DELETE must have fired
    await expect
      .poll(() => deleteCallId, {
        timeout: 5_000,
        message: 'DELETE /api/v1/channels/telegram.eu was not called after confirm',
      })
      .toBe('telegram.eu')

    // Dialog must close
    await expect(dialog).not.toBeVisible({ timeout: 5_000 })

    // The card must be gone (list refreshed → empty)
    await expect(card).not.toBeVisible({ timeout: 8_000 })
  },
)

// ── (f) Delete → cancel → no DELETE ─────────────────────────────────────────

test(
  '(f) cancel on delete dialog fires no DELETE',
  async ({ page }) => {
    let deleteCallCount = 0

    const channelsWithInstance = [
      {
        id: 'telegram',
        instance_id: 'telegram.eu',
        name: 'Telegram',
        transport: 'webhook',
        enabled: false,
        description: 'Telegram Bot API',
      },
    ]

    await page.route('**/api/v1/channels', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(channelsWithInstance),
        })
      } else {
        await route.continue()
      }
    })

    await page.route('**/api/v1/channels/telegram.eu', async (route: Route) => {
      if (route.request().method() === 'DELETE') {
        deleteCallCount++
        await route.fulfill({ status: 204 })
      } else {
        await route.continue()
      }
    })

    await stubAgents(page, AGENTS_FIXTURE)
    await stubWorkspacesList(page, WORKSPACES_FIXTURE)

    await gotoConnectors(page)

    const card = page.getByTestId('channel-card-telegram.eu')
    await expect(card).toBeVisible({ timeout: 15_000 })

    const deleteBtn = page.getByTestId('channel-delete-btn-telegram.eu')
    await deleteBtn.click()

    const dialog = page.getByTestId('delete-instance-dialog')
    await expect(dialog).toBeVisible({ timeout: 5_000 })

    // Click cancel
    const cancelBtn = page.getByTestId('delete-instance-cancel-btn')
    await cancelBtn.click()

    // Dialog should close
    await expect(dialog).not.toBeVisible({ timeout: 5_000 })

    // Wait a tick to ensure no async DELETE fired
    await page.waitForTimeout(300)
    expect(deleteCallCount).toBe(0)

    // The card must still be visible (not deleted)
    await expect(card).toBeVisible({ timeout: 5_000 })
  },
)

// ── (g) Instance row shows the workspace→agent binding ──────────────────────
//
// TDD #31 / US-9, AS-2 (connectors-providers-redesign-spec.md): "Given an
// instance whatsapp.sales bound to workspace 'Sales' / agent 'Mia', when its
// row renders, then the title reads 'Sales → Mia'." This test exercises the
// same binding-title behaviour on the pre-existing telegram.us-sales fixture:
// the row resolves its routing (workspace_id + default_agent_id) and the
// workspace/agent id lists to human-readable names.

test(
  '(g) configured instance row shows the "Sales → Mia" workspace→agent binding',
  async ({ page }) => {
    await registerBaseRoutes(page)

    // Override the base (unbound) routing stub: telegram.us-sales is bound to
    // workspace "ws1" / agent "mia".
    await page.route('**/api/v1/channels/*/routing', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ workspace_id: 'ws1', default_agent_id: 'mia' }),
        })
      } else {
        await route.continue()
      }
    })

    // Override the base workspaces/agents fixtures with a name-bearing "ws1"
    // -> "Sales" / "mia" -> "Mia" pair so the row can resolve the binding
    // title. Full required-field shapes per the Workspace/Agent contract
    // schemas so the SPA's runtime zod validation doesn't drop the entries.
    await stubWorkspacesList(page, [
      {
        id: 'ws1',
        name: 'Sales',
        status: 'active',
        pinned: false,
        pin_order: 0,
        task_count: 0,
        created_at: '2026-06-08T14:22:00Z',
        updated_at: '2026-06-08T14:22:00Z',
      },
    ])
    await stubAgents(page, [
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        locked: true,
        status: 'active',
        soul: '',
        timeout_seconds: 300,
        max_tool_iterations: 25,
        // Required by Agent.yaml since 36801b44 (ADR-066/067/068); omitting it
        // makes AgentSchema reject GET /agents. false = healthy.
        needs_model: false,
      },
    ])

    await gotoConnectors(page)

    const binding = page.getByTestId('channel-binding-telegram.us-sales')
    await expect(binding).toBeVisible({ timeout: 15_000 })
    await expect(binding).toContainText('Sales → Mia')
  },
)
