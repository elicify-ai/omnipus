/**
 * channel-instance-crud.spec.ts — E2E tests for the channel add-instance / delete flow.
 *
 * Tests exercise the channel create/delete + grouped-IA flows from the
 * connectors-providers-redesign spec (US-9 / US-10), which supersede the
 * original channel-instance-workspace-binding CRUD scenarios these tests were
 * first written against:
 *
 *   (a) FR-009 / US-10: "Add channel" button renders on the Connectors screen
 *       and opens a Sheet with a type selector and a slug input.
 *   (b) US-6 / FR-017: Invalid slug (uppercase/special chars) → submit blocked
 *       and the slug hint is shown.
 *   (c) US-10, AS-2: Valid type + slug → POST fires with {type, slug} and the
 *       new instance appears in the list (create→appears flow).
 *   (d) US-11: The new namespaced instance id (e.g. "telegram.eu") is shown as a
 *       badge on the channel card.
 *   (e) US-10 / AC-2: Clicking the delete affordance opens a confirmation dialog;
 *       confirming fires DELETE and the instance disappears from the list
 *       (delete→gone flow).
 *   (f) US-10: Cancelling the delete dialog fires no DELETE.
 *   (g) US-9, AS-1/AS-2: Instances group under their channel type, and a
 *       configured row shows its workspace→agent binding title.
 *
 * Architecture:
 *   - All external API calls are intercepted with page.route() stubs so the
 *     spec runs without a real gateway, real agents, or real workspaces.
 *   - Interceptors are registered before page.goto() to avoid race conditions
 *     on initial load.
 *   - data-testid selectors are used throughout for stable targeting.
 *
 * Traces to: connectors-providers-redesign-spec.md US-9/US-10.
 */

import { expect, type Page, type Route } from '@playwright/test'
import { test } from './fixtures/console-errors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
void BASE_URL // used only for reference; actual calls are via page.goto

// ── Stub data ────────────────────────────────────────────────────────────────
//
// The fixed backend emits ONE entry per cfg.Channels instance, keyed by
// instance_id (e.g. "telegram.us" and "telegram.eu" are TWO distinct rows
// even though they share type "telegram"). This stub faithfully mirrors that
// shape: instance_id is always present and equals the config map key.

// Pre-existing instance — a "telegram.us" entry that already exists before
// the user adds "telegram.eu". This lets test (c+d) assert two-per-type.
const STUB_CHANNELS_INITIAL = [
  {
    id: 'telegram',
    instance_id: 'telegram.us',
    name: 'Telegram',
    transport: 'webhook',
    enabled: false,
    description: 'Telegram Bot API',
  },
]

// After POST /api/v1/channels (create telegram.eu) the backend re-lists ALL
// instances. The new entry appears as a distinct per-instance row alongside
// the pre-existing telegram.us — two rows for the same base type.
const STUB_CHANNELS_AFTER_CREATE = [
  ...STUB_CHANNELS_INITIAL,
  {
    id: 'telegram',
    instance_id: 'telegram.eu',
    name: 'Telegram',
    transport: 'webhook',
    enabled: false,
    description: 'Telegram Bot API',
  },
]

// ── Route registration helper ────────────────────────────────────────────────

/**
 * Register base route stubs for the Connectors screen.
 * Individual tests may override specific routes after calling this.
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
    postCreateResponse = { id: 'telegram.eu', type: 'telegram', enabled: false },
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
  // (needed by ChannelConfigPanel when opened for telegram.us or telegram.eu)
  await page.route('**/api/v1/channels/telegram*', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    } else {
      await route.continue()
    }
  })

  // Stub GET /api/v1/channels/<id>/routing — the redesigned rows
  // (ChannelInstanceRow) fire getChannelRouting(instanceId) per rendered row
  // to resolve their workspace→agent binding title. Without this stub the
  // request falls through to the real gateway, violating this file's
  // documented hermeticity. Defaults to an unbound instance ({}); individual
  // tests may override with a more specific page.route() after calling this
  // helper.
  await page.route('**/api/v1/channels/*/routing', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    } else {
      await route.continue()
    }
  })

  // Stub agents and workspaces to prevent 404s if polled
  await page.route('**/api/v1/agents', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    } else {
      await route.continue()
    }
  })

  await page.route('**/api/v1/workspaces?**', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    } else {
      await route.continue()
    }
  })
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

// ── (b) Invalid slug → submit blocked + hint shown ──────────────────────────

test(
  '(b) invalid slug blocks submit and shows slug hint',
  async ({ page }) => {
    await registerBaseRoutes(page)
    await gotoConnectors(page)

    // Open the create-channel Sheet
    const addBtn = page.getByTestId('add-channel-instance-btn')
    await expect(addBtn).toBeVisible({ timeout: 15_000 })
    await addBtn.click()

    const dialog = page.getByTestId('create-channel-sheet')
    await expect(dialog).toBeVisible({ timeout: 8_000 })

    // Select a channel type — use the type selector
    const typeSelect = dialog.getByTestId('create-channel-type-select')
    await expect(typeSelect).toBeVisible({ timeout: 5_000 })
    // Click to open (Radix Select in real browser)
    await typeSelect.click()
    // Select 'telegram' from the dropdown
    const telegramOption = page.getByRole('option', { name: 'telegram' })
    await expect(telegramOption).toBeVisible({ timeout: 5_000 })
    await telegramOption.click()

    // Enter an invalid slug (uppercase — fails [a-z0-9-]{1,32})
    const slugInput = dialog.getByTestId('create-channel-slug-input')
    await slugInput.fill('EU')

    // Slug error hint must appear
    const slugError = dialog.getByTestId('create-channel-slug-error')
    await expect(slugError).toBeVisible({ timeout: 5_000 })
    await expect(slugError).toContainText(/lowercase/i)

    // Submit button must be disabled
    const submitBtn = dialog.getByTestId('create-channel-submit-btn')
    await expect(submitBtn).toBeDisabled()
  },
)

// ── (c+d) Valid type + slug → POST fires → instance appears ─────────────────
//
// The fixed backend emits one row per cfg.Channels instance. After creating
// telegram.eu the GET /channels response includes BOTH telegram.us (the
// pre-existing instance) and telegram.eu (the newly created one) as distinct
// per-instance entries — two rows for the same base type. This test asserts
// that per-instance behaviour: both cards appear after the create.

test(
  '(c+d) valid type+slug fires POST and the new instance appears in the channel list',
  async ({ page }) => {
    let capturedPostBody: string | null = null

    // First GET returns the initial channel list (telegram.us already exists);
    // after POST + second GET returns the updated list (telegram.us + telegram.eu).
    // Both responses carry `instance_id` matching the per-instance backend shape.
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
          body: JSON.stringify({ id: 'telegram.eu', instance_id: 'telegram.eu', type: 'telegram', enabled: false }),
        })
      } else {
        await route.continue()
      }
    })

    // Stub per-instance config fetches (telegram.us, telegram.eu)
    await page.route('**/api/v1/channels/telegram*', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
      } else {
        await route.continue()
      }
    })

    // Stub other endpoints
    await page.route('**/api/v1/agents', async (route: Route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })
    await page.route('**/api/v1/workspaces?**', async (route: Route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })

    await gotoConnectors(page)

    // Pre-existing telegram.us card must be visible before any action
    const existingCard = page.getByTestId('channel-card-telegram.us')
    await expect(existingCard).toBeVisible({ timeout: 15_000 })

    // Open the dialog
    const addBtn = page.getByTestId('add-channel-instance-btn')
    await expect(addBtn).toBeVisible({ timeout: 5_000 })
    await addBtn.click()

    const dialog = page.getByTestId('create-channel-sheet')
    await expect(dialog).toBeVisible({ timeout: 8_000 })

    // Select type
    const typeSelect = dialog.getByTestId('create-channel-type-select')
    await typeSelect.click()
    const telegramOption = page.getByRole('option', { name: 'telegram' })
    await expect(telegramOption).toBeVisible({ timeout: 5_000 })
    await telegramOption.click()

    // Enter a valid slug
    const slugInput = dialog.getByTestId('create-channel-slug-input')
    await slugInput.fill('eu')

    // No slug error must be shown
    await expect(dialog.getByTestId('create-channel-slug-error')).not.toBeVisible()

    // Submit
    const submitBtn = dialog.getByTestId('create-channel-submit-btn')
    await expect(submitBtn).not.toBeDisabled()
    await submitBtn.click()

    // (c) Verify POST body
    await expect
      .poll(() => capturedPostBody, {
        timeout: 5_000,
        message: 'POST /api/v1/channels was not called after form submit',
      })
      .not.toBeNull()

    const body = JSON.parse(capturedPostBody!) as { type?: string; slug?: string }
    expect(body.type).toBe('telegram')
    expect(body.slug).toBe('eu')

    // Dialog should close after success
    await expect(dialog).not.toBeVisible({ timeout: 5_000 })

    // (d) Both per-instance rows appear in the channel list after the create.
    // The pre-existing telegram.us row must still be present AND the new
    // telegram.eu row must appear — proving the backend emits one row per
    // instance (not one row per type).
    const newCard = page.getByTestId('channel-card-telegram.eu')
    await expect(newCard).toBeVisible({ timeout: 8_000 })
    await expect(newCard).toContainText('telegram.eu')
    // Pre-existing instance must still be listed (two-per-type assertion)
    await expect(existingCard).toBeVisible()
    await expect(existingCard).toContainText('telegram.us')

    // TDD #31 / US-9, AS-1: instances group under their channel type — one
    // "telegram" group header contains BOTH per-instance rows, not two
    // separate top-level cards.
    const telegramGroup = page.getByTestId('channel-type-group-telegram')
    await expect(telegramGroup).toBeVisible()
    await expect(telegramGroup.getByTestId('channel-card-telegram.us')).toBeVisible()
    await expect(telegramGroup.getByTestId('channel-card-telegram.eu')).toBeVisible()
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

    await page.route('**/api/v1/agents', async (route: Route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })
    await page.route('**/api/v1/workspaces?**', async (route: Route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })

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

    await page.route('**/api/v1/agents', async (route: Route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })
    await page.route('**/api/v1/workspaces?**', async (route: Route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })

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
// same binding-title behaviour on the pre-existing telegram.us fixture: the
// row resolves its routing (workspace_id + default_agent_id) and the
// workspace/agent id lists to human-readable names.

test(
  '(g) configured instance row shows the "Sales → Mia" workspace→agent binding',
  async ({ page }) => {
    await registerBaseRoutes(page)

    // Override the base (unbound) routing stub: telegram.us is bound to
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

    // Override the base (empty-array) workspaces/agents stubs with
    // name-bearing fixtures so the row can resolve "ws1" -> "Sales" and
    // "mia" -> "Mia". Full required-field shapes per the Workspace/Agent
    // contract schemas (contracts/components/schemas/{Workspace,Agent}.yaml)
    // so the SPA's runtime zod validation doesn't drop the entries.
    await page.route('**/api/v1/workspaces?**', async (route: Route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([
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
          ]),
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
          body: JSON.stringify([
            {
              id: 'mia',
              name: 'Mia',
              type: 'core',
              locked: true,
              status: 'active',
              soul: '',
              timeout_seconds: 300,
              max_tool_iterations: 25,
              steering_mode: 'one-at-a-time',
            },
          ]),
        })
      } else {
        await route.continue()
      }
    })

    await gotoConnectors(page)

    const binding = page.getByTestId('channel-binding-telegram.us')
    await expect(binding).toBeVisible({ timeout: 15_000 })
    await expect(binding).toContainText('Sales → Mia')
  },
)
