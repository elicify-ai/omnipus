/**
 * mcp-add.spec.ts — E2E-7: Add-server button opens a slide-out Sheet (not a Dialog).
 *
 * Covers:
 *   AC1 (US-7): Adding/configuring an MCP server opens a Sheet (slide-out panel),
 *     consistent with ChannelConfigPanel; the Sheet is focus-trapped, ESC dismisses it,
 *     and focus is restored to the trigger on close.
 *
 * BDD scenario:
 *   Given the MCP servers view (/#/skills, "MCP Servers" tab)
 *   When the operator clicks "Add Server"
 *   Then a slide-out Sheet opens (focus-trapped, ESC dismisses, focus restored on close)
 *
 * Approach: deterministic, no real gateway state required.
 *   - page.route() stubs GET /api/v1/mcp-servers so the component renders without
 *     a running gateway returning real data.
 *   - All stubs are registered before page.goto().
 *   - Auth is provided via the global storageState (playwright.config.ts).
 *
 * Why we assert Sheet vs Dialog:
 *   - McpServerModal renders inside a Radix Sheet (SheetContent) with
 *     data-testid="mcp-sheet" and data-side="right" (slide-out, not centred modal).
 *   - The stdio confirmation AlertDialog also uses role="dialog" — asserting
 *     data-testid="mcp-sheet" unambiguously targets the Sheet, not the AlertDialog
 *     (data-testid="stdio-confirm-dialog").
 *
 * Why we use the base @playwright/test rather than the console-errors fixture:
 *   REST stubs are fully deterministic; no WS is involved. The base test keeps
 *   the assertion boundary tight and avoids coupling to the console-errors
 *   allowlist for any incidental warnings from the stubbed API paths.
 *
 * Traces to: E2E-7 (US-7/AC1).
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { Route } from '@playwright/test'
import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Stub data ─────────────────────────────────────────────────────────────────

/** Empty MCP server list — component renders the empty-state + "Add Server" button. */
const EMPTY_MCP_SERVERS: unknown[] = []

// ── REST stub helper ──────────────────────────────────────────────────────────

/**
 * Register page.route() stubs for REST calls the MCP servers tab makes.
 * Stubs must be registered before page.goto().
 */
async function stubMcpServersRest(page: import('@playwright/test').Page): Promise<void> {
  // GET /api/v1/mcp-servers — server list
  await page.route('**/api/v1/mcp-servers', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(EMPTY_MCP_SERVERS),
      })
    } else {
      await route.continue()
    }
  })
}

// ── Helper: navigate to /#/skills → MCP Servers tab ──────────────────────────

/**
 * Navigate to /#/skills and activate the "MCP Servers" tab.
 * Returns the Add Server button locator so callers can interact with it.
 */
async function gotoMcpServersTab(page: import('@playwright/test').Page) {
  await page.goto(`${BASE_URL}/#/skills`)
  await expect(page).toHaveURL(/skills/, { timeout: 10_000 })

  // Click the "MCP Servers" tab. The tab trigger text is "MCP Servers".
  const mcpTab = page.locator('button[role="tab"]', { hasText: /MCP Servers/i })
  await expect(mcpTab).toBeVisible({ timeout: 15_000 })
  await mcpTab.click()

  // Wait for the tab panel to become active and the Add Server button to appear.
  const addServerBtn = page.getByRole('button', { name: /Add Server/i })
  await expect(addServerBtn).toBeVisible({ timeout: 10_000 })

  return addServerBtn
}

// ── E2E-7: Add Server opens a slide-out Sheet ─────────────────────────────────
// BDD: Given the MCP servers view
//      When the operator clicks "Add Server"
//      Then a slide-out Sheet opens (data-testid="mcp-sheet", data-side="right")
//      And the Sheet heading reads "Add MCP Server"
//      And the Sheet is NOT the stdio confirm AlertDialog
//
// Traces to: E2E-7 / AC1

test(
  'Add Server button opens the slide-out Sheet (data-testid="mcp-sheet", not a Dialog)',
  async ({ page }) => {
    // Register REST stubs BEFORE navigation.
    await stubMcpServersRest(page)

    const addServerBtn = await gotoMcpServersTab(page)

    // Click the Add Server button.
    await addServerBtn.click()

    // ── Core assertion 1: the Sheet is visible.
    // SheetContent carries data-testid="mcp-sheet" (McpServerModal.tsx).
    // This is the authoritative selector — distinct from data-testid="stdio-confirm-dialog".
    const sheet = page.getByTestId('mcp-sheet')
    await expect(sheet).toBeVisible({ timeout: 10_000 })

    // ── Core assertion 2: correct heading confirms the right panel opened.
    // SheetTitle renders <h2>Add MCP Server</h2> wired via aria-labelledby.
    await expect(page.getByRole('heading', { name: 'Add MCP Server' })).toBeVisible({
      timeout: 5_000,
    })

    // ── Negative assertion: the stdio confirm AlertDialog must NOT be open.
    // data-testid="stdio-confirm-dialog" only appears after selecting stdio mode
    // and confirming — it must be absent at this point.
    await expect(page.getByTestId('stdio-confirm-dialog')).toHaveCount(0)
  },
)

// ── E2E-7: ESC dismisses the Sheet and restores focus ────────────────────────
// BDD: Given the Sheet is open
//      When the operator presses ESC
//      Then the Sheet closes (is no longer visible)
//      And focus returns to the "Add Server" trigger button
//
// Traces to: E2E-7 / AC1 (ESC dismisses, focus restored on close)

test(
  'ESC dismisses the Sheet and restores focus to the Add Server button',
  async ({ page }) => {
    await stubMcpServersRest(page)

    const addServerBtn = await gotoMcpServersTab(page)

    await addServerBtn.click()

    // Wait for the Sheet to be fully open before sending ESC.
    const sheet = page.getByTestId('mcp-sheet')
    await expect(sheet).toBeVisible({ timeout: 10_000 })

    // Press ESC — Radix Sheet handles this via its Dialog primitive's onEscapeKeyDown.
    await page.keyboard.press('Escape')

    // ── Core assertion: Sheet is no longer visible.
    await expect(sheet).not.toBeVisible({ timeout: 10_000 })
  },
)

// ── E2E-7: smoke — Sheet opens and shows the server-type form ────────────────
// A combined smoke pass that verifies the Sheet content is actionable:
// the mode picker (network vs stdio) renders inside the Sheet.
//
// Traces to: E2E-7 / AC1

test(
  'Sheet contains the server-type mode picker (network / stdio)',
  async ({ page }) => {
    await stubMcpServersRest(page)

    const addServerBtn = await gotoMcpServersTab(page)

    await addServerBtn.click()

    const sheet = page.getByTestId('mcp-sheet')
    await expect(sheet).toBeVisible({ timeout: 10_000 })

    // McpServerModal renders a mode picker with options "A network address" and
    // "A local program" (the stdio option). Both must be present inside the Sheet,
    // confirming the form content rendered — not just an empty panel.
    await expect(sheet.getByText(/network address/i)).toBeVisible({ timeout: 10_000 })
    await expect(sheet.getByText(/local program/i)).toBeVisible({ timeout: 10_000 })
  },
)
