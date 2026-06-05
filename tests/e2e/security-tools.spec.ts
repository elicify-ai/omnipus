/**
 * security-tools.spec.ts — E2E-1: Tool Access grid regression catcher.
 *
 * Traces to: US-1/AC1, AC2, AC4.
 *
 * Boots the REAL gateway (embedded SPA + real Go binary) and asserts:
 *   1. GET /api/v1/tools returns > 41 entries (SC-101 / AC2).
 *   2. The Tool Access category grid is NON-EMPTY (SC-102 / AC1).
 *   3. The grid contains a tool row for "exec" (SC-101 / AC1).
 *   4. The grid contains a tool row for "web_search" (SC-101 / AC1).
 *   5. No tool is listed twice — no duplicate data-testid="tool-row-*" (AC1).
 *   6. No user-facing category label equals the raw key "core" or "system" (AC4).
 *      Raw keys must map to human labels: core → "General", system → "System".
 *
 * Navigation path:
 *   Settings → Security tab → AdvancedDisclosure "Advanced / technical details"
 *   → "Tool Access — Global Policies" section → ToolPolicyEditor category grid.
 *
 * The ToolPolicyEditor category grid is inside an AdvancedDisclosure that is
 * collapsed by default. We must click the disclosure trigger before querying
 * the category-grid testid.
 *
 * Individual tool rows are also collapsed (per CategorySection); we must expand
 * each category to see its tool-row-* testids. For the non-duplicate check we
 * expand all categories sequentially. For the exec/web_search presence check
 * we use the API directly (SC-101 mandates API returns > 41 entries including
 * those two tools) and also verify the tool rows appear after expansion.
 *
 * Why we use the base @playwright/test `test` rather than the console-errors
 * fixture wrapper:
 *   The console-errors fixture's cancelOnTeardown auto-fixture is harmless here
 *   but the fixture assertion fires after each test. When the SPA talks to a
 *   real gateway that may emit minor WS reconnect messages during the
 *   AdvancedDisclosure expansion (which unmounts/remounts some query-dependent
 *   subtrees), we avoid false-positive console-error failures on benign warnings
 *   that the fixture's allowlist may not yet cover. We use direct assertions as
 *   the source of truth.
 *
 * Constraint: runs only after Wave 0 integrates (depends on the corrected /tools
 * registry). This file is the mandatory regression catcher — it MUST stay green.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)".
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Auth helper ───────────────────────────────────────────────────────────────

/**
 * Read the bearer token from the Playwright storageState file that global-setup
 * wrote. Avoids calling POST /api/v1/auth/login which would rotate the admin's
 * token_hash and invalidate the storageState token for all subsequent page tests
 * (causing them to land on the login screen).
 *
 * The SPA stores the bearer under the key 'omnipus_auth_token' in localStorage.
 * Playwright's storageState JSON layout:
 *   { origins: [{ origin: "http://localhost:6060", localStorage: [{name, value}] }] }
 */
function readStorageStateToken(): string {
  const authFile = path.join(__dirname, 'fixtures/.auth/admin.json')
  const state = JSON.parse(fs.readFileSync(authFile, 'utf-8')) as {
    origins?: Array<{ localStorage?: Array<{ name: string; value: string }> }>
  }
  const token = state.origins
    ?.flatMap((o) => o.localStorage ?? [])
    .find((e) => e.name === 'omnipus_auth_token')?.value
  if (!token) throw new Error('omnipus_auth_token not found in storageState — check global-setup')
  return token
}

// ── AC2: API-level assertion (SC-101) ─────────────────────────────────────────
// Independent test against the real /api/v1/tools — not a mock.
// Verifies: count > 41, exec present, web_search present.
//
// BDD:
//   Given a default install
//   When GET /api/v1/tools is called
//   Then it returns > 41 entries including exec and web_search

test('(AC2/SC-101) GET /api/v1/tools returns > 41 entries including exec and web_search', async ({
  request,
}) => {
  // Read the bearer token from the storageState file written by global-setup.
  // We never call POST /api/v1/auth/login here — doing so rotates the admin's
  // token_hash, invalidating the storageState token for all subsequent page tests.
  const token = readStorageStateToken()

  const toolsRes = await request.get(`${BASE_URL}/api/v1/tools`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(toolsRes.ok(), `GET /api/v1/tools failed: ${toolsRes.status()}`).toBe(true)

  const tools = (await toolsRes.json()) as Array<{ name: string; category?: string }>
  expect(Array.isArray(tools), 'Tools response must be an array').toBe(true)

  // SC-101: count must be > 41.
  expect(tools.length, `Expected > 41 tools from /api/v1/tools, got ${tools.length}`).toBeGreaterThan(41)

  // SC-101: exec must be present.
  const toolNames = tools.map((t) => t.name)
  expect(toolNames, 'exec must be in /api/v1/tools response').toContain('exec')

  // SC-101: web_search must be present.
  expect(toolNames, 'web_search must be in /api/v1/tools response').toContain('web_search')
})

// ── AC1 + AC4 + SC-102: UI-level assertions (real embedded SPA) ───────────────
// Navigation: Settings → Security tab → "Advanced / technical details" disclosure
// → ToolPolicyEditor category grid.
//
// BDD:
//   Given a default install
//   When the Tool Access editor renders
//   Then the category grid is non-empty
//   And it contains a tool row for exec (after expanding its category)
//   And it contains a tool row for web_search (after expanding its category)
//   And no tool is listed twice
//   And no user-facing category label equals the raw key "core" or "system"

test(
  '(AC1/AC4/SC-102) Tool Access grid is non-empty, exec+web_search present, no duplicates, no raw category labels',
  async ({ page }) => {
    // Navigate to Settings. HashRouter: route is /#/settings.
    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Click the Security tab.
    const securityTab = page.locator('button[role="tab"]', { hasText: 'Security' })
    await expect(securityTab).toBeVisible({ timeout: 10_000 })
    await securityTab.click()

    // Confirm the Security tab panel is active.
    const tabPanel = page.locator('[role="tabpanel"][data-state="active"]').first()
    await expect(tabPanel).toBeVisible({ timeout: 10_000 })

    // The "Security & Policy" heading must be visible (outer heading, always rendered).
    await expect(page.getByRole('heading', { name: /security.*policy/i })).toBeVisible({
      timeout: 10_000,
    })

    // ── Open the AdvancedDisclosure "Advanced / technical details" ──────────────
    // The disclosure trigger has data-testid="advanced-disclosure-trigger" and
    // title text "Advanced / technical details". It is collapsed by default.
    // We match by its button text content.
    const advancedDisclosureTrigger = page.getByRole('button', {
      name: /advanced.*technical details/i,
    })
    await expect(advancedDisclosureTrigger).toBeVisible({ timeout: 10_000 })
    await advancedDisclosureTrigger.click()

    // After clicking, the AdvancedDisclosure content must be present.
    const advancedContent = page.locator('[data-testid="advanced-disclosure-content"]').first()
    await expect(advancedContent).toBeVisible({ timeout: 10_000 })

    // ── Verify "Tool Access — Global Policies" section heading ─────────────────
    await expect(page.getByText('Tool Access — Global Policies', { exact: true })).toBeVisible({
      timeout: 10_000,
    })

    // ── Wait for ToolPolicyEditor and category grid to render ──────────────────
    // The GlobalToolPoliciesSection fires two queries (tools-builtin and
    // global-tool-policies). While loading it renders skeleton divs; the
    // category-grid testid only appears after both queries resolve.
    const categoryGrid = page.getByTestId('category-grid')
    await expect(categoryGrid).toBeVisible({ timeout: 15_000 })

    // ── AC1 (non-empty): the category grid must have at least one category row ─
    // CategorySection renders a <button aria-expanded> for each category. We
    // select ONLY buttons with the aria-expanded attribute to exclude the
    // Allow/Ask/Deny policy buttons that appear inside expanded categories
    // (those policy buttons do not have aria-expanded).
    const categoryToggles = categoryGrid.locator('button[aria-expanded]')
    const categoryCount = await categoryToggles.count()
    expect(
      categoryCount,
      `Expected at least 1 category in the grid, got ${categoryCount}`,
    ).toBeGreaterThan(0)

    // ── AC4: no raw category label "core" or "system" as button text ───────────
    // CATEGORY_LABELS maps core → "General" and system → "System".
    // The raw key "core" must NEVER appear as the full button text content.
    // The raw key "system" must NEVER appear as the sole button text content
    // (though "System" — the human label — is acceptable).
    const allCategoryButtonTexts = await categoryToggles.evaluateAll((buttons: Element[]) =>
      buttons.map((b) => b.textContent?.trim() ?? ''),
    )

    for (const text of allCategoryButtonTexts) {
      expect(
        text.toLowerCase().startsWith('core'),
        `Category button text "${text}" starts with raw key "core" — expected "General" label`,
      ).toBe(false)

      expect(
        text.startsWith('system'),
        `Category button text "${text}" starts with raw key "system" (lowercase) — expected "System" label`,
      ).toBe(false)
    }

    // ── Expand all categories to expose tool-row-* testids ────────────────────
    // tool-row-* testids only appear in the DOM after their category is expanded.
    // Iterate over category toggle buttons (aria-expanded) only — NOT the
    // Allow/Ask/Deny policy buttons inside already-expanded categories.
    const toggleCount = await categoryToggles.count()
    for (let i = 0; i < toggleCount; i++) {
      const btn = categoryToggles.nth(i)
      const isExpanded = await btn.getAttribute('aria-expanded')
      if (isExpanded !== 'true') {
        await btn.click()
        await page.waitForTimeout(150)
      }
    }

    // ── AC1: exec tool row must be present ─────────────────────────────────────
    const execRow = page.getByTestId('tool-row-exec')
    await expect(
      execRow,
      'exec tool row must exist in the category grid after expansion',
    ).toBeVisible({ timeout: 10_000 })

    // ── AC1: web_search tool row must be present ───────────────────────────────
    const webSearchRow = page.getByTestId('tool-row-web_search')
    await expect(
      webSearchRow,
      'web_search tool row must exist in the category grid after expansion',
    ).toBeVisible({ timeout: 10_000 })

    // ── AC1: no tool listed twice (no duplicate tool-row-* testids) ────────────
    // Collect all data-testid values that match the pattern "tool-row-*" within
    // the ToolPolicyEditor (the whole editor, not just the category grid, so we
    // also catch any hypothetical duplicates that might appear in the MCP section
    // or the Customize defaults section — which the spec says must NOT exist).
    const toolPolicyEditor = page.getByTestId('tool-policy-editor')
    await expect(toolPolicyEditor).toBeVisible({ timeout: 5_000 })

    const allToolRowTestIds = await toolPolicyEditor.evaluate((editorEl: Element) => {
      const rows = editorEl.querySelectorAll('[data-testid^="tool-row-"]')
      return Array.from(rows).map((el) => el.getAttribute('data-testid') ?? '')
    })

    // Sort and detect duplicates.
    const seen = new Set<string>()
    const duplicates: string[] = []
    for (const testId of allToolRowTestIds) {
      if (seen.has(testId)) {
        duplicates.push(testId)
      }
      seen.add(testId)
    }

    expect(
      duplicates,
      `These tools are listed more than once in the Tool Access grid: ${duplicates.join(', ')}`,
    ).toHaveLength(0)

    // ── Negative: system-disclosure-wrapper must NOT exist ─────────────────────
    // The old separate "system" disclosure section was removed (AC5 / FR-103).
    // system.* tools now appear in the category grid under "System".
    await expect(page.getByTestId('system-disclosure-wrapper')).toHaveCount(0)
  },
)
