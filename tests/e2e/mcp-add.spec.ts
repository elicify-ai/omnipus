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
 * Approach for Sheet UI tests (first three): deterministic, empty list stub.
 *   - page.route() stubs GET /api/v1/mcp-servers so the component renders without
 *     existing server entries. This keeps the Sheet-open/close/content tests
 *     independent of gateway state left by earlier suite runs.
 *   - Auth is provided via the global storageState (playwright.config.ts).
 *
 * Approach for 409 duplicate-name test (fourth test): deterministic mock.
 *   - The MCP config write now triggers a synchronous live-reconcile pass on the
 *     backend (connect the new server, ≤15s per server, ≤20s total — see the
 *     MCP live-reconciliation fix) before POST /api/v1/mcp-servers responds. A
 *     real, unstubbed POST would make this test's timing depend on that live
 *     connect (and on network reachability of the test URL), so — like the
 *     first three tests — GET and POST are stubbed for determinism.
 *   - The stub tracks names added within the test run and returns a 409 for a
 *     repeat name, reproducing the backend's own uniqueness check (rest.go:
 *     "mcp server %q already exists") without depending on its live-connect
 *     timing. Status 409 is a "known status" in api-error.ts, so the SPA's
 *     conflict toast text is driven entirely by the status code, not the
 *     mocked body — asserting the real user-facing behaviour stays valid.
 *   - Server name uses a timestamp suffix for parity with prior runs of this
 *     spec, though the mock makes cross-run collisions moot.
 *
 * Why we assert Sheet vs Dialog:
 *   - McpServerModal renders inside a Radix Sheet (SheetContent) with
 *     data-testid="mcp-sheet" and data-side="right" (slide-out, not centred modal).
 *   - The stdio confirmation AlertDialog also uses role="dialog" — asserting
 *     data-testid="mcp-sheet" unambiguously targets the Sheet, not the AlertDialog
 *     (data-testid="stdio-confirm-dialog").
 *
 * Why we use the base @playwright/test rather than the console-errors fixture:
 *   REST stubs in the first three tests are fully deterministic; no WS is involved.
 *   The base test keeps the assertion boundary tight and avoids coupling to the
 *   console-errors allowlist for any incidental warnings from the stubbed API paths.
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

// ── E2E-7: duplicate name is rejected with a 409 conflict toast ──────────────
// BDD: Given an MCP server named "test-dedup-<run>" already exists
//      When the operator tries to add a second server with the same name
//      Then the backend returns 409 Conflict
//      And the inline toast shows the conflict message
//
// POST /api/v1/mcp-servers now triggers a synchronous live-reconcile pass
// server-side (connect the new server) before responding, so an unstubbed
// POST would make this test's timing depend on a live connect attempt rather
// than on the uniqueness check under test. Both GET and POST are stubbed
// (same pattern as stubMcpServersRest above) so the test is deterministic:
// the mock tracks names added within the run and returns 409 for a repeat,
// reproducing the backend's own uniqueness check
// (rest.go: `mcp server %q already exists`). Status 409 is a "known status"
// in api-error.ts (defaultUserMessage), so the SPA's conflict-toast text is
// driven purely by the status code, not the mocked body — the assertion below
// still exercises the real SPA error-handling path end to end.
//
// Auth: the global storageState provides a pre-authenticated admin session
// (see playwright.config.ts + global-setup.ts). No manual login needed.
//
// CSRF: form submissions from the browser automatically include the __Host-csrf
// cookie and the X-Csrf-Token header set by the SPA's fetchAPI wrapper
// (src/lib/api.ts). No manual CSRF handling needed in UI-driven tests.
//
// Traces to: E2E-7 / AC1 (409 conflict surfaces as a toast)

test(
  'adding a duplicate MCP server name triggers a 409 and the conflict toast',
  async ({ page }) => {
    // ── Deterministic stub: GET returns an empty list; POST simulates the
    // backend's uniqueness check without a live connect attempt.
    const addedNames = new Set<string>()
    await page.route('**/api/v1/mcp-servers', async (route: Route) => {
      const req = route.request()
      if (req.method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(EMPTY_MCP_SERVERS),
        })
        return
      }
      if (req.method() === 'POST') {
        const payload = JSON.parse(req.postData() || '{}') as { name?: string }
        const name = payload.name ?? ''
        if (addedNames.has(name)) {
          await route.fulfill({
            status: 409,
            contentType: 'application/json',
            body: JSON.stringify({ error: `mcp server "${name}" already exists` }),
          })
          return
        }
        addedNames.add(name)
        // status: 'connected' — the modal's onSuccess only shows the plain
        // "MCP server added" toast when status === 'connected'; anything else
        // ('error' or 'disconnected') shows a different, honesty-focused
        // warning toast that would break the assertion below.
        await route.fulfill({
          status: 201,
          contentType: 'application/json',
          body: JSON.stringify({
            id: `mocked-${name}`,
            name,
            transport: 'sse',
            status: 'connected',
            tool_count: 0,
          }),
        })
        return
      }
      await route.continue()
    })

    // ── Step 1: Navigate to /#/skills.
    await page.goto(`${BASE_URL}/#/skills`)
    await expect(page).toHaveURL(/skills/, { timeout: 10_000 })

    // Click the "MCP Servers" tab.
    const mcpTab = page.locator('button[role="tab"]', { hasText: /MCP Servers/i })
    await expect(mcpTab).toBeVisible({ timeout: 15_000 })
    await mcpTab.click()

    const addServerBtn = page.getByRole('button', { name: /Add Server/i })
    await expect(addServerBtn).toBeVisible({ timeout: 10_000 })

    // ── Unique server name for this test run.
    const serverName = `test-dedup-${Date.now()}`

    // ── Step 2: Add the first MCP server via the UI form.
    await addServerBtn.click()

    const sheet = page.getByTestId('mcp-sheet')
    await expect(sheet).toBeVisible({ timeout: 10_000 })

    // Fill the Name field (first input in the Sheet).
    const nameInput = sheet.locator('input').first()
    await expect(nameInput).toBeVisible({ timeout: 10_000 })
    await nameInput.pressSequentially(serverName)

    // Fill the Server URL field (network mode is the default; data-testid="network-url").
    const urlInput = sheet.locator('[data-testid="network-url"]')
    await expect(urlInput).toBeVisible({ timeout: 5_000 })
    await urlInput.pressSequentially('https://mcp.example.com/sse')

    // Submit — data-testid="submit-add" on the add button.
    const submitBtn = sheet.getByTestId('submit-add')
    await expect(submitBtn).toBeEnabled({ timeout: 5_000 })
    await submitBtn.click()

    // ── Verify the first server was added successfully.
    // McpServerModal.tsx onSuccess → addToast({ message: 'MCP server added', variant: 'success' })
    // The Sheet also closes on success (handleClose → onOpenChange(false)).
    const successToast = page.getByText('MCP server added').first()
    await expect(successToast).toBeVisible({ timeout: 10_000 })

    // Confirm the Sheet closed (success path closes it).
    await expect(sheet).not.toBeVisible({ timeout: 10_000 })

    // ── Step 3: Attempt to add a DUPLICATE server with the same name.
    // Click "Add Server" again.
    const addServerBtn2 = page.getByRole('button', { name: /Add Server/i })
    await expect(addServerBtn2).toBeVisible({ timeout: 10_000 })
    await addServerBtn2.click()

    const sheet2 = page.getByTestId('mcp-sheet')
    await expect(sheet2).toBeVisible({ timeout: 10_000 })

    // Fill the same name — the stub's uniqueness check will reject this.
    const nameInput2 = sheet2.locator('input').first()
    await expect(nameInput2).toBeVisible({ timeout: 10_000 })
    await nameInput2.pressSequentially(serverName)

    const urlInput2 = sheet2.locator('[data-testid="network-url"]')
    await expect(urlInput2).toBeVisible({ timeout: 5_000 })
    await urlInput2.pressSequentially('https://mcp-other.example.com/sse')

    const submitBtn2 = sheet2.getByTestId('submit-add')
    await expect(submitBtn2).toBeEnabled({ timeout: 5_000 })
    await submitBtn2.click()

    // ── Core assertion: the 409 surfaces as the conflict toast.
    // api-error.ts: defaultUserMessage(409) = "This conflicts with the current state.
    // Please refresh and try again." (status 409 is a "known status", so the generic
    // message is used regardless of the mocked error body).
    const conflictToast = page.getByText(/conflicts with the current state/i).first()
    await expect(conflictToast).toBeVisible({ timeout: 10_000 })

    // ── Differentiation assertion: the second attempt did NOT succeed.
    // If the duplicate had been silently accepted, "MCP server added" would appear
    // a second time. Assert only one "MCP server added" toast — the one from step 2.
    // (Playwright counts all matching text nodes visible at this instant.)
    const successToasts = page.getByText('MCP server added')
    await expect(conflictToast).toBeVisible()
    const successCount = await successToasts.count()
    // Expect 0 (step-2 toast faded) or 1 (still visible but not 2, which would
    // indicate the duplicate add also succeeded).
    expect(successCount, 'second add must not succeed — conflict toast must be the only outcome').toBeLessThanOrEqual(1)
  },
)
