import { expect, chromium } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { loginAs } from './fixtures/login';
import { expectA11yClean } from './fixtures/a11y';
import path from 'path';
import { fileURLToPath } from 'url';

// ADR-044 / US-5 (2026-07-15): auth is the browser-managed `omnipus-session`
// HttpOnly cookie — the SPA never reads or writes a JS-visible bearer token
// (src/lib/api.ts deleted getAuthHeaders(); every request goes out with
// credentials:'include'). `HandleLogin` (pkg/gateway/rest_auth.go) sets the
// cookie via middleware.WriteSessionCookie; `HandleLogout` clears it via
// middleware.ClearSessionCookie. Tests below assert on that cookie directly
// rather than on any local/session storage token.
const SESSION_COOKIE_NAME = 'omnipus-session';

// Mirror playwright.config.ts:storageState and global-setup.ts:AUTH_FILE —
// honor OMNIPUS_AUTH_FILE so the afterAll refreshed-token write lands in the
// same file that storageState reads. Hardcoding the path caused every
// post-auth.spec test to start with the stale (rotated-out) token from the
// pre-auth-spec global-setup write.
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(fileURLToPath(import.meta.url)),
      'fixtures/.auth/admin.json',
    );

// auth.spec.ts manages its own login flows — it tests the login paths themselves.
// Each test explicitly controls its session state; do not use global storageState here.
test.use({ storageState: { cookies: [], origins: [] } });

test('(a) valid credentials land on dashboard', async ({ page }) => {
  await loginAs(page, 'admin', 'admin123');

  // After loginAs succeeds, the banner landmark is visible (enforced by loginAs post-condition).
  // The AppShell renders a plain <header> with implicit ARIA role "banner".
  // The sidebar nav is NOT the auth indicator — it only renders while the overlay drawer is open.
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });
  await expect(page).not.toHaveURL(/\/#\/(login|onboarding)/);

  // ADR-044 / US-5 (S8/S9): login authenticates via the browser-managed
  // omnipus-session HttpOnly cookie — assert the real credential landed in
  // the browser's cookie jar, not just that the UI looks logged in. This is
  // the cookie HandleLogin's middleware.WriteSessionCookie sets; the SPA
  // itself never sees or stores it (HttpOnly), so document.cookie can't be
  // used here — only the Playwright context's cookie jar can observe it.
  const cookies = await page.context().cookies();
  const sessionCookie = cookies.find((c) => c.name === SESSION_COOKIE_NAME);
  expect(sessionCookie, 'omnipus-session cookie must be set after login').toBeTruthy();
  expect(sessionCookie?.httpOnly, 'omnipus-session cookie must be HttpOnly').toBe(true);
  expect(sessionCookie?.value, 'omnipus-session cookie must carry a non-empty token').not.toBe('');

  await expectA11yClean(page);
});

test('(b) wrong password shows inline error and stays on /login', async ({ page }) => {
  // HashRouter: login is at /#/login
  await page.goto('/#/login');

  // Use the exact IDs from login.tsx:110 and :130
  await expect(page.locator('#login-username')).toBeVisible({ timeout: 10_000 });
  // pressSequentially() required — fill() does not trigger React onChange on these inputs
  await page.locator('#login-username').pressSequentially('admin');
  await page.locator('#login-password').pressSequentially('wrong-password-xyz');

  // Submit button exact text: "Sign in" (login.tsx:168)
  await page.getByRole('button', { name: 'Sign in' }).click();

  // Error display: login.tsx:150-153 renders a <div> with style={{ color: 'var(--color-error)' }}
  // when status === 'error'. No testid — match on the inline style.
  const errorEl = page.locator('div[style*="color: var(--color-error)"], div[style*="color-error"]').first();
  await expect(errorEl).toBeVisible({ timeout: 15_000 });

  // Must remain on login route (HashRouter: /#/login)
  expect(page.url()).toMatch(/login/);
});

test('(c) dev_mode_bypass = true shows red persistent banner on every route', async ({ page }) => {
  // First establish an authenticated session without any state mocking. Only
  // after we're logged in do we install the /state mock — otherwise the mock's
  // route.fetch() replay can return an anonymous state body and break the
  // login flow (the replay uses the request headers from the page, which may
  // not include a bearer token during the anonymous onboarding redirect).
  await loginAs(page, 'admin', 'admin123');

  // Now mock GET /api/v1/state to force dev_mode_bypass=true. We use a full
  // synthetic response so we don't depend on the replay's auth context.
  await page.route('**/api/v1/state', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ onboarding_complete: true, dev_mode_bypass: true }),
    });
  });

  // Reload so the app boots with the mocked state (clears any cached state).
  await page.goto('/');

  // Wait for the AppShell to render with the mocked dev_mode_bypass state.
  const banner = page.getByTestId('dev-mode-banner');
  await expect(banner).toBeVisible({ timeout: 15_000 });

  // Banner must persist when navigating to another route
  await page.goto('/#/agents');
  await expect(banner).toBeVisible({ timeout: 10_000 });

  // Unregister routes to avoid "route in flight" errors during cleanup
  await page.unrouteAll({ behavior: 'ignoreErrors' });
});

test('(d) sign out clears the omnipus-session cookie server-side', async ({ page }) => {
  // Traces to: preview-on-main-listener-spec.md S14 / FR-020.
  // Given a logged-in browser (real cookie-issuing login, not a mock).
  await loginAs(page, 'admin', 'admin123');

  const cookiesAfterLogin = await page.context().cookies();
  expect(
    cookiesAfterLogin.find((c) => c.name === SESSION_COOKIE_NAME),
    'omnipus-session cookie must be present after login',
  ).toBeTruthy();

  // When the SPA's "Sign out" action fires — Sidebar.tsx handleLogout calls
  // POST /api/v1/auth/logout (HandleLogout → middleware.ClearSessionCookie)
  // BEFORE clearing local UI state and navigating away. Drive the real menu,
  // not api.ts's logout() directly, so this exercises the actual wiring a
  // regression could break (e.g. a future edit that clears local state first
  // and skips the network call entirely).
  const hamburger = page.locator('#sidebar-hamburger');
  await expect(hamburger).toBeVisible({ timeout: 10_000 });
  await hamburger.click();

  const profileTrigger = page.locator('[data-testid="sidebar-profile-trigger"]');
  await expect(profileTrigger).toBeVisible({ timeout: 10_000 });
  await profileTrigger.click();

  await page.getByRole('menuitem', { name: 'Sign out' }).click();

  // Then the response clears omnipus-session, and a later /api/v1/* replaying
  // the old cookie would 401 — handleLogout's own navigate({to:'/login'})
  // confirms the round trip settled (it fires in a .finally() after the
  // logout() call resolves or rejects).
  await expect(page).toHaveURL(/\/#\/login/, { timeout: 15_000 });

  // The Set-Cookie: omnipus-session=; Max-Age=0 response expires the cookie
  // immediately — browsers drop expired cookies from the jar outright, so it
  // must no longer appear at all (not just have an empty value).
  const cookiesAfterLogout = await page.context().cookies();
  expect(
    cookiesAfterLogout.find((c) => c.name === SESSION_COOKIE_NAME),
    'omnipus-session cookie must be cleared after logout',
  ).toBeUndefined();
});

/**
 * Session-cookie rotation recovery: auth tests do fresh logins, and each
 * login OVERWRITES the single-slot session_token_hash for the `admin` user
 * (pkg/gateway/rest_auth.go HandleLogin) — so every earlier omnipus-session
 * cookie (including the one global-setup.ts captured before this file ran,
 * and test (d)'s explicitly-cleared one) is invalidated. After auth tests
 * complete, re-login (a real UI login, exactly what the spec's regression
 * note #7 asks for) and update the shared storageState so all subsequent
 * spec files (chat, settings, etc.) get a valid session.
 *
 * context.storageState() captures whatever the browser context is actually
 * holding after loginAs() — the real omnipus-session + CSRF cookies the
 * server just issued — there is nothing left to mirror by hand (the SPA has
 * no JS-visible token to copy from sessionStorage anymore, ADR-044).
 */
test.afterAll(async () => {
  const browser = await chromium.launch();
  const context = await browser.newContext({
    baseURL: process.env.OMNIPUS_URL || 'http://localhost:6060',
  });
  const page = await context.newPage();
  await page.goto('/');
  await loginAs(page, 'admin', 'admin123');
  await context.storageState({ path: AUTH_FILE });
  await browser.close();
});
