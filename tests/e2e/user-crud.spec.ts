/**
 * user-crud.spec.ts — E2E: admin creates second admin, no token modal
 *
 * Last-admin guard: admin creates second admin who can log in and administer.
 *
 * IMPORTANT: This spec manages its own gateway lifecycle (port 5050).
 * It does NOT rely on the globally-started gateway from global-setup.ts.
 * It uses a throwaway OMNIPUS_HOME to avoid polluting the shared fixture state.
 *
 * Gateway is started via OMNIPUS_BINARY env (default: /tmp/omnipus-ci).
 * Run with:
 *   OMNIPUS_BINARY=/tmp/omnipus-ci npx playwright test tests/e2e/user-crud.spec.ts
 *
 * CONTRACT BEING TESTED:
 *   - Create user: no token shown, toast says "User created. They can now log in with the password you set."
 *   - Login as new admin succeeds, Access tab visible (admin role works)
 *   - Second admin can delete first admin (not last admin, so allowed)
 *   - Last-admin guard: deleting the only remaining admin is blocked by UI
 *   - Access tab is visible for admins when dev_mode_bypass is off
 */

import { test, expect, type Page } from '@playwright/test';
import {
  startGateway,
  stopGateway,
  assertUserManagementEmbedPresent,
  getFreePort,
  type GatewayHandle,
} from './setup.js';

// ── Gateway port (resolved to ephemeral at runtime) ────────────────────────────

let GATEWAY_PORT: number;
let GATEWAY_URL: string;

const FIRST_ADMIN = { username: 'first-admin', password: 'first-admin-pass' };
const SECOND_ADMIN = { username: 'second-admin', password: 'second-admin-pass' };

// ── Isolated auth state (no shared storageState) ───────────────────────────────

// This spec manages its own auth — it starts a blank gateway instance so
// the global admin.json storageState would reference the wrong token anyway.
// baseURL is not set at module level because GATEWAY_PORT is resolved at runtime;
// all page.goto() calls use GATEWAY_URL directly.
test.use({ storageState: { cookies: [], origins: [] } });

// ── Shared gateway handle ──────────────────────────────────────────────────────
let handle: GatewayHandle;

// ── Login helper ──────────────────────────────────────────────────────────────

/**
 * Log into the SPA via the login form.
 * Uses pressSequentially() because fill() does not trigger React onChange.
 */
async function loginViaUI(
  page: Page,
  username: string,
  password: string,
): Promise<void> {
  // Navigate to root — SPA's hash router redirects to /#/login when unauthenticated.
  await page.goto(GATEWAY_URL);
  // Wait for the login form to appear.
  // The SPA uses HashRouter: unauthenticated root redirects to /#/login.
  await expect(page.locator('#login-username')).toBeVisible({ timeout: 15_000 });
  // pressSequentially() is required — fill() does not fire React synthetic onChange,
  // leaving the Sign-in button disabled={!username.trim() || !password}.
  await page.locator('#login-username').pressSequentially(username);
  await page.locator('#login-password').pressSequentially(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  // Success: SPA navigates away from /#/login to /#/ (the app shell).
  // Wait for URL to leave the login page, then wait for the banner landmark.
  await expect(page).not.toHaveURL(/\/#\/login/, { timeout: 20_000 });
  // The AppShell renders a <header> with implicit role="banner".
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });
}

// ── Navigate to Access tab ────────────────────────────────────────────────────

/**
 * Navigate to Settings → Access tab.
 * The SPA uses TanStack Router, so navigate to /settings path.
 */
async function navigateToAccessTab(page: Page): Promise<void> {
  // The SPA uses hash routing (createHashHistory). /settings serves index.html which
  // starts at root — must use /#/settings to route to the settings screen.
  await page.goto(`${GATEWAY_URL}/#/settings`);
  // Wait for the tab list to render
  await expect(page.locator('[role="tablist"]').first()).toBeVisible({ timeout: 15_000 });
  // Click the "Access" tab
  // UsersSection renders when showAccessTab is true (admin role + no dev_mode_bypass)
  const accessTab = page.locator('button[role="tab"]', { hasText: 'Access' });
  await expect(accessTab).toBeVisible({ timeout: 10_000 });
  await accessTab.click();
  // Wait for the Users section to render
  await expect(page.getByRole('table', { name: 'User accounts' })).toBeVisible({
    timeout: 10_000,
  });
}

/**
 * Get the cell in the username column for a given user.
 * The username column is the first <td> in the row — we use the TableBody
 * row, not the header row, to avoid ambiguity.
 *
 * The username cell text is "first-admin(you)" for the own row because
 * UsersSection appends "(you)" — we use the cell's text content which
 * includes the "(you)" suffix. We match against the username prefix.
 */
function getUsernameCell(page: Page, username: string) {
  // Strategy: find the TableCell that contains the username text.
  // The cell also may contain "(you)" span — use hasText regex to match prefix.
  // We search within the table's tbody to avoid header cells.
  return page
    .getByRole('table', { name: 'User accounts' })
    .locator('tbody')
    .getByRole('cell')
    .filter({ hasText: new RegExp(`^${username}`) })
    .first();
}

// ── Spec lifecycle ────────────────────────────────────────────────────────────

test.beforeAll(async () => {
  GATEWAY_PORT = await getFreePort();
  GATEWAY_URL = `http://localhost:${GATEWAY_PORT}`;
  handle = await startGateway({
    port: GATEWAY_PORT,
    adminUsername: FIRST_ADMIN.username,
    adminPassword: FIRST_ADMIN.password,
  });
});

test.afterAll(async () => {
  await stopGateway(handle);
});

// ── Test: SPA embed verification ──────────────────────────────────────────────

test('(a) SPA embed contains user-management UI', async () => {
  // assertUserManagementEmbedPresent() was already called in startGateway().
  // This test documents the contract explicitly so failures are loud.
  assertUserManagementEmbedPresent(); // re-assert in test context for clear FAIL message
});

// ── Test: Admin creates second admin without token modal ───────────────────────

test('(b) admin creates second admin — no token or Copy button appears', async ({ page }) => {
  // BDD: Given admin on Settings → Access → Users
  //      When they click "Add user", fill {username, role=admin, password}
  //      Then POST returns {username, role} with NO token; UI shows success toast without any token string
  //
  await loginViaUI(page, FIRST_ADMIN.username, FIRST_ADMIN.password);
  await navigateToAccessTab(page);

  // ── Given: Users table shows only first-admin ─────────────────────────────
  // Verify first-admin row exists (differentiation: real users are shown)
  // Use specific cell selector to avoid strict-mode violation with Actions cell
  const firstAdminCell = getUsernameCell(page, FIRST_ADMIN.username);
  await expect(firstAdminCell).toBeVisible({ timeout: 10_000 });

  // ── When: click "Add user" ────────────────────────────────────────────────
  await page.getByRole('button', { name: 'Add user' }).click();

  // Wait for the dialog
  await expect(page.getByRole('dialog')).toBeVisible({ timeout: 8_000 });
  await expect(page.getByRole('heading', { name: 'Add user' })).toBeVisible();

  // Fill the form
  await page.locator('#add-username').pressSequentially(SECOND_ADMIN.username);
  // Select admin role (radio button)
  await page.locator('input[type="radio"][value="admin"]').check();
  await page.locator('#add-password').pressSequentially(SECOND_ADMIN.password);

  // ── When: submit ──────────────────────────────────────────────────────────
  await page.getByRole('button', { name: 'Create user' }).click();

  // ── Then: SUCCESS TOAST must appear with the right message ────────────────
  // UsersSection.tsx: 'User created. They can now log in with the password you set.'
  // No token is displayed at creation time.
  await expect(
    page.getByText('User created. They can now log in with the password you set.'),
  ).toBeVisible({ timeout: 15_000 });

  // ── Then: NO token string "omnipus_" anywhere on the page ─────────────────
  // Token creation is removed from the Create flow entirely — no token is displayed.
  const pageContent = await page.content();
  // Multi-token format (SEC-1/#399): omnipus_<8hex id>_<64hex secret>; the
  // legacy single-token form (omnipus_<64hex>) is also matched so a leak of
  // either format is detected.
  expect(pageContent, 'Page must not contain a bearer token string').not.toMatch(
    /omnipus_([0-9a-f]{8}_)?[0-9a-f]{64}/i,
  );

  // ── Then: NO "Copy" button in dialog or toast ─────────────────────────────
  // A "Copy" button would be the tell-tale sign of a token-copy affordance.
  // No one-time-token modal should appear.
  const copyButton = page.getByRole('button', { name: /copy/i }).first();
  // Use toBeHidden rather than toBeVisible negation for reliable assertion
  await expect(copyButton).toBeHidden();

  // ── Then: Dialog is closed and second-admin appears in the table ───────────
  await expect(page.getByRole('dialog')).not.toBeVisible({ timeout: 8_000 });
  // The users table should now show second-admin
  const secondAdminCell = getUsernameCell(page, SECOND_ADMIN.username);
  await expect(secondAdminCell).toBeVisible({ timeout: 10_000 });
});

// ── Test: Second admin can log in ─────────────────────────────────────────────

test('(c) second admin can log in and sees Access tab', async ({ page }) => {
  // BDD: Given second-admin was created with role=admin
  //      When they log in via the login form
  //      Then login succeeds and the Access tab is visible (admin privilege active)
  //
  await loginViaUI(page, SECOND_ADMIN.username, SECOND_ADMIN.password);

  // ── Then: Settings → Access tab is visible for admin ─────────────────────
  // Use hash routing (SPA uses createHashHistory).
  await page.goto(`${GATEWAY_URL}/#/settings`);
  await expect(page.locator('[role="tablist"]').first()).toBeVisible({ timeout: 15_000 });

  const accessTab = page.locator('button[role="tab"]', { hasText: 'Access' });
  await expect(accessTab).toBeVisible({ timeout: 10_000 });

  // Navigate into Access tab to confirm it works (not just tab visible)
  await accessTab.click();
  await expect(page.getByRole('table', { name: 'User accounts' })).toBeVisible({ timeout: 10_000 });

  // Both users are present — use specific username cell selector
  const firstAdminCell = getUsernameCell(page, FIRST_ADMIN.username);
  const secondAdminCell = getUsernameCell(page, SECOND_ADMIN.username);
  await expect(firstAdminCell).toBeVisible({ timeout: 8_000 });
  await expect(secondAdminCell).toBeVisible({ timeout: 8_000 });
});

// ── Test: Second admin deletes first admin ────────────────────────────────────

test('(d) second admin deletes first-admin and deployment remains with >=1 admin', async ({ page }) => {
  // BDD: Given second-admin is logged in as admin
  //      When they delete first-admin (who is NOT the last admin)
  //      Then deletion succeeds, first-admin row disappears, second-admin remains
  //
  await loginViaUI(page, SECOND_ADMIN.username, SECOND_ADMIN.password);
  await navigateToAccessTab(page);

  // Locate the actions menu for first-admin row
  // RowActions renders a button with aria-label="Actions for {username}"
  const firstAdminActionsBtn = page.getByRole('button', {
    name: `Actions for ${FIRST_ADMIN.username}`,
  });
  await expect(firstAdminActionsBtn).toBeVisible({ timeout: 10_000 });
  await firstAdminActionsBtn.click();

  // Dropdown opens — click "Delete"
  const deleteMenuItem = page.getByRole('menuitem', { name: 'Delete' });
  await expect(deleteMenuItem).toBeVisible({ timeout: 8_000 });
  await deleteMenuItem.click();

  // Confirmation dialog opens
  const deleteDialog = page.getByRole('dialog');
  await expect(deleteDialog).toBeVisible({ timeout: 8_000 });
  await expect(deleteDialog.getByText(new RegExp(FIRST_ADMIN.username, 'i'))).toBeVisible();

  // Click the destructive "Delete" button in the dialog
  const confirmDeleteBtn = deleteDialog.getByRole('button', { name: 'Delete' });
  await expect(confirmDeleteBtn).toBeVisible();
  await confirmDeleteBtn.click();

  // ── Then: success toast fires ─────────────────────────────────────────────
  // UsersSection shows a success toast with the deleted username
  await expect(
    page.getByText(new RegExp(`${FIRST_ADMIN.username}.*deleted|deleted.*${FIRST_ADMIN.username}`, 'i')),
  ).toBeVisible({ timeout: 15_000 });

  // ── Then: first-admin row disappears from table ───────────────────────────
  const firstAdminCell = getUsernameCell(page, FIRST_ADMIN.username);
  await expect(firstAdminCell).not.toBeVisible({ timeout: 10_000 });

  // ── Then: second-admin still present (deployment has >=1 admin) ───────────
  const secondAdminCell = getUsernameCell(page, SECOND_ADMIN.username);
  await expect(secondAdminCell).toBeVisible({ timeout: 8_000 });
});

// ── Test: Last-admin guard — UI blocks self-deletion when only admin ───────────

test('(e) last-admin guard: delete button disabled when second-admin is the only admin', async ({ page }) => {
  // BDD: Given second-admin is the only admin remaining
  //      When they open the per-row menu on their own row
  //      Then the Delete option is disabled (grayed out) — UI-level guard
  //
  await loginViaUI(page, SECOND_ADMIN.username, SECOND_ADMIN.password);
  await navigateToAccessTab(page);

  // Open the actions menu for second-admin (their own row)
  const secondAdminActionsBtn = page.getByRole('button', {
    name: `Actions for ${SECOND_ADMIN.username}`,
  });
  await expect(secondAdminActionsBtn).toBeVisible({ timeout: 10_000 });
  await secondAdminActionsBtn.click();

  // The Delete menu item should be disabled
  // UsersSection.tsx: DropdownMenuItem disabled={isOwnRow && isOnlyAdmin}
  // Radix DropdownMenuItem renders aria-disabled="true" when disabled prop is set
  const deleteMenuItem = page.getByRole('menuitem', { name: 'Delete' });
  await expect(deleteMenuItem).toBeVisible({ timeout: 8_000 });

  // Check aria-disabled or data-disabled attribute (Radix pattern)
  const isDisabled =
    (await deleteMenuItem.getAttribute('aria-disabled')) === 'true' ||
    (await deleteMenuItem.getAttribute('data-disabled')) !== null;
  expect(isDisabled, 'Delete menuitem must be disabled when user is the only admin').toBe(true);
});

// ── Test: Backend last-admin guard — direct API call returns 409 ───────────────

test('(f) backend last-admin guard: DELETE /api/v1/users/{last-admin} returns 409', async ({ page }) => {
  // BDD: Given second-admin is the only admin
  //      When a direct API DELETE bypasses the disabled UI button
  //      Then the server returns 409 (guard runs inside safeUpdateConfigJSON callback)
  //
  // This test uses page.request.fetch() to bypass the UI guard and hit the API directly.
  //
  // Precondition: test (d) has already deleted first-admin, so second-admin is the only admin.
  // second-admin tries to DELETE themselves via the API (bypassing the disabled UI button).
  // The backend last-admin guard must fire and return 409.

  await loginViaUI(page, SECOND_ADMIN.username, SECOND_ADMIN.password);

  // Extract the auth token from localStorage (mirrored from sessionStorage by loginViaUI)
  const token = await page.evaluate(() => {
    return (
      localStorage.getItem('omnipus_auth_token') ||
      sessionStorage.getItem('omnipus_auth_token') ||
      ''
    );
  });
  // Multi-token format (SEC-1/#399): omnipus_<8hex id>_<64hex secret>. The
  // optional id segment keeps the legacy single-token form valid too.
  expect(token, 'Auth token must match omnipus_ bearer pattern').toMatch(
    /^omnipus_([0-9a-f]{8}_)?[0-9a-f]{64}$/,
  );

  // Get the CSRF cookie value so we can echo it in the header.
  // The cookie is set by the login flow (rest_auth.go → IssueCSRFCookie).
  const cookies = await page.context().cookies();
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf');

  // If no CSRF cookie, navigate to trigger issuance, then retry.
  let csrfToken = csrfCookie?.value ?? '';
  if (!csrfToken) {
    // GET to an authed endpoint; the browser follows Set-Cookie response headers.
    await page.goto(`${GATEWAY_URL}/api/v1/users`);
    const refreshedCookies = await page.context().cookies();
    const refreshedCsrf = refreshedCookies.find(
      (c) => c.name === '__Host-csrf' || c.name === 'csrf',
    );
    csrfToken = refreshedCsrf?.value ?? '';
  }

  // Attempt to DELETE second-admin (the only remaining admin) via API directly.
  // This bypasses the disabled UI button in test (e) to prove the backend guard is enforced.
  const res = await page.request.delete(`${GATEWAY_URL}/api/v1/users/${SECOND_ADMIN.username}`, {
    headers: {
      Authorization: `Bearer ${token}`,
      ...(csrfToken
        ? { 'X-Csrf-Token': csrfToken, Cookie: `__Host-csrf=${csrfToken}` }
        : {}),
    },
  });

  // The backend returns 409 when the last-admin guard fires.
  expect(res.status(), 'last-admin DELETE must return 409').toBe(409);
  const body = (await res.json()) as { error?: string };
  expect(body.error?.toLowerCase(), 'error message must reference admin/zero/last').toMatch(
    /administrator|zero|last/,
  );
});
