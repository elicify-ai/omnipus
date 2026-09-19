import { request as playwrightRequest } from '@playwright/test';
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { readCredentials } from './credentials';
import { AUTH_STATE_PATH, RAW_RESULTS_DIR, SCREENSHOTS_DIR } from './paths';

/**
 * Global setup for the D17 in-context touch check.
 *
 * 1. Clears stale per-run artifacts (raw result files, violation
 *    screenshots) from a previous invocation, so an aggregated report never
 *    silently mixes results from two different runs.
 * 2. Logs in for real via `POST /api/v1/auth/login` — the exact same
 *    handler (`HandleLogin`, pkg/gateway/rest_auth.go) the login FORM
 *    posts to — and writes the resulting session + CSRF cookies out as a
 *    Playwright storageState file every project references. This is the
 *    same API-login-instead-of-UI-login approach tests/e2e/global-setup.ts
 *    uses, for the same reason: it is still a genuine, server-minted
 *    session (not a fabricated cookie), just without paying the SPA's
 *    cold-start cost five times (once per project).
 *
 * The UI-driven login path itself is still exercised for real — see
 * specs/task-flows.spec.ts's "sign in" test and the login-route entry in
 * specs/in-context-touch.spec.ts, both of which explicitly start from an
 * EMPTY storageState (test.use({ storageState: { cookies: [], origins: [] } }))
 * and drive the actual form.
 *
 * The cookie alone is not sufficient — `_app`'s route guard also requires
 * `localStorage.omnipus_auth_username` (`hasStoredSession()`,
 * src/store/auth.ts) before it will even ask the server to validate the
 * cookie. `tests/e2e/global-setup.ts` writes that into storageState's
 * `origins[].localStorage`; this does the same. (In practice every
 * authenticated test also calls lib/reauth.ts's `reauthenticate()` right
 * before it runs — see that file's doc comment for why a shared snapshot
 * like this one can't be relied on alone — so this file's storageState is
 * mostly a correct, harmless default rather than what tests actually lean on.)
 */
export default async function globalSetup(): Promise<void> {
  for (const dir of [RAW_RESULTS_DIR, SCREENSHOTS_DIR]) {
    if (existsSync(dir)) rmSync(dir, { recursive: true, force: true });
    mkdirSync(dir, { recursive: true });
  }

  const baseURL = process.env.OMNIPUS_URL ?? 'http://127.0.0.1:6060';
  const { username, password } = readCredentials();

  const context = await playwrightRequest.newContext({ baseURL });
  try {
    const resp = await context.post('/api/v1/auth/login', {
      data: { username, password },
    });
    if (!resp.ok()) {
      const body = await resp.text().catch(() => '');
      throw new Error(
        `[touch-in-context] global setup login failed: POST /api/v1/auth/login -> ${resp.status()}.\n` +
          `Response body: ${body}\n` +
          'Check TOUCH_CHECK_USER/TOUCH_CHECK_PASS and that OMNIPUS_URL points at a running gateway ' +
          'seeded with that admin account.',
      );
    }
    const storageState = await context.storageState();
    const storageStateWithLocalStorage = {
      ...storageState,
      origins: [{ origin: baseURL, localStorage: [{ name: 'omnipus_auth_username', value: username }] }],
    };
    const dir = path.dirname(AUTH_STATE_PATH);
    if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
    writeFileSync(AUTH_STATE_PATH, JSON.stringify(storageStateWithLocalStorage, null, 2));
  } finally {
    await context.dispose();
  }
}
