import { type Page, expect } from '@playwright/test';

export interface Credentials {
  username: string;
  password: string;
}

/**
 * Return true when the user is already authenticated via a REAL session —
 * not merely rendering the authenticated shell because of dev_mode_bypass.
 *
 * Uses the banner landmark (the top-level <header> element rendered by AppShell) —
 * always present on authenticated routes. The element is a plain <header> tag;
 * HTML5 gives it the implicit ARIA role "banner" so we match by role, not attribute.
 * The sidebar nav is only visible while the overlay drawer is open, so
 * nav[aria-label="Main navigation"] is NOT a reliable auth indicator.
 *
 * IMPORTANT (ADR-044 / US-5 regression, 2026-07-16): the banner alone is NOT a
 * reliable "already logged in" signal in this harness. The e2e CI config
 * always sets `gateway.dev_mode_bypass: true` (.github/workflows/pr.yml "Seed
 * gateway config" step), and `checkBearerAuth` (pkg/gateway/auth.go) grants
 * `devBypassUser` access to ANY request that has no `Authorization: Bearer`
 * header — UNCONDITIONALLY, regardless of whether a valid omnipus-session
 * cookie exists (the SPA never sends a bearer header anymore; ADR-044 removed
 * getAuthHeaders()). That means the `/_app` route guard's own auth check
 * (`GET /api/v1/auth/validate`, also gated by `withAuth`/`checkBearerAuth`)
 * succeeds via bypass on a COMPLETELY FRESH, never-logged-in browser context
 * — the AppShell (and its banner) renders with no real login having ever
 * occurred. A banner-only check would make `loginAs` short-circuit and skip
 * submitting the real login form entirely, so a genuine `omnipus-session`
 * cookie is never issued — exactly the failure auth.spec.ts (a)/(d) caught
 * (asserting the real cookie is present after `loginAs`, consistently absent).
 *
 * Fix: also require the real `omnipus-session` cookie (the credential a
 * genuine login issues via middleware.WriteSessionCookie) before treating the
 * page as already authenticated. Callers that start from the shared
 * storageState (which already carries a real cookie captured by a genuine
 * REST login in global-setup.ts) are unaffected — the fast path still fires
 * immediately. Callers that start from an intentionally empty storageState
 * (auth.spec.ts) now correctly fall through to a real login-form submission.
 */
async function isAuthenticated(page: Page): Promise<boolean> {
  const bannerVisible = await page.getByRole('banner').isVisible({ timeout: 2_000 });
  if (!bannerVisible) return false;
  const cookies = await page.context().cookies();
  return cookies.some((c) => c.name === 'omnipus-session');
}

/**
 * Complete the local-mode onboarding wizard (src/routes/onboarding.tsx) with
 * EXACT selectors from the SPA. The wizard runs BEFORE any session exists
 * (the FR-050 pre-auth window), so finishing it does not sign the admin in —
 * the caller lands on the login form afterwards and signs in with the
 * account it just created (loginAs handles that hand-off).
 *
 * Step 1 — admin username: #admin-username → "Continue"
 * Step 2 — admin password: #admin-password / #admin-password-confirm → "Continue"
 * Step 3 — preferences: #pref-name → "Continue"
 * Step 4 — provider: pick the OpenRouter Popular tile
 *           (picker-popular-openrouter) → type the key into the second-level
 *           panel (provider-detail-panel-api-key-input) → confirm
 *           (provider-detail-panel-continue) → choose the first model
 *           (onboarding-model-select, then the first onboarding-model-* item;
 *           choosing auto-probes it, FR-029) → "Finish" once the probe passed
 * Done   — "Start chatting" on the Meet-your-Assistant screen
 *
 * The API key is sourced from OPENROUTER_API_KEY_CI (or OPENROUTER_API_KEY as a
 * fallback for local worker runs); tests will fail with a real probe error if
 * it is absent — that is intentional: Finish only enables on a passed probe.
 *
 * IMPORTANT: pressSequentially() is used instead of fill() on the admin
 * inputs because React's synthetic onChange is not triggered by fill() on
 * controlled inputs — the Continue button stays disabled={!username.trim()}
 * without real keystroke events.
 */
async function completeOnboarding(page: Page, creds: Credentials): Promise<void> {
  const apiKey =
    process.env.OPENROUTER_API_KEY_CI ??
    process.env.OPENROUTER_API_KEY ??
    'sk-test-placeholder';

  // ── Step 1 — Admin username ───────────────────────────────────────────────
  await expect(page).toHaveURL(/onboarding/, { timeout: 15_000 });
  await expect(page.locator('#admin-username')).toBeVisible({ timeout: 10_000 });
  await page.locator('#admin-username').pressSequentially(creds.username);
  await page.getByRole('button', { name: /^continue$/i }).click();

  // ── Step 2 — Admin password ───────────────────────────────────────────────
  await expect(page.locator('#admin-password')).toBeVisible({ timeout: 10_000 });
  await page.locator('#admin-password').pressSequentially(creds.password);
  await page.locator('#admin-password-confirm').pressSequentially(creds.password);
  await page.getByRole('button', { name: /^continue$/i }).click();

  // ── Step 3 — Preferences ──────────────────────────────────────────────────
  await expect(page.locator('#pref-name')).toBeVisible({ timeout: 10_000 });
  await page.locator('#pref-name').fill(creds.username);
  await page.getByRole('button', { name: /^continue$/i }).click();

  // ── Step 4 — Provider ─────────────────────────────────────────────────────
  // The ONE picker (FR-021): OpenRouter is a Popular tile in the shipped
  // catalog (pkg/providers/catalog/data/providers_catalog.json, tier "popular").
  await page.getByTestId('picker-popular-openrouter').click();
  const panel = page.getByTestId('provider-detail-panel');
  await expect(panel).toBeVisible({ timeout: 8_000 });
  await panel.getByTestId('provider-detail-panel-api-key-input').fill(apiKey);
  await panel.getByTestId('provider-detail-panel-continue').click();
  await expect(page.getByTestId('onboarding-provider-summary')).toBeVisible();

  // Choosing a model auto-probes it (FR-029); Finish enables only when the
  // probe for THAT model passed.
  await page.getByTestId('onboarding-model-select').click();
  await page
    .locator('[data-testid^="onboarding-model-"]:not([data-testid="onboarding-model-select"])')
    .first()
    .click();
  const finishBtn = page.getByRole('button', { name: /^finish$/i });
  await expect(finishBtn).toBeEnabled({ timeout: 30_000 });
  await finishBtn.click();

  // ── Done — Meet your Assistant ────────────────────────────────────────────
  const startChatting = page.getByRole('button', { name: 'Start chatting' });
  await expect(startChatting).toBeVisible({ timeout: 15_000 });
  await startChatting.click();

  // Post-condition: the wizard is finished. In local mode no session was
  // minted, so the /_app guard sends a fresh browser to the login form —
  // loginAs signs in from there.
  await expect(page.locator('#login-username').or(page.getByRole('banner'))).toBeVisible({
    timeout: 15_000,
  });
}

async function completeLoginForm(page: Page, creds: Credentials): Promise<void> {
  // Use the exact IDs from src/routes/-login-local.tsx (#login-username, #login-password)
  await expect(page.locator('#login-username')).toBeVisible({ timeout: 10_000 });

  // pressSequentially() is required — fill() does not fire React synthetic onChange,
  // leaving the Sign-in button disabled={!username.trim() || !password}.
  await page.locator('#login-username').pressSequentially(creds.username);
  await page.locator('#login-password').pressSequentially(creds.password);

  // Submit button text is "Sign in" (-login-local.tsx)
  await page.getByRole('button', { name: 'Sign in' }).click();

  // After successful login the URL leaves the login page
  await expect(page).not.toHaveURL(/\/#\/login/, { timeout: 15_000 });
}

/**
 * Bring the page to an authenticated state.
 *
 * Idempotent: if a REAL session (banner + omnipus-session cookie) is already
 * present, returns immediately. Detects onboarding vs login form and handles
 * both paths.
 *
 * NOTE: The SPA uses HashRouter — routes appear as /#/login, /#/onboarding etc.
 * URL checks must use fragment-aware patterns.
 */
export async function loginAs(page: Page, username = 'admin', password = 'admin123'): Promise<void> {
  const creds: Credentials = { username, password };

  await page.goto('/');

  // Fast-path: already authenticated with a REAL session.
  if (await isAuthenticated(page)) {
    return;
  }

  const url = page.url();

  if (url.includes('/onboarding')) {
    await completeOnboarding(page, creds);
    if (await isAuthenticated(page)) return;
    await completeLoginForm(page, creds);
    return;
  }

  // On the login form the URL contains /#/login or the page shows #login-username
  const loginUsername = page.locator('#login-username');
  if (await loginUsername.isVisible({ timeout: 5_000 })) {
    await completeLoginForm(page, creds);
    return;
  }

  // Fallback: the wizard's first field on the root route (redirected)
  const adminUsername = page.locator('#admin-username');
  if (await adminUsername.isVisible({ timeout: 5_000 })) {
    await completeOnboarding(page, creds);
    if (await isAuthenticated(page)) return;
    await completeLoginForm(page, creds);
    return;
  }

  // dev_mode_bypass fallback (ADR-044/US-5 regression, 2026-07-16): isAuthenticated
  // returned false (no real omnipus-session cookie) but we landed on neither
  // /login nor /onboarding and neither surface's markers appeared within the
  // probes above. This is the signature of gateway.dev_mode_bypass (always on
  // in the e2e CI config — see isAuthenticated's doc) granting the `/_app`
  // route guard's auth check regardless of any real session, so `page.goto('/')`
  // landed straight on the authenticated shell without ever visiting /login.
  // The router has no reason to redirect an already-"authenticated" bypass
  // session to /login on its own, so force it there directly to get a REAL
  // login-form submission — and therefore a real omnipus-session cookie —
  // instead of silently accepting the bypass-rendered shell as "logged in".
  await page.goto('/#/login');
  await expect(page.locator('#login-username')).toBeVisible({ timeout: 10_000 });
  await completeLoginForm(page, creds);
}
