import { request as pwRequest, type BrowserContext } from '@playwright/test';
import { readCredentials } from './credentials';

/**
 * Logs in fresh, right now, and installs the resulting cookies into an
 * ALREADY-OPEN browser context.
 *
 * Why this exists (not just a nicety): `HandleLogin` (pkg/gateway/rest_auth.go)
 * keeps the session cookie SINGLE-SLOT per user — "Session-cookie token
 * remains single-slot (one browser session cookie per login is the existing
 * contract); overwrite as before." Every login, anywhere, for the "admin"
 * account invalidates whatever session cookie existed before it. This
 * suite's "sign in" task flow performs a REAL login (by design — D17 names
 * "signing in" as a required task flow), so a single global storageState
 * shared across the whole run gets silently invalidated the moment that
 * flow executes, and every other test sharing it starts rendering the login
 * form instead of the page under test — a real, observed failure mode in
 * this suite's own first run (see tests/touch-in-context/README.md).
 *
 * The fix is structural: every authenticated test re-authenticates for
 * itself, immediately before it needs the session, and the whole suite runs
 * with `workers: 1` (playwright.touch-in-context.config.ts) so no second
 * login can land mid-test and invalidate the one this test is relying on.
 *
 * A second, distinct pitfall this function also closes: the cookie alone is
 * NOT enough. `_app`'s route guard (`src/routes/_app.tsx` `beforeLoad`)
 * checks `hasStoredSession()` (src/store/auth.ts) — whether
 * `localStorage.omnipus_auth_username` is set — BEFORE it ever asks the
 * server to validate the session cookie: "A browser that has never signed
 * in has no session to validate... Skip the round trip entirely... and go
 * straight to /login." `context.addCookies()` only ever touches cookies; a
 * context that got its session via a REST call (not the real login FORM)
 * has a perfectly valid session cookie but an empty localStorage, so it
 * still bounces straight to the login page. `tests/e2e/global-setup.ts`
 * solves the exact same problem by writing `omnipus_auth_username` into its
 * storageState's `origins[].localStorage` — this uses `addInitScript`
 * instead since it is patching an ALREADY-OPEN context rather than creating
 * a new one from a storageState file.
 */
export async function reauthenticate(context: BrowserContext, baseURL: string): Promise<void> {
  const { username, password } = readCredentials();
  const api = await pwRequest.newContext({ baseURL });
  try {
    const resp = await api.post('/api/v1/auth/login', { data: { username, password } });
    if (!resp.ok()) {
      const body = await resp.text().catch(() => '');
      throw new Error(
        `[touch-in-context] reauthenticate: POST /api/v1/auth/login -> ${resp.status()}. Body: ${body}`,
      );
    }
    const state = await api.storageState();
    await context.addCookies(state.cookies);
    // Registered before any navigation in this test — fires on every
    // subsequent page load in this context, same as a real login form
    // submission's own `setStoredUsername` call would.
    await context.addInitScript((storedUsername: string) => {
      try {
        window.localStorage.setItem('omnipus_auth_username', storedUsername);
      } catch {
        // localStorage can throw in a locked-down context (rare, e.g.
        // third-party-storage restrictions) — non-fatal, the cookie itself
        // still carries the real session; the test will simply hit the
        // same "never signed in" fast-path a truly fresh browser would.
      }
    }, username);
  } finally {
    await api.dispose();
  }
}
